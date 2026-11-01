package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func TestRun(t *testing.T) {
	d, dev := setup(t)
	if err := dev.runGame("game"); err != nil {
		t.Fatal(err)
	}
	if len(d.commands("run-game gameid='game'")) != 1 {
		t.Errorf("cmds %v", d.cmds)
	}
	d.rpcStderr = "timeout\n"
	if err := dev.runGame("game"); err == nil {
		t.Error("no error")
	}
	if err := dev.runGame("a;b"); err == nil {
		t.Error("accepted a bad id")
	}
}

func TestPullAndPush(t *testing.T) {
	d, dev := setup(t)
	writeFile(t, filepath.Join(d.home, "r", "a.txt"), "aaa")
	writeFile(t, filepath.Join(d.home, "r", "s", "b.log"), "bbb")
	dest := t.TempDir()
	n, err := dev.pull("r", dest, []string{"*.log"})
	if err != nil || n != 1 {
		t.Fatalf("%d, %v", n, err)
	}
	if readFile(t, filepath.Join(dest, "s", "b.log")) != "bbb" {
		t.Error("no b.log")
	}
	if _, err := os.Stat(filepath.Join(dest, "a.txt")); err == nil {
		t.Error("include ignored")
	}
	if n, err := dev.pull("r", dest, []string{"*.log"}); err != nil || n != 0 {
		t.Errorf("second pull: %d, %v", n, err)
	}
	// A glob on the device matches without walking the directory, which may be
	// enormous or partly unreadable.
	writeFile(t, filepath.Join(d.home, "r", "c.csv"), "ccc")
	if err := os.Mkdir(filepath.Join(d.home, "r", "locked"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(d.home, "r", "locked"), 0o755)
	if n, err := dev.pull("r/*.csv", dest, nil); err != nil || n != 1 {
		t.Errorf("glob: %d, %v", n, err)
	}
	if readFile(t, filepath.Join(dest, "c.csv")) != "ccc" {
		t.Error("no c.csv")
	}
	if n, err := dev.pull("r/*.csv", dest, nil); err != nil || n != 0 {
		t.Errorf("second glob: %d, %v", n, err)
	}
	if n, err := dev.pull("r/*.none", dest, nil); err != nil || n != 0 {
		t.Errorf("no match: %d, %v", n, err)
	}
	os.Chmod(filepath.Join(d.home, "r", "locked"), 0o755)
	os.Remove(filepath.Join(d.home, "r", "locked"))
	os.Remove(filepath.Join(d.home, "r", "c.csv"))

	// One remote file, as a plain copy.
	if _, err := dev.pull("r/a.txt", dest, nil); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dest, "a.txt")) != "aaa" {
		t.Error("no a.txt")
	}

	src := writeTree(t, map[string]string{"n/c.txt": "ccc"})
	writeFile(t, filepath.Join(d.home, "up", "keep"), "k")
	if err := dev.push(src, "up"); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(d.home, "up", "n", "c.txt")) != "ccc" || readFile(t, filepath.Join(d.home, "up", "keep")) != "k" {
		t.Error("push went wrong")
	}
	if err := dev.push(filepath.Join(src, "n", "c.txt"), "single"); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(d.home, "single", "c.txt")) != "ccc" {
		t.Error("single file push went wrong")
	}
}

func TestShellPassesTheExitCode(t *testing.T) {
	d, dev := setup(t)
	d.execExit = 7
	err := dev.shell([]string{"false"})
	var code exitCodeError
	if !errors.As(err, &code) || int(code) != 7 {
		t.Errorf("got %v", err)
	}
	d.execExit = 0
	if err := dev.shell([]string{"true"}); err != nil {
		t.Error(err)
	}
}

