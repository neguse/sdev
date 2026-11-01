package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// Steam reads its launch flags from one file per variable, named after it.
var flagName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var steamClients = map[string]string{
	"os":         "SteamStatus.OS",
	"os-dev":     "SteamStatus.OS_DEV",
	"sideloaded": "SteamStatus.SIDELOADED",
}

const renderdocUnit = "sdev-renderdoc"

// Values of gamescope's screenshot request property.
var shotModes = map[string]int{"": 1, "baseplane": 1, "layers": 2, "composition": 3, "screen": 4}

type deviceStatus struct {
	IsFrame        bool     `json:"is_deckard"`
	SessionSelect  string   `json:"session_select"`
	SessionOptions []string `json:"session_options"`
	SessionStatus  string   `json:"session_status"`
	CEFEnabled     bool     `json:"cef_debugging_enabled"`
	SteamVRLogPath string   `json:"steamvr_logpath"`
}

func (d *device) statusJSON() (string, error) {
	return d.tool("steamos-get-status", "--json")
}

func (d *device) status() (deviceStatus, error) {
	var s deviceStatus
	out, err := d.statusJSON()
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		return s, fmt.Errorf("steamos-get-status: %w in %q", err, out)
	}
	return s, nil
}

func (d *device) listGames() (string, error) {
	return d.tool("steamos-list-games")
}

func (d *device) removeTitle(gameid string) error {
	// steamos-delete interpolates the name into a shell command, so a valid ID is
	// also what makes this safe.
	if err := checkGameID(gameid); err != nil {
		return err
	}
	if _, err := d.tool("steamos-delete", "--delete-title", shQuote(gameid)); err != nil {
		return err
	}
	// steamos-delete leaves these behind.
	_, _, err := d.run(fmt.Sprintf("cd ~/devkit-game && rm -f %[1]s-argv.json %[1]s-env.json %[1]s-settings.json", shQuote(gameid)))
	return err
}

func (d *device) removeAllTitles() error {
	_, err := d.tool("steamos-delete", "--delete-all-titles")
	return err
}

func (d *device) resetSteam() error {
	_, err := d.tool("steamos-delete", "--reset-steam-client")
	return err
}

func (d *device) restart() error {
	s, err := d.status()
	if err != nil {
		return err
	}
	return d.restartWith(s)
}

// waitOutShortSession keeps a restart from counting against Steam. SteamOS takes
// three sessions under 60 seconds for a broken client and repairs it, which moves
// ~/.steam aside and drops the devkit client configuration.
func (d *device) waitOutShortSession() error {
	const start = "/tmp/steamos-short-session-start"
	_, _, err := d.run(fmt.Sprintf(`if [ -f %[1]s ]; then n=$((60 - $(date +%%s) + $(date +%%s -r %[1]s))); if [ "$n" -gt 0 ]; then sleep "$n"; fi; fi`, start))
	return err
}

func (d *device) restartWith(s deviceStatus) error {
	if err := d.waitOutShortSession(); err != nil {
		return err
	}
	cmd := shQuote(s.SessionSelect) + " gamescope"
	if s.IsFrame {
		cmd = "systemctl --user unmask steam ; systemctl --user restart steam"
	}
	_, _, err := d.run(cmd)
	return err
}

func (d *device) setSession(name string) error {
	s, err := d.status()
	if err != nil {
		return err
	}
	if !slices.Contains(s.SessionOptions, name) {
		return fmt.Errorf("unknown session %q: the device offers %s", name, strings.Join(s.SessionOptions, ", "))
	}
	sel := name
	if name == "plasma-x11" || name == "plasma-wayland" {
		sel = "plasma"
	}
	typeFile := `"${XDG_CONF_DIR:-$HOME/.config}/steamos-session-type"`
	cmd := "rm -f " + typeFile
	if name == "plasma-wayland" {
		cmd = "echo wayland > " + typeFile
	}
	if _, _, err := d.run(cmd); err != nil {
		return err
	}
	if err := d.waitOutShortSession(); err != nil {
		return err
	}
	if _, _, err := d.run(shQuote(s.SessionSelect) + " " + shQuote(sel)); err != nil {
		return err
	}
	// Steam Deck may keep reporting "error" for a session that did switch, so this
	// is not worth failing over. The official client only warns too.
	for range 5 {
		time.Sleep(10 * d.poll)
		if s, err := d.status(); err == nil && s.SessionStatus == name {
			return nil
		}
	}
	fmt.Fprintf(os.Stderr, "sdev: the device does not report session %s yet\n", name)
	return nil
}

