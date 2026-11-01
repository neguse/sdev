package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// Values of gamescope's screenshot request property.
var shotModes = map[string]int{"": 1, "baseplane": 1, "layers": 2, "composition": 3, "screen": 4}

func (d *device) runGame(gameid string) error {
	if err := checkGameID(gameid); err != nil {
		return err
	}
	if err := d.syncTools(); err != nil {
		return err
	}
	_, stderr, err := d.run("python3 ~/devkit-utils/steam-devkit-rpc run-game gameid=" + shQuote(gameid))
	if err != nil {
		return err
	}
	// The tool exits 0 on a timeout and only says so here. Success means Steam
	// took the request, not that the game started.
	if !strings.Contains(stderr, "success") {
		return fmt.Errorf("run-game: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// waitUntilWritten returns once a file's size stops changing.
func (d *device) waitUntilWritten(remote string) error {
	last := ""
	for range 600 {
		size, _, err := d.run("stat -c %s " + shQuote(remote))
		if err != nil {
			return err
		}
		if size != last && strings.TrimSpace(size) != "0" {
			last = size
		} else if size == last {
			return nil
		}
		time.Sleep(5 * d.poll)
	}
	return fmt.Errorf("%s kept changing", remote)
}

func (d *device) remotePath(p string) string {
	if path.IsAbs(p) {
		return p
	}
	return path.Join(d.home, p)
}

// pull copies a remote directory's files into local, or the files a remote glob
// matches, or one remote file. A glob does not walk the directory, which helps
// where a directory is large or partly unreadable, like the home or /tmp.
func (d *device) pull(remote, local string, include []string) (int, error) {
	remote = d.remotePath(remote)
	if strings.ContainsAny(remote, "*?[") {
		matches, err := d.sftp.Glob(remote)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", remote, err)
		}
		copied := 0
		for _, match := range matches {
			info, err := d.sftp.Lstat(match)
			if err != nil {
				return copied, err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			e := entry{mode: info.Mode(), size: info.Size(), mtime: info.ModTime().Unix()}
			n, err := pullFile(d.sftp, match, path.Base(match), e, local)
			if err != nil {
				return copied, err
			}
			copied += n
		}
		return copied, nil
	}
	info, err := d.sftp.Stat(remote)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", remote, err)
	}
	if !info.IsDir() {
		return 1, getFile(d.sftp, remote, filepath.Join(local, path.Base(remote)))
	}
	return pullTree(d.sftp, remote, local, func(name string) bool {
		return len(include) == 0 || matchesAny(include, name)
	})
}

// push copies a local directory's contents, or one local file, into a remote
// directory, leaving what else is there.
func (d *device) push(local, remote string) error {
	info, err := os.Stat(local)
	if err != nil {
		return err
	}
	fsys := os.DirFS(local)
	files, err := tree{}, error(nil)
	if info.IsDir() {
		files, err = scanLocal(fsys)
		if err != nil {
			return err
		}
	} else {
		fsys = os.DirFS(filepath.Dir(local))
		all, err := scanLocal(fsys)
		if err != nil {
			return err
		}
		files[filepath.Base(local)] = all[filepath.Base(local)]
	}
	_, err = syncTree(d.sftp, fsys, files, d.remotePath(remote), syncOptions{keepExtra: true})
	return err
}

// shell runs a command with this process's standard streams, or opens a terminal
// when there is none. Words are joined with spaces, as ssh does.
func (d *device) shell(command []string) error {
	s, err := d.ssh.NewSession()
	if err != nil {
		return err
	}
	defer s.Close()
	s.Stdin, s.Stdout, s.Stderr = os.Stdin, os.Stdout, os.Stderr
	if len(command) == 0 {
		return d.terminal(s, "")
	}
	return remoteExit(s.Run(strings.Join(command, " ")))
}

func remoteExit(err error) error {
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return exitCodeError(exit.ExitStatus())
	}
	return err
}

func (d *device) terminal(s *ssh.Session, command string) error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return errors.New("this needs a terminal; pass a command to run one without")
	}
	width, height, err := term.GetSize(fd)
	if err != nil {
		return err
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer term.Restore(fd, state)
	if err := s.RequestPty(os.Getenv("TERM"), height, width, ssh.TerminalModes{}); err != nil {
		return err
	}
	if command != "" {
		return remoteExit(s.Run(command))
	}
	if err := s.Shell(); err != nil {
		return err
	}
	return remoteExit(s.Wait())
}

