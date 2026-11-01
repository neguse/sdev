package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func file(size, mtime int64) entry {
	return entry{mode: 0o644, size: size, mtime: mtime}
}

func TestDiff(t *testing.T) {
	dir := entry{mode: fs.ModeDir | 0o755}
	link := func(target string) entry { return entry{mode: fs.ModeSymlink | 0o777, link: target} }
	tests := []struct {
		name          string
		local, remote tree
		want          plan
	}{
		{name: "identical", local: tree{"a": file(1, 1)}, remote: tree{"a": file(1, 1)}},
		{
			name:   "size or mtime change is sent",
			local:  tree{"a": file(2, 1), "b": file(1, 2)},
			remote: tree{"a": file(1, 1), "b": file(1, 1)},
			want:   plan{put: []string{"a", "b"}},
		},
		{
			name:   "new directories come before their children",
			local:  tree{"d": dir, "d/e": dir, "d/e/f": file(1, 1)},
			remote: tree{},
			want:   plan{mkdir: []string{"d", "d/e"}, put: []string{"d/e/f"}},
		},
		{
			name:   "stale paths go children first",
			local:  tree{},
			remote: tree{"d": dir, "d/e": file(1, 1)},
			want:   plan{remove: []string{"d/e", "d"}},
		},
		{
			name:   "a kind change removes then recreates",
			local:  tree{"x": file(1, 1), "y": dir},
			remote: tree{"x": dir, "x/z": file(1, 1), "y": file(1, 1)},
			want:   plan{remove: []string{"y", "x/z", "x"}, mkdir: []string{"y"}, put: []string{"x"}},
		},
		{
			name:   "a retargeted link is replaced",
			local:  tree{"l": link("new"), "same": link("t")},
			remote: tree{"l": link("old"), "same": link("t")},
			want:   plan{remove: []string{"l"}, link: []string{"l"}},
		},
		{
			name:   "a mode change alone is only a chmod",
			local:  tree{"a": {mode: 0o755, size: 1, mtime: 1}},
			remote: tree{"a": file(1, 1)},
			want:   plan{chmod: []string{"a"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diff(tt.local, tt.remote); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestScanLocal(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "d", "run.sh"), []byte("hi"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := scanLocal(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["d"].mode.IsDir() {
		t.Fatalf("got %+v", got)
	}
	if e := got["d/run.sh"]; e.size != 2 || e.mtime == 0 {
		t.Errorf("got %+v", e)
	}
}

func TestScanLocalLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	root := t.TempDir()
	if err := os.Symlink("lib.so.1", filepath.Join(root, "lib.so")); err != nil {
		t.Fatal(err)
	}
	got, err := scanLocal(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	if e := got["lib.so"]; e.mode.Type() != fs.ModeSymlink || e.link != "lib.so.1" {
		t.Errorf("got %+v", e)
	}
}

func TestScanLocalMakesEverythingExecutable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := scanLocal(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	if got["a"].mode != 0o755 {
		t.Errorf("mode %v", got["a"].mode)
	}
}
