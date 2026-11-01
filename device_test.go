package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// fakeDevice stands in for a devkit: the HTTP pairing service, plus an SSH
// server with real SFTP on a temp home and canned answers for the remote tools.
type fakeDevice struct {
	home     string
	endpoint endpoint

	mu          sync.Mutex
	authorized  map[string]bool
	registered  int
	cmds        []string
	prepareOut  string
	shortcut    string
	rpcStderr   string
	shotContent string
}

func newFakeDevice(t *testing.T) *fakeDevice {
	t.Helper()
	d := &fakeDevice{
		home:        t.TempDir(),
		authorized:  map[string]bool{},
		shortcut:    `{"success": "ok"}`,
		rpcStderr:   "success\n",
		shotContent: "png",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login-name", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "steamos") })
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f := strings.Fields(string(body))
		if len(f) != 3 || f[2] != magicPhrase {
			http.Error(w, "bad", http.StatusForbidden)
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		d.authorized[f[0]+" "+f[1]] = true
		d.registered++
	})
	web := httptest.NewServer(mux)
	t.Cleanup(web.Close)
	_, webPort, _ := net.SplitHostPort(web.Listener.Addr().String())

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(m ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		d.mu.Lock()
		defer d.mu.Unlock()
		id := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
		if m.User() != "steamos" || !d.authorized[id] {
			return nil, fmt.Errorf("denied")
		}
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go d.serve(c, cfg)
		}
	}()
	_, sshPort, _ := net.SplitHostPort(l.Addr().String())
	d.endpoint = endpoint{host: "127.0.0.1", httpPort: webPort, sshPort: sshPort}
	return d
}

func (d *fakeDevice) serve(c net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		ch, reqs, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			defer ch.Close()
			for r := range reqs {
				switch r.Type {
				case "exec":
					var p struct{ Command string }
					ssh.Unmarshal(r.Payload, &p)
					r.Reply(true, nil)
					out, errOut, code := d.exec(p.Command)
					io.WriteString(ch, out)
					io.WriteString(ch.Stderr(), errOut)
					ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{uint32(code)}))
					return
				case "subsystem":
					r.Reply(true, nil)
					s, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(d.home))
					if err == nil {
						s.Serve()
					}
					return
				default:
					r.Reply(false, nil)
				}
			}
		}()
	}
}

func (d *fakeDevice) exec(cmd string) (stdout, stderr string, code int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cmds = append(d.cmds, cmd)
	switch {
	case strings.Contains(cmd, "steamos-prepare-upload"):
		if d.prepareOut != "" {
			return d.prepareOut, "", 0
		}
		id := unquote(cmd[strings.Index(cmd, "--gameid ")+len("--gameid "):])
		dir := filepath.Join(d.home, "devkit-game", id)
		os.MkdirAll(dir, 0o755)
		out, _ := json.Marshal(map[string]string{"user": "steamos", "directory": sftpPath(dir)})
		return string(out), "", 0
	case strings.Contains(cmd, "steam-client-create-shortcut"):
		return d.shortcut, "", 0
	case strings.Contains(cmd, "steam-devkit-rpc"):
		return "", d.rpcStderr, 0
	case strings.Contains(cmd, "gamescopectl"):
		if d.shotContent == "" {
			return "", "no gamescope", 1
		}
		os.WriteFile(filepath.Join(d.home, shotName), []byte(d.shotContent), 0o644)
		return "", "", 0
	}
	return "", "", 0
}