func TestDeployOptions(t *testing.T) {
	d, dev := setup(t)
	payload := writeTree(t, map[string]string{"run.sh": "x", "a.pdb": "no", "dbg/x": "no", "data/keep": "k"})
	dest := filepath.Join(d.home, "devkit-game", "game")
	writeFile(t, filepath.Join(dest, "extra"), "e")

	// Exclusions drop files at any depth and whole directories.
	opts := deployOptions{dir: payload, gameid: "game", argv: []string{"run.sh"}, exclude: []string{"*.pdb", "dbg"}, keepExtra: true, appid: "480"}
	if err := dev.deploy(opts); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"a.pdb", "dbg"} {
		if _, err := os.Stat(filepath.Join(dest, gone)); err == nil {
			t.Errorf("%s was sent", gone)
		}
	}
	if readFile(t, filepath.Join(dest, "extra")) != "e" {
		t.Error("--no-delete removed a file")
	}
	calls := d.commands("steam-client-create-shortcut")
	if !strings.Contains(calls[0], `"force_appid":"480"`) {
		t.Errorf("appid missing: %s", calls[0])
	}
}

func TestDeployUpdateKeepsNewerFiles(t *testing.T) {
	d, dev := setup(t)
	payload := writeTree(t, map[string]string{"run.sh": "local"})
	dest := filepath.Join(d.home, "devkit-game", "game")
	writeFile(t, filepath.Join(dest, "run.sh"), "newer on device")
	if err := dev.deploy(deployOptions{dir: payload, gameid: "game", argv: []string{"run.sh"}, update: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dest, "run.sh")); got != "local" {
		// Written just now on both sides, so only a strictly newer file may stay.
		if got != "newer on device" {
			t.Errorf("got %q", got)
		}
	}
}

func TestDeployChecksum(t *testing.T) {
	d, dev := setup(t)
	payload := writeTree(t, map[string]string{"run.sh": "abc"})
	dest := filepath.Join(d.home, "devkit-game", "game")
	opts := deployOptions{dir: payload, gameid: "game", argv: []string{"run.sh"}, checksum: true}
	if err := dev.deploy(opts); err != nil {
		t.Fatal(err)
	}
	// Same size and time would hide this from the default comparison.
	info, _ := os.Stat(filepath.Join(payload, "run.sh"))
	writeFile(t, filepath.Join(dest, "run.sh"), "xyz")
	os.Chtimes(filepath.Join(dest, "run.sh"), info.ModTime(), info.ModTime())
	if err := dev.deploy(opts); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dest, "run.sh")); got != "abc" {
		t.Errorf("got %q", got)
	}
}

func TestShotModes(t *testing.T) {
	d, dev := setup(t)
	out := filepath.Join(t.TempDir(), "out.png")
	if err := dev.shot(out, "composition"); err != nil {
		t.Fatal(err)
	}
	if len(d.commands("GAMESCOPECTRL_DEBUG_REQUEST_SCREENSHOT 3")) != 1 {
		t.Errorf("cmds %v", d.cmds)
	}
	if err := dev.shot(out, "nope"); err == nil {
		t.Error("accepted an unknown mode")
	}
}

func TestDeviceFromTXT(t *testing.T) {
	got, ok := newFoundDevice("deck", "10.0.0.2", 32000, []string{"txtvers=1", "login=deck"})
	if !ok || got.Name != "deck" || got.Address != "10.0.0.2" || got.Login != "deck" || got.Port != 32000 {
		t.Errorf("%+v, %v", got, ok)
	}
	if _, ok := newFoundDevice("x", "10.0.0.3", 1, []string{"txtvers=2"}); ok {
		t.Error("accepted an incompatible version")
	}
}

func deckStatus(extra map[string]any) string {
	s := map[string]any{"is_deckard": false, "session_select": "holo-session-select", "session_status": "gamescope", "cef_debugging_enabled": false}
	for k, v := range extra {
		s[k] = v
	}
	out, _ := json.Marshal(s)
	return string(out)
}

func TestStatusAndList(t *testing.T) {
	d, dev := setup(t)
	d.status = deckStatus(nil)
	d.games = `[{"gameid": "a"}]`
	if got, err := dev.statusJSON(); err != nil || got != d.status {
		t.Errorf("status %q, %v", got, err)
	}
	if got, err := dev.listGames(); err != nil || got != d.games {
		t.Errorf("list %q, %v", got, err)
	}
}