func (d *device) shot(out, mode string) error {
	value, ok := shotModes[mode]
	if !ok {
		return fmt.Errorf("unknown screenshot mode %q", mode)
	}
	variant, _, err := d.run(`. /etc/os-release && echo "$VARIANT_ID"`)
	if err != nil {
		return err
	}
	var remote string
	if strings.TrimSpace(variant) == "vr" {
		remote, err = d.shotFrame()
	} else {
		remote, err = d.shotGamescope(value)
	}
	if err != nil {
		return fmt.Errorf("taking the screenshot: %w", err)
	}
	defer d.sftp.Remove(remote)
	return getFile(d.sftp, remote, out)
}

func (d *device) shotFrame() (string, error) {
	remote := path.Join(d.tmp, "sdev-shot.png")
	if _, err := d.tool("deckard-capture", "--json", "--filename", shQuote(remote)); err != nil {
		return "", err
	}
	return remote, nil
}

func (d *device) shotGamescope(mode int) (string, error) {
	tmp := shQuote(d.tmp)
	cmd := fmt.Sprintf("rm -f %s/gamescope*.png; DISPLAY=:0 xprop -root -f GAMESCOPECTRL_DEBUG_REQUEST_SCREENSHOT 32c -set GAMESCOPECTRL_DEBUG_REQUEST_SCREENSHOT %d", tmp, mode)
	if _, _, err := d.run(cmd); err != nil {
		return "", err
	}
	for range 100 {
		out, _, err := d.run(fmt.Sprintf(`find %s -maxdepth 1 -type f -name "gamescope*.png"`, tmp))
		if err != nil {
			return "", err
		}
		if remote, _, _ := strings.Cut(out, "\n"); remote != "" {
			return remote, d.waitUntilWritten(remote)
		}
		time.Sleep(d.poll)
	}
	return "", errors.New("gamescope wrote no screenshot in time")
}

// sameChecksum compares by content, as rsync --checksum does. Hashing on the
// device is one command, whereas reading the files back would mean sending them.
func (d *device) sameChecksum(fsys fs.FS, root string) (sameFile, error) {
	out, _, err := d.run("cd " + shQuote(root) + " && find . -type f -exec sha256sum {} +")
	if err != nil {
		return nil, err
	}
	remote := map[string]string{}
	for line := range strings.Lines(out) {
		sum, name, ok := strings.Cut(strings.TrimRight(line, "\n"), "  ")
		if ok {
			remote[strings.TrimPrefix(name, "./")] = sum
		}
	}
	return func(name string, l, r entry) bool {
		if l.size != r.size {
			return false
		}
		f, err := fsys.Open(name)
		if err != nil {
			return false
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return false
		}
		return hex.EncodeToString(h.Sum(nil)) == remote[name]
	}, nil
}

type foundDevice struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	Login   string `json:"login,omitempty"`
}

// newFoundDevice reads a DNS-SD record. Only version 1 of its format is defined.
func newFoundDevice(name, address string, port int, txt []string) (foundDevice, bool) {
	dev := foundDevice{Name: name, Address: address, Port: port}
	for _, field := range txt {
		key, value, _ := strings.Cut(field, "=")
		switch key {
		case "txtvers":
			if value != "1" {
				return dev, false
			}
		case "login":
			dev.Login = value
		}
	}
	return dev, true
}

func discover(timeout time.Duration) ([]foundDevice, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, err
	}
	entries := make(chan *zeroconf.ServiceEntry)
	found := []foundDevice{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range entries {
			if len(e.AddrIPv4) == 0 {
				continue
			}
			if dev, ok := newFoundDevice(e.Instance, e.AddrIPv4[0].String(), e.Port, e.Text); ok {
				found = append(found, dev)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := resolver.Browse(ctx, "_steamos-devkit._tcp", "local.", entries); err != nil {
		return nil, err
	}
	<-ctx.Done()
	<-done
	return found, nil
}
