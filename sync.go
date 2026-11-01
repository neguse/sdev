package main

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

// tmpPrefix marks a file being uploaded. A leftover from an interrupted run is
// not in the local tree, so the next sync removes it.
const tmpPrefix = ".sdev-tmp-"

type entry struct {
	mode  fs.FileMode
	size  int64
	mtime int64 // Unix seconds, the most SFTP carries
	link  string
}

// tree maps slash-separated paths relative to the synced root to their entries.
type tree map[string]entry

type plan struct {
	remove []string
	mkdir  []string
	put    []string
	link   []string
	chmod  []string
}

// diff lists what brings remote in line with local. Files count as unchanged
// when size and mtime match, like rsync's default.
func diff(local, remote tree) plan {
	var p plan
	for name, r := range remote {
		l, ok := local[name]
		if !ok || l.mode.Type() != r.mode.Type() || (l.mode.Type() == fs.ModeSymlink && l.link != r.link) {
			p.remove = append(p.remove, name)
		}
	}
	for name, l := range local {
		r, ok := remote[name]
		replaced := !ok || slices.Contains(p.remove, name)
		switch {
		case l.mode.IsDir():
			if replaced {
				p.mkdir = append(p.mkdir, name)
			} else if l.mode.Perm() != r.mode.Perm() {
				p.chmod = append(p.chmod, name)
			}
		case l.mode.Type() == fs.ModeSymlink:
			if replaced {
				p.link = append(p.link, name)
			}
		case replaced || l.size != r.size || l.mtime != r.mtime:
			p.put = append(p.put, name)
		case l.mode.Perm() != r.mode.Perm():
			p.chmod = append(p.chmod, name)
		}
	}
	// Reverse order visits children before their parents, sorted order after.
	slices.Sort(p.remove)
	slices.Reverse(p.remove)
	slices.Sort(p.mkdir)
	slices.Sort(p.put)
	slices.Sort(p.link)
	slices.Sort(p.chmod)
	return p
}

func scanLocal(fsys fs.FS) (tree, error) {
	t := tree{}
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := entry{mode: info.Mode(), size: info.Size(), mtime: info.ModTime().Unix()}
		switch {
		case info.Mode().Type() == fs.ModeSymlink:
			if e.link, err = fs.ReadLink(fsys, name); err != nil {
				return err
			}
			e.link = strings.ReplaceAll(e.link, `\`, "/")
		case !info.IsDir() && !info.Mode().IsRegular():
			return fmt.Errorf("%s: unsupported file type %v", name, info.Mode().Type())
		}
		t[name] = e
		return nil
	})
	return t, err
}

// allExecutable is for sources that hold no meaningful Unix permissions: on
// Windows, and in the embedded tools.
func allExecutable(t tree) {
	for name, e := range t {
		if e.mode.Type() != fs.ModeSymlink {
			e.mode = e.mode.Type() | 0o755
			t[name] = e
		}
	}
}

func scanRemote(c *sftp.Client, root string) (tree, error) {
	t := tree{}
	w := c.Walk(root)
	for w.Step() {
		if err := w.Err(); err != nil {
			return nil, err
		}
		if w.Path() == root {
			continue
		}
		info := w.Stat()
		e := entry{mode: info.Mode(), size: info.Size(), mtime: info.ModTime().Unix()}
		if info.Mode().Type() == fs.ModeSymlink {
			var err error
			if e.link, err = c.ReadLink(w.Path()); err != nil {
				return nil, err
			}
		}
		t[strings.TrimPrefix(w.Path(), root+"/")] = e
	}
	return t, nil
}

// syncTree makes the directory root on the device match local, read from fsys.
func syncTree(c *sftp.Client, fsys fs.FS, local tree, root string) (plan, error) {
	if err := c.MkdirAll(root); err != nil {
		return plan{}, fmt.Errorf("%s: %w", root, err)
	}
	remote, err := scanRemote(c, root)
	if err != nil {
		return plan{}, err
	}
	p := diff(local, remote)
	for _, name := range p.remove {
		if err := c.Remove(path.Join(root, name)); err != nil {
			return p, fmt.Errorf("remove %s: %w", name, err)
		}
	}
	for _, name := range p.mkdir {
		dir := path.Join(root, name)
		if err := c.Mkdir(dir); err != nil {
			return p, fmt.Errorf("mkdir %s: %w", name, err)
		}
		if err := c.Chmod(dir, local[name].mode.Perm()); err != nil {
			return p, fmt.Errorf("chmod %s: %w", name, err)
		}
	}
	for _, name := range p.put {
		if err := putFile(c, fsys, name, local[name], path.Join(root, name)); err != nil {
			return p, fmt.Errorf("upload %s: %w", name, err)
		}
	}
	for _, name := range p.link {
		if err := c.Symlink(local[name].link, path.Join(root, name)); err != nil {
			return p, fmt.Errorf("symlink %s: %w", name, err)
		}
	}
	for _, name := range p.chmod {
		if err := c.Chmod(path.Join(root, name), local[name].mode.Perm()); err != nil {
			return p, fmt.Errorf("chmod %s: %w", name, err)
		}
	}
	return p, nil
}

// putFile uploads beside the target and renames over it, because writing into
// the executable of a running game fails with ETXTBSY.
func putFile(c *sftp.Client, fsys fs.FS, name string, e entry, target string) error {
	src, err := fsys.Open(name)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := path.Join(path.Dir(target), tmpPrefix+path.Base(target))
	dst, err := c.Create(tmp)
	if err != nil {
		return err
	}
	_, err = dst.ReadFrom(src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := c.Chmod(tmp, e.mode.Perm()); err != nil {
		return err
	}
	mtime := time.Unix(e.mtime, 0)
	if err := c.Chtimes(tmp, mtime, mtime); err != nil {
		return err
	}
	return c.PosixRename(tmp, target)
}
