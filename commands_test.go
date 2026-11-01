package main

import (
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