// setClient defaults to the ID the official client passes when it switches
// clients from its devices window.
func (d *device) setClient(client, gameid, args string, gdbserver bool) error {
	config, ok := steamClients[client]
	if !ok {
		return fmt.Errorf("unknown client %q: use os, os-dev or sideloaded", client)
	}
	if gameid == "" {
		gameid = "steam"
	}
	if err := checkGameID(gameid); err != nil {
		return err
	}
	cmd := []string{"--client", config, "--gameid", shQuote(gameid)}
	if args != "" {
		cmd = append(cmd, "--args="+shQuote(args))
	}
	if gdbserver {
		cmd = append(cmd, "--gdbserver")
	}
	if _, err := d.tool("steamos-set-steam-client", cmd...); err != nil {
		return err
	}
	return d.restart()
}

// enableCEF returns the console's URL, restarting Steam only if it has to.
func (d *device) enableCEF() (string, error) {
	s, err := d.status()
	if err != nil {
		return "", err
	}
	if !s.CEFEnabled {
		if _, _, err := d.run("touch ~/.steam/steam/.cef-enable-remote-debugging"); err != nil {
			return "", err
		}
		if err := d.restartWith(s); err != nil {
			return "", err
		}
		time.Sleep(50 * d.poll)
	}
	return "http://" + net.JoinHostPort(d.host, "8081"), nil
}

func (d *device) setFlags(set kv, unset []string) error {
	for _, name := range unset {
		if !flagName.MatchString(name) {
			return fmt.Errorf("invalid flag name %q", name)
		}
	}
	for name := range set {
		if !flagName.MatchString(name) {
			return fmt.Errorf("invalid flag name %q", name)
		}
	}
	const dir = `"$XDG_RUNTIME_DIR/steam/env`
	cmds := []string{"mkdir -p " + dir + `"`}
	for _, name := range unset {
		cmds = append(cmds, "rm -f "+dir+"/"+name+`"`)
	}
	for name, value := range set {
		cmds = append(cmds, "echo "+shQuote(value)+" > "+dir+"/"+name+`"`)
	}
	_, _, err := d.run(strings.Join(cmds, " && "))
	return err
}

func (d *device) renderdocServer(on bool) error {
	// Stopping what is not running is not an error.
	if _, _, err := d.run("systemctl --user stop " + renderdocUnit + " || true; killall -9 renderdoccmd || true"); err != nil {
		return err
	}
	if !on {
		return nil
	}
	// A process started from SSH dies with the connection, even in its own
	// session, so systemd has to own it.
	_, _, err := d.run("systemd-run --user --unit=" + renderdocUnit + " --setenv=RENDERDOC_TEMP=" + shQuote(d.home) + " renderdoccmd remoteserver")
	return err
}

func (d *device) perfOverlay(on bool) error {
	// The setting is the opposite way round.
	_, _, err := d.run("mangohudctl set no_display " + strconv.FormatBool(!on))
	return err
}

func (d *device) perfStart() error {
	_, _, err := d.run("mangohudctl set log_session true")
	return err
}

func (d *device) perfStop(dest string) (int, error) {
	if _, _, err := d.run("mangohudctl set log_session false"); err != nil {
		return 0, err
	}
	return d.pullGlob(path.Join(d.home, "mangoapp_*.csv"), dest)
}

func (d *device) gpuTrace(out string) error {
	tmp, _, err := d.run("mktemp -p " + shQuote(d.tmp) + " XXXXXXXXX-trace.zip")
	if err != nil {
		return err
	}
	remote := strings.TrimSpace(tmp)
	// gpu-trace's service writes the file itself and wants the name to be free.
	if _, _, err := d.run("rm -f " + shQuote(remote)); err != nil {
		return err
	}
	defer d.run("rm -f " + shQuote(remote))
	capture := "gpu-trace --capture --no-gpuvis -o " + shQuote(remote)
	if _, stderr, err := d.run(capture); err != nil || strings.Contains(stderr, "Failed to capture trace") {
		// Most likely tracing is not on yet: turn it on and let the buffers fill.
		if _, _, err := d.run("gpu-trace --start"); err != nil {
			return err
		}
		time.Sleep(50 * d.poll)
		if _, stderr, err := d.run(capture); err != nil {
			return err
		} else if strings.Contains(stderr, "Failed to capture trace") {
			return errors.New(strings.TrimSpace(stderr))
		}
	}
	return getFile(d.sftp, remote, out)
}

