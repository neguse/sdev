package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// magicPhrase is what a devkit's pairing service expects after the public key.
const magicPhrase = "900b919520e4cf601998a71eec318fec"

const shotName = ".sdev-shot.png"

// pkill -x compares against the kernel's process name, cut to this length.
const maxProcessName = 15

//go:embed all:devkit-utils
var embedded embed.FS

// The official client's rule. Steam itself refuses a hyphen.
var validGameID = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]+$`)

// The official client treats these as sideloaded Steam clients, not titles.
var reservedGameIDs = []string{"steam", "steamdeckard", "steamvr", "steamvrdeckard"}

// Words made only of these need no quoting, which keeps ordinary commands readable.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./=:,+@%-]+$`)

type endpoint struct {
	host, httpPort, sshPort string
}

func defaultEndpoint(host string) endpoint {
	return endpoint{host: host, httpPort: "32000", sshPort: "22"}
}

type kv map[string]string

type deployOptions struct {
	dir, gameid string
	argv        []string
	settings    kv
	kill        string
	run         bool
}

type device struct {
	user string
	home string
	ssh  *ssh.Client
	sftp *sftp.Client
}

// keyPathFor finds the key the official devkit client creates, so that a device
// paired with either tool works with both.
func keyPathFor(goos string, getenv func(string) string, configDir func() (string, error)) string {
	if local := getenv("LOCALAPPDATA"); goos == "windows" && local != "" {
		return filepath.Join(local, "steamos-devkit", "steamos-devkit", "devkit_rsa")
	}
	dir, err := configDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "steamos-devkit", "devkit_rsa")
}

func defaultKeyPath() string {
	return keyPathFor(runtime.GOOS, os.Getenv, os.UserConfigDir)
}

func loadOrCreateKey(keyPath string) (ssh.Signer, error) {
	data, err := os.ReadFile(keyPath)
	if errors.Is(err, fs.ErrNotExist) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}
		block, err := ssh.MarshalPrivateKey(key, "")
		if err != nil {
			return nil, err
		}
		pub, err := ssh.NewPublicKey(&key.PublicKey)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
			return nil, err
		}
		data = pem.EncodeToMemory(block)
		if err := os.WriteFile(keyPath, data, 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(keyPath+".pub", ssh.MarshalAuthorizedKey(pub), 0o644); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}