// sftpPath spells a local path the way the SFTP server expects it: on Windows,
// C:\x is /C:/x, and C:/x would read as relative.
func sftpPath(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func unquote(s string) string {
	return strings.ReplaceAll(strings.Trim(s, "'"), `'\''`, "'")
}

func (d *fakeDevice) commands(substr string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, c := range d.cmds {
		if strings.Contains(c, substr) {
			out = append(out, c)
		}
	}
	return out
}

func setup(t *testing.T) (*fakeDevice, *device) {
	t.Helper()
	d := newFakeDevice(t)
	dev, err := connect(d.endpoint, filepath.Join(t.TempDir(), "keys", "devkit_rsa"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dev.Close() })
	return d, dev
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestConnectPairsOnceAndReusesKey(t *testing.T) {
	d := newFakeDevice(t)
	key := filepath.Join(t.TempDir(), "keys", "devkit_rsa")
	for range 2 {
		dev, err := connect(d.endpoint, key)
		if err != nil {
			t.Fatal(err)
		}
		if dev.user != "steamos" {
			t.Errorf("user %q", dev.user)
		}
		dev.Close()
	}
	if d.registered != 1 {
		t.Errorf("registered %d times", d.registered)
	}
}

func TestConnectUnreachableNamesTheHost(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	_, err = connect(endpoint{host: "127.0.0.1", httpPort: port, sshPort: port}, filepath.Join(t.TempDir(), "k"))
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("got %v", err)
	}
}

func TestDeploy(t *testing.T) {
	d, dev := setup(t)
	payload := writeTree(t, map[string]string{"run.sh": "#!/bin/sh\n", "data/a.txt": "aaa"})
	opts := deployOptions{dir: payload, gameid: "game", argv: []string{"run.sh", "two words"}, settings: kv{"compat_tool": "x"}}
	if err := dev.deploy(opts); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(d.home, "devkit-game", "game")
	got, err := os.ReadFile(filepath.Join(dest, "data", "a.txt"))
	if err != nil || string(got) != "aaa" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(d.home, "devkit-utils", "steamos-prepare-upload")); err != nil {
		t.Error(err)
	}

	calls := d.commands("steam-client-create-shortcut")
	if len(calls) != 1 {
		t.Fatalf("shortcut calls: %v", calls)
	}
	var parms struct {
		Gameid, Directory string
		Argv              []string
		Env, Settings     map[string]string
		ForceAppid        *string `json:"force_appid"`
	}
	raw := unquote(calls[0][strings.Index(calls[0], "--parms ")+len("--parms "):])
	if err := json.Unmarshal([]byte(raw), &parms); err != nil {
		t.Fatalf("%v in %s", err, raw)
	}
	if parms.Gameid != "game" || parms.Directory != sftpPath(dest) || parms.Argv[1] != "two words" ||
		parms.Env == nil || parms.Settings["compat_tool"] != "x" || parms.ForceAppid == nil {
		t.Errorf("parms %+v", parms)
	}
	if got := d.commands("steam-devkit-rpc"); len(got) != 0 {
		t.Errorf("ran without --run: %v", got)
	}

	// A file gone from the payload goes from the device, and unchanged files stay.
	if err := os.Remove(filepath.Join(payload, "data", "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := dev.deploy(opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "data", "a.txt")); !os.IsNotExist(err) {
		t.Errorf("stale file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "run.sh")); err != nil {
		t.Error(err)
	}
	if entries, _ := filepath.Glob(filepath.Join(dest, tmpPrefix+"*")); len(entries) != 0 {
		t.Errorf("temp files left: %v", entries)
	}
}

func TestDeploySkipsUnchangedFiles(t *testing.T) {
	d, dev := setup(t)
	payload := writeTree(t, map[string]string{"run.sh": "x"})
	opts := deployOptions{dir: payload, gameid: "game", argv: []string{"run.sh"}}
	if err := dev.deploy(opts); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(d.home, "devkit-game", "game", "run.sh")
	if err := os.WriteFile(remote, []byte("y"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(payload, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(remote, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := dev.deploy(opts); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(remote); string(got) != "y" {
		t.Errorf("same size and mtime was resent: %q", got)
	}
}

func TestDeployReplacesARunningBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ETXTBSY is Linux behaviour")
	}
	d, dev := setup(t)
	dest := filepath.Join(d.home, "devkit-game", "game")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	// A real ELF: running a script would not lock the script's own file.
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	elf, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dest, "game")
	if err := os.WriteFile(bin, elf, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	payload := writeTree(t, map[string]string{"game": "new binary"})
	if err := dev.deploy(deployOptions{dir: payload, gameid: "game", argv: []string{"game"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(bin); string(got) != "new binary" {
		t.Errorf("got %q", got)
	}
}

func TestDeployRun(t *testing.T) {
	d, dev := setup(t)
	payload := writeTree(t, map[string]string{"run.sh": "x"})
	if err := dev.deploy(deployOptions{dir: payload, gameid: "game", argv: []string{"run.sh"}, kill: "game", run: true}); err != nil {
		t.Fatal(err)
	}
	if got := d.commands("pkill -TERM -x 'game'"); len(got) != 1 {
		t.Errorf("kill: %v", d.cmds)
	}
	if got := d.commands("run-game gameid='game'"); len(got) != 1 {
		t.Errorf("run: %v", d.cmds)
	}
}

func TestDeployReportsToolFailures(t *testing.T) {
	payload := writeTree(t, map[string]string{"run.sh": "x"})
	opts := deployOptions{dir: payload, gameid: "game", argv: []string{"run.sh"}, run: true}

	d, dev := setup(t)
	d.shortcut = `{"error": "The Steam client is not running.\n"}`
	if err := dev.deploy(opts); err == nil || !strings.HasSuffix(err.Error(), "not running.") {
		t.Errorf("shortcut: %q", err)
	}

	d, dev = setup(t)
	d.rpcStderr = "timeout\n"
	if err := dev.deploy(opts); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("rpc: %v", err)
	}
}

func TestDeployRefusesBeforeTouchingTheDevice(t *testing.T) {
	d, dev := setup(t)
	good := writeTree(t, map[string]string{"run.sh": "x"})
	tests := []struct {
		name string
		opts deployOptions
	}{
		{"traversal gameid", deployOptions{dir: good, gameid: "../x", argv: []string{"a"}}},
		{"empty gameid", deployOptions{dir: good, argv: []string{"a"}}},
		{"hyphen in gameid", deployOptions{dir: good, gameid: "a-b", argv: []string{"a"}}},
		{"single quote in argv", deployOptions{dir: good, gameid: "g", argv: []string{"a", "it's"}}},
		{"no argv", deployOptions{dir: good, gameid: "g"}},
		{"empty payload", deployOptions{dir: t.TempDir(), gameid: "g", argv: []string{"a"}}},
		{"missing payload", deployOptions{dir: filepath.Join(good, "nope"), gameid: "g", argv: []string{"a"}}},
		{"unmatchable kill", deployOptions{dir: good, gameid: "g", argv: []string{"a"}, kill: "a-very-long-process-name"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := dev.deploy(tt.opts); err == nil {
				t.Error("no error")
			}
		})
	}
	if got := d.commands("python3"); len(got) != 0 {
		t.Errorf("reached the device: %v", got)
	}
}

func TestDeployRefusesUnexpectedDirectory(t *testing.T) {
	d, dev := setup(t)
	precious := filepath.Join(d.home, "precious")
	if err := os.WriteFile(precious, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(map[string]string{"directory": sftpPath(d.home)})
	d.prepareOut = string(out)
	payload := writeTree(t, map[string]string{"run.sh": "x"})
	err := dev.deploy(deployOptions{dir: payload, gameid: "game", argv: []string{"run.sh"}})
	if err == nil || !strings.Contains(err.Error(), "unexpected directory") {
		t.Errorf("got %v", err)
	}
	if _, err := os.Stat(precious); err != nil {
		t.Errorf("deleted files in the wrong directory: %v", err)
	}
}

func TestShot(t *testing.T) {
	d, dev := setup(t)
	out := filepath.Join(t.TempDir(), "out.png")
	if err := dev.shot(out); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(out); string(got) != "png" {
		t.Errorf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(d.home, shotName)); !os.IsNotExist(err) {
		t.Errorf("left on device: %v", err)
	}

	d.shotContent = ""
	if err := dev.shot(out); err == nil {
		t.Error("no error when the screenshot fails")
	}
}

func TestShQuoteRoundTrips(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	for _, s := range []string{"plain", "a b", "it's", `{"a": "$HOME"}`, "`x`", "'"} {
		out, err := exec.Command(sh, "-c", "printf %s "+shQuote(s)).Output()
		if err != nil || string(out) != s {
			t.Errorf("%q: got %q, %v", s, out, err)
		}
	}
}

func TestKeyPath(t *testing.T) {
	tests := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"windows", map[string]string{"LOCALAPPDATA": `C:\L`}, filepath.Join(`C:\L`, "steamos-devkit", "steamos-devkit", "devkit_rsa")},
		{"linux", map[string]string{"XDG_CONFIG_HOME": "/c"}, filepath.Join("/c", "steamos-devkit", "devkit_rsa")},
	}
	for _, tt := range tests {
		got := keyPathFor(tt.goos, func(k string) string { return tt.env[k] }, func() (string, error) { return "/c", nil })
		if got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.goos, got, tt.want)
		}
	}
}

func TestParseArgs(t *testing.T) {
	cmd, err := parseDeploy([]string{"10.0.0.1", "--dir", "d", "--gameid", "g", "--set", "x=y=z", "--kill", "p", "--run", "--", "run.sh", "--flag"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.host != "10.0.0.1" || cmd.dir != "d" || cmd.gameid != "g" || !cmd.run || cmd.kill != "p" ||
		cmd.settings["x"] != "y=z" || len(cmd.argv) != 2 || cmd.argv[1] != "--flag" {
		t.Errorf("got %+v", cmd)
	}
	for _, args := range [][]string{{}, {"--dir", "d"}, {"h", "--set", "noequals"}} {
		if _, err := parseDeploy(args); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}