func (d *device) rgpCapture(dest string) (string, error) {
	// Steam points RADV's trigger here and RADV always writes to /tmp.
	if _, _, err := d.run("rm -f /tmp/*.rgp; touch /tmp/rgp.trigger"); err != nil {
		return "", err
	}
	remote := ""
	for range 20 {
		if out, _, err := d.run("ls -1t /tmp/*.rgp"); err == nil {
			remote, _, _ = strings.Cut(out, "\n")
			break
		}
		time.Sleep(d.poll)
	}
	if remote == "" {
		return "", errors.New("no capture appeared. It works for Vulkan titles only, with graphics profiling enabled in the device's developer settings")
	}
	if err := d.waitUntilWritten(remote); err != nil {
		return "", err
	}
	local := filepath.Join(dest, path.Base(remote))
	if err := getFile(d.sftp, remote, local); err != nil {
		return "", err
	}
	return local, nil
}

func (d *device) controllerConfig(appid, gameid, dest string) error {
	var args []string
	switch {
	case appid != "":
		if _, err := strconv.ParseUint(appid, 10, 64); err != nil {
			return fmt.Errorf("invalid appid %q", appid)
		}
		args = []string{"--appid", appid}
	case gameid != "":
		if err := checkGameID(gameid); err != nil {
			return err
		}
		args = []string{"--gameid", shQuote(gameid)}
	default:
		return errors.New("controller needs --appid or --gameid")
	}
	if _, _, err := d.run("rm -f " + shQuote(d.tmp) + "/config_*"); err != nil {
		return err
	}
	out, err := d.tool("steamos-dump-controller-config", args...)
	if err != nil {
		return err
	}
	// The tool exits 0 either way.
	var result struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return fmt.Errorf("steamos-dump-controller-config: %w in %q", err, out)
	}
	if result.Error != "" {
		return fmt.Errorf("controller config: %s", strings.TrimSpace(result.Error))
	}
	_, err = d.pullGlob(path.Join(d.tmp, "config_*.vdf"), dest)
	return err
}

// logs downloads what the official client's log sync does, a directory for each
// source. A source the device does not have is skipped.
func (d *device) logs(dest string) error {
	s, err := d.status()
	if err != nil {
		return err
	}
	all := func(string) bool { return true }
	sources := map[string]string{
		"steam": path.Join(d.home, ".local/share/Steam/logs"),
		"dumps": path.Join(d.tmp, "dumps"),
	}
	if s.SteamVRLogPath != "" {
		sources["steamvr"] = s.SteamVRLogPath
	}
	for local, remote := range sources {
		if _, err := pullTree(d.sftp, remote, filepath.Join(dest, local), all); err != nil {
			return err
		}
	}
	_, err = d.pullGlob(path.Join(d.home, "steam-*.log"), filepath.Join(dest, "proton"))
	return err
}

// sideload sends a Steam client build. Unlike a title it gets no shortcut: the
// session starts it once "client sideloaded" selects it.
func (d *device) sideload(o deployOptions) error {
	if !slices.Contains(reservedGameIDs, o.gameid) {
		return fmt.Errorf("%q is not a sideload id: use one of %s", o.gameid, strings.Join(reservedGameIDs, ", "))
	}
	local, err := o.scan()
	if err != nil {
		return err
	}
	directory, err := d.prepareUpload(o.gameid, o.preventRepair)
	if err != nil {
		return err
	}
	p, err := syncTree(d.sftp, os.DirFS(o.dir), local, directory, syncOptions{keepExtra: o.keepExtra})
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d sent, %d removed\n", directory, len(p.put)+len(p.link), len(p.remove))
	argv, err := json.Marshal([]string{commandLine(o.argv)})
	if err != nil {
		return err
	}
	settings := o.settings
	if settings == nil {
		settings = kv{}
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	base := path.Dir(directory)
	for name, content := range map[string][]byte{"-argv.json": argv, "-settings.json": settingsJSON} {
		f, err := d.sftp.Create(path.Join(base, o.gameid+name))
		if err != nil {
			return err
		}
		_, err = f.Write(content)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *device) setPassword() error {
	if err := d.syncTools(); err != nil {
		return err
	}
	s, err := d.ssh.NewSession()
	if err != nil {
		return err
	}
	defer s.Close()
	s.Stdin, s.Stdout, s.Stderr = os.Stdin, os.Stdout, os.Stderr
	return d.terminal(s, "~/devkit-utils/steamos-set-password.sh")
}

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

// pullGlob copies the files a remote glob matches into local.
func (d *device) pullGlob(glob, local string) (int, error) {
	matches, err := d.sftp.Glob(glob)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", glob, err)
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

// pull copies a remote directory's files into local, or the files a remote glob
// matches, or one remote file. A glob does not walk the directory, which helps
// where a directory is large or partly unreadable, like the home or /tmp.
func (d *device) pull(remote, local string, include []string) (int, error) {
	remote = d.remotePath(remote)
	if strings.ContainsAny(remote, "*?[") {
		return d.pullGlob(remote, local)
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