func loginName(ep endpoint) (string, error) {
	url := "http://" + net.JoinHostPort(ep.host, ep.httpPort) + "/login-name"
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("no devkit service at %s: is it awake, in developer mode, on this network? (%w)", ep.host, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusOK || name == "" {
		return "", fmt.Errorf("%s: login-name answered %s %q", ep.host, resp.Status, name)
	}
	return name, nil
}

func register(ep endpoint, signer ssh.Signer) error {
	id := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	url := "http://" + net.JoinHostPort(ep.host, ep.httpPort) + "/register"
	// The device may show a prompt, which waits for a person.
	client := http.Client{Timeout: 60 * time.Second}
	resp, err := client.Post(url, "text/plain", strings.NewReader(id+" "+magicPhrase+"\n"))
	if err != nil {
		return fmt.Errorf("pairing with %s: %w", ep.host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pairing with %s: %s %s", ep.host, resp.Status, bytes.TrimSpace(body))
	}
	return nil
}

func dialSSH(ep endpoint, user string, signer ssh.Signer) (*ssh.Client, error) {
	return ssh.Dial("tcp", net.JoinHostPort(ep.host, ep.sshPort), &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		// A device's host key changes on reimage and its address on DHCP. The
		// official client does not pin it either.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	})
}

// connect logs in to the device, pairing first if it does not know the key yet.
func connect(ep endpoint, keyPath string) (*device, error) {
	user, err := loginName(ep)
	if err != nil {
		return nil, err
	}
	signer, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, fmt.Errorf("key %s: %w", keyPath, err)
	}
	client, err := dialSSH(ep, user, signer)
	if err != nil && strings.Contains(err.Error(), "unable to authenticate") {
		fmt.Printf("Pairing: approve the request on %s\n", ep.host)
		if err := register(ep, signer); err != nil {
			return nil, err
		}
		client, err = dialSSH(ep, user, signer)
	}
	if err != nil {
		return nil, fmt.Errorf("ssh %s@%s: %w", user, ep.host, err)
	}
	sc, err := sftp.NewClient(client, sftp.UseConcurrentWrites(true))
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("sftp %s: %w", ep.host, err)
	}
	home, err := sc.Getwd()
	if err != nil {
		sc.Close()
		client.Close()
		return nil, err
	}
	return &device{user: user, home: home, ssh: client, sftp: sc}, nil
}

func (d *device) Close() error {
	d.sftp.Close()
	return d.ssh.Close()
}

// shQuote quotes s as one word for the remote shell.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// commandLine builds the launch command. Steam joins argv with spaces and hands
// it to a shell, so each word is quoted to arrive as given.
func commandLine(argv []string) string {
	words := make([]string, len(argv))
	for i, word := range argv {
		if plainWord.MatchString(word) {
			words[i] = word
		} else {
			words[i] = shQuote(word)
		}
	}
	return strings.Join(words, " ")
}

func (d *device) run(cmd string) (stdout, stderr string, err error) {
	s, err := d.ssh.NewSession()
	if err != nil {
		return "", "", err
	}
	defer s.Close()
	var out, errOut bytes.Buffer
	s.Stdout, s.Stderr = &out, &errOut
	if err := s.Run(cmd); err != nil {
		return out.String(), errOut.String(), fmt.Errorf("%s: %w: %s", cmd, err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), errOut.String(), nil
}

func (d *device) syncTools() error {
	tools, err := fs.Sub(embedded, "devkit-utils")
	if err != nil {
		return err
	}
	local, err := scanLocal(tools)
	if err != nil {
		return err
	}
	// Embedded files carry no mtime, so stamping them now resends all of them
	// and a new sdev cannot leave older tools behind.
	now := time.Now().Unix()
	for name, e := range local {
		e.mtime = now
		local[name] = e
	}
	_, err = syncTree(d.sftp, tools, local, path.Join(d.home, "devkit-utils"))
	return err
}

func (o deployOptions) validate() (tree, error) {
	if !validGameID.MatchString(o.gameid) {
		return nil, fmt.Errorf("invalid game id %q: must match %s", o.gameid, validGameID)
	}
	if slices.Contains(reservedGameIDs, strings.ToLower(o.gameid)) {
		return nil, fmt.Errorf("game id %q is reserved for sideloaded Steam clients", o.gameid)
	}
	if len(o.argv) == 0 {
		return nil, errors.New("no command to launch")
	}
	if len(o.kill) > maxProcessName {
		return nil, fmt.Errorf("process name %q is over %d characters, so pkill cannot match it", o.kill, maxProcessName)
	}
	local, err := scanLocal(os.DirFS(o.dir))
	if err != nil {
		return nil, err
	}
	// An empty source would delete the whole game from the device.
	if len(local) == 0 {
		return nil, fmt.Errorf("%s is empty", o.dir)
	}
	return local, nil
}

func (d *device) deploy(o deployOptions) error {
	local, err := o.validate()
	if err != nil {
		return err
	}
	if err := d.syncTools(); err != nil {
		return fmt.Errorf("installing devkit-utils: %w", err)
	}
	out, _, err := d.run("python3 ~/devkit-utils/steamos-prepare-upload --gameid " + shQuote(o.gameid))
	if err != nil {
		return err
	}
	var prepared struct {
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal([]byte(out), &prepared); err != nil {
		return fmt.Errorf("steamos-prepare-upload: %w in %q", err, out)
	}
	// The sync deletes anything extra, so never trust this path blindly.
	if path.Base(prepared.Directory) != o.gameid || path.Base(path.Dir(prepared.Directory)) != "devkit-game" {
		return fmt.Errorf("steamos-prepare-upload chose an unexpected directory: %s", prepared.Directory)
	}

	p, err := syncTree(d.sftp, os.DirFS(o.dir), local, prepared.Directory)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d sent, %d removed\n", prepared.Directory, len(p.put)+len(p.link), len(p.remove))

	if err := d.createShortcut(o, prepared.Directory); err != nil {
		return err
	}
	if !o.run {
		return nil
	}
	if o.kill != "" {
		// Otherwise a previous run stays alive beside the new one.
		if _, _, err := d.run("pkill -TERM -x " + shQuote(o.kill) + " || true"); err != nil {
			return err
		}
	}
	_, stderr, err := d.run("python3 ~/devkit-utils/steam-devkit-rpc run-game gameid=" + shQuote(o.gameid))
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

func (d *device) createShortcut(o deployOptions, directory string) error {
	settings := o.settings
	if settings == nil {
		settings = kv{}
	}
	parms, err := json.Marshal(map[string]any{
		"gameid":      o.gameid,
		"directory":   directory,
		"argv":        []string{commandLine(o.argv)},
		"env":         kv{},
		"settings":    settings,
		"force_appid": "",
	})
	if err != nil {
		return err
	}
	out, _, err := d.run("python3 ~/devkit-utils/steam-client-create-shortcut --parms " + shQuote(string(parms)))
	if err != nil {
		return err
	}
	// The tool exits 0 even when Steam refuses.
	var result struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return fmt.Errorf("steam-client-create-shortcut: %w in %q", err, out)
	}
	if result.Error != "" {
		return fmt.Errorf("registering with Steam: %s", strings.TrimSpace(result.Error))
	}
	return nil
}

func (d *device) shot(out string) error {
	remote := path.Join(d.home, shotName)
	variant, _, err := d.run(`. /etc/os-release && echo "$VARIANT_ID"`)
	if err != nil {
		return err
	}
	if strings.TrimSpace(variant) == "vr" {
		// Steam Frame has no gamescope to ask.
		if err := d.syncTools(); err != nil {
			return fmt.Errorf("installing devkit-utils: %w", err)
		}
		_, _, err = d.run("python3 ~/devkit-utils/deckard-capture --json --filename " + shQuote(remote))
	} else {
		// gamescope writes the file asynchronously.
		_, _, err = d.run(fmt.Sprintf("rm -f %[1]s; gamescopectl screenshot %[1]s >/dev/null 2>&1; sleep 1; test -s %[1]s", shQuote(remote)))
	}
	if err != nil {
		return fmt.Errorf("taking the screenshot: %w", err)
	}
	defer d.sftp.Remove(remote)
	src, err := d.sftp.Open(remote)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(out)
	if err != nil {
		return err
	}
	if _, err := src.WriteTo(dst); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}