func TestRemove(t *testing.T) {
	d, dev := setup(t)
	if err := dev.removeTitle("game"); err != nil {
		t.Fatal(err)
	}
	if len(d.commands("steamos-delete --delete-title 'game'")) != 1 || len(d.commands("-argv.json")) != 1 {
		t.Errorf("cmds %v", d.cmds)
	}
	if err := dev.removeAllTitles(); err != nil {
		t.Fatal(err)
	}
	if len(d.commands("--delete-all-titles")) != 1 {
		t.Errorf("cmds %v", d.cmds)
	}
	// steamos-delete hands the name to a shell.
	if err := dev.removeTitle("x; rm -rf ~"); err == nil {
		t.Error("accepted a bad id")
	}
	if err := dev.resetSteam(); err != nil {
		t.Fatal(err)
	}
	if len(d.commands("--reset-steam-client")) != 1 {
		t.Errorf("cmds %v", d.cmds)
	}
}

func TestResetSteamNeedsYes(t *testing.T) {
	if err := run([]string{"reset-steam", "192.0.2.1"}); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("got %v", err)
	}
}

func TestRestart(t *testing.T) {
	d, dev := setup(t)
	d.status = deckStatus(nil)
	if err := dev.restart(); err != nil {
		t.Fatal(err)
	}
	if len(d.commands("'holo-session-select' gamescope")) != 1 {
		t.Errorf("deck: %v", d.cmds)
	}
	d.status = deckStatus(map[string]any{"is_deckard": true})
	if err := dev.restart(); err != nil {
		t.Fatal(err)
	}
	if len(d.commands("systemctl --user restart steam")) != 1 {
		t.Errorf("frame: %v", d.cmds)
	}
}

func TestSession(t *testing.T) {
	d, dev := setup(t)
	d.status = deckStatus(map[string]any{"session_options": []string{"gamescope", "plasma-x11", "plasma-wayland"}, "session_status": "plasma-wayland"})
	if err := dev.setSession("plasma-wayland"); err != nil {
		t.Fatal(err)
	}
	if len(d.commands("echo wayland >")) != 1 || len(d.commands("'holo-session-select' 'plasma'")) != 1 {
		t.Errorf("cmds %v", d.cmds)
	}
	if err := dev.setSession("nope"); err == nil {
		t.Error("accepted an unknown session")
	}
}

func TestClient(t *testing.T) {
	d, dev := setup(t)
	d.status = deckStatus(nil)
	if err := dev.setClient("os-dev", "", "it's", true); err != nil {
		t.Fatal(err)
	}
	if len(d.commands(`steamos-set-steam-client --client SteamStatus.OS_DEV --gameid 'steam' --args='it'\''s' --gdbserver`)) != 1 {
		t.Errorf("cmds %v", d.cmds)
	}
	if len(d.commands("'holo-session-select' gamescope")) != 1 {
		t.Errorf("no restart: %v", d.cmds)
	}
	if err := dev.setClient("nope", "", "", false); err == nil {
		t.Error("accepted an unknown client")
	}
}

func TestCEF(t *testing.T) {
	d, dev := setup(t)
	d.status = deckStatus(nil)
	url, err := dev.enableCEF()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(url, ":8081") || len(d.commands(".cef-enable-remote-debugging")) != 1 {
		t.Errorf("%q, %v", url, d.cmds)
	}
	d.status = deckStatus(map[string]any{"cef_debugging_enabled": true})
	if _, err := dev.enableCEF(); err != nil {
		t.Fatal(err)
	}
	if len(d.commands(".cef-enable-remote-debugging")) != 1 {
		t.Errorf("restarted Steam needlessly: %v", d.cmds)
	}
}

func TestFlags(t *testing.T) {
	d, dev := setup(t)
	if err := dev.setFlags(kv{"PROTON_LOG": "a b"}, []string{"WINEDEBUG"}); err != nil {
		t.Fatal(err)
	}
	if len(d.commands(`echo 'a b' > "$XDG_RUNTIME_DIR/steam/env/PROTON_LOG"`)) != 1 || len(d.commands(`rm -f "$XDG_RUNTIME_DIR/steam/env/WINEDEBUG"`)) != 1 {
		t.Errorf("cmds %v", d.cmds)
	}
	if err := dev.setFlags(kv{"../x": "1"}, nil); err == nil {
		t.Error("accepted a bad name")
	}
	if err := dev.setFlags(nil, []string{"a/b"}); err == nil {
		t.Error("accepted a bad name")
	}
}

func TestRenderDocServer(t *testing.T) {
	d, dev := setup(t)
	if err := dev.renderdocServer(true); err != nil {
		t.Fatal(err)
	}
	if len(d.commands("renderdoccmd remoteserver")) != 1 {
		t.Errorf("cmds %v", d.cmds)
	}
	if len(d.commands("systemd-run --user")) != 1 {
		t.Errorf("not detached, so it would die with the session: %v", d.cmds)
	}
	// Turning off what is not running is not an error.
	if err := dev.renderdocServer(false); err != nil {
		t.Fatal(err)
	}
	if len(d.commands("killall -9 renderdoccmd || true")) != 2 {
		t.Errorf("cmds %v", d.cmds)
	}
}

func TestPerf(t *testing.T) {
	d, dev := setup(t)
	if err := dev.perfOverlay(true); err != nil {
		t.Fatal(err)
	}
	if err := dev.perfStart(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(d.home, "mangoapp_1.csv"), "csv")
	writeFile(t, filepath.Join(d.home, "other.csv"), "no")
	writeFile(t, filepath.Join(d.home, "sub", "mangoapp_2.csv"), "no")
	dest := t.TempDir()
	if n, err := dev.perfStop(dest); err != nil || n != 1 {
		t.Fatalf("%d, %v", n, err)
	}
	for _, c := range []string{"mangohudctl set no_display false", "mangohudctl set log_session true", "mangohudctl set log_session false"} {
		if len(d.commands(c)) != 1 {
			t.Errorf("%s: %v", c, d.cmds)
		}
	}
	if readFile(t, filepath.Join(dest, "mangoapp_1.csv")) != "csv" {
		t.Error("no csv")
	}
}

func TestGPUTraceRetriesAfterStarting(t *testing.T) {
	d, dev := setup(t)
	d.traceFailsOnce = true
	out := filepath.Join(t.TempDir(), "x", "trace.zip")
	if err := dev.gpuTrace(out); err != nil {
		t.Fatal(err)
	}
	if readFile(t, out) != "trace" || len(d.commands("gpu-trace --start")) != 1 {
		t.Errorf("cmds %v", d.cmds)
	}
}

func TestRGP(t *testing.T) {
	d, dev := setup(t)
	dest := t.TempDir()
	got, err := dev.rgpCapture(dest)
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, got) != "rgp" || filepath.Dir(got) != dest || len(d.commands("touch /tmp/rgp.trigger")) != 1 {
		t.Errorf("%s, %v", got, d.cmds)
	}
	d.rgpMissing = true
	if _, err := dev.rgpCapture(dest); err == nil || !strings.Contains(err.Error(), "Vulkan") {
		t.Errorf("got %v", err)
	}
}

func TestController(t *testing.T) {
	d, dev := setup(t)
	writeFile(t, filepath.Join(d.home, "tmp", "config_1.vdf"), "vdf")
	writeFile(t, filepath.Join(d.home, "tmp", "other"), "no")
	dest := t.TempDir()
	if err := dev.controllerConfig("", "game", dest); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dest, "config_1.vdf")) != "vdf" {
		t.Error("no config")
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 1 {
		t.Errorf("got %v", entries)
	}
	if err := dev.controllerConfig("", "", dest); err == nil {
		t.Error("accepted neither id")
	}
	d.controllerErr = "no such title"
	if err := dev.controllerConfig("480", "", dest); err == nil || !strings.Contains(err.Error(), "no such title") {
		t.Errorf("got %v", err)
	}
}

func TestLogs(t *testing.T) {
	d, dev := setup(t)
	vr := filepath.Join(d.home, "vr", "logs")
	d.status = deckStatus(map[string]any{"steamvr_logpath": sftpPath(vr)})
	writeFile(t, filepath.Join(d.home, ".local/share/Steam/logs/console_log.txt"), "steam")
	writeFile(t, filepath.Join(d.home, "tmp/dumps/a.dmp"), "dump")
	writeFile(t, filepath.Join(vr, "vrserver.txt"), "vr")
	writeFile(t, filepath.Join(d.home, "steam-480.log"), "proton")
	writeFile(t, filepath.Join(d.home, "secret"), "no")
	dest := t.TempDir()
	if err := dev.logs(dest); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"steam/console_log.txt": "steam", "dumps/a.dmp": "dump", "steamvr/vrserver.txt": "vr", "proton/steam-480.log": "proton"} {
		if got := readFile(t, filepath.Join(dest, filepath.FromSlash(name))); got != want {
			t.Errorf("%s: %q", name, got)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "proton", "secret")); err == nil {
		t.Error("pulled an unrelated file")
	}
}

func TestLogsToleratesMissingDirectories(t *testing.T) {
	d, dev := setup(t)
	d.status = deckStatus(nil)
	if err := dev.logs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestSideload(t *testing.T) {
	d, dev := setup(t)
	build := writeTree(t, map[string]string{"steam.sh": "x"})
	if err := dev.sideload(deployOptions{dir: build, gameid: "steam", argv: []string{"-dev", "two words"}}); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(d.home, "devkit-game")
	if readFile(t, filepath.Join(dest, "steam", "steam.sh")) != "x" {
		t.Error("client not sent")
	}
	if got := readFile(t, filepath.Join(dest, "steam-argv.json")); got != `["-dev 'two words'"]` {
		t.Errorf("argv %q", got)
	}
	if readFile(t, filepath.Join(dest, "steam-settings.json")) != "{}" {
		t.Error("no settings")
	}
	if got := d.commands("steam-client-create-shortcut"); len(got) != 0 {
		t.Errorf("registered a client as a title: %v", got)
	}
	if err := dev.sideload(deployOptions{dir: build, gameid: "game"}); err == nil {
		t.Error("accepted an id that is not reserved")
	}
}

func TestClientGameID(t *testing.T) {
	d, dev := setup(t)
	d.status = deckStatus(nil)
	if err := dev.setClient("sideloaded", "steamvr", "", false); err != nil {
		t.Fatal(err)
	}
	if len(d.commands("--client SteamStatus.SIDELOADED --gameid 'steamvr'")) != 1 {
		t.Errorf("cmds %v", d.cmds)
	}
}

func TestRestartsWaitOutTheShortSessionWindow(t *testing.T) {
	d, dev := setup(t)
	d.status = deckStatus(map[string]any{"session_options": []string{"gamescope"}, "session_status": "gamescope"})
	if err := dev.restart(); err != nil {
		t.Fatal(err)
	}
	if err := dev.setSession("gamescope"); err != nil {
		t.Fatal(err)
	}
	waits, selects := 0, 0
	for _, cmd := range d.commands("") {
		switch {
		case strings.Contains(cmd, "steamos-short-session-start"):
			waits++
		case strings.Contains(cmd, "holo-session-select"):
			selects++
			if waits != selects {
				t.Fatalf("selected a session without waiting: %v", d.cmds)
			}
		}
	}
	if selects != 2 {
		t.Errorf("cmds %v", d.cmds)
	}
}

func TestDeployPreventAutoRepair(t *testing.T) {
	d, dev := setup(t)
	payload := writeTree(t, map[string]string{"run.sh": "x"})
	if err := dev.deploy(deployOptions{dir: payload, gameid: "game", argv: []string{"run.sh"}, preventRepair: true}); err != nil {
		t.Fatal(err)
	}
	if len(d.commands("--prevent-auto-repair 1")) != 1 {
		t.Errorf("cmds %v", d.cmds)
	}
}
