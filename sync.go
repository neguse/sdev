package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
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

// sameFile decides whether a local file needs no upload.
type sameFile func(name string, local, remote entry) bool

// sameSizeAndTime is rsync's default comparison.
func sameSizeAndTime(_ string, l, r entry) bool {
	return l.size == r.size && l.mtime == r.mtime
}

// diff lists what brings remote in line with local.
func diff(local, remote tree, same sameFile) plan {
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
		case replaced || !same(name, l, r):
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
		// Everything is sent as 0755, as the official client's rsync --chmod does:
		// Windows and embedded files have no Unix permissions to keep.
		e := entry{mode: info.Mode().Type() | 0o755, size: info.Size(), mtime: info.ModTime().Unix()}
		switch {
		case info.Mode().Type() == fs.ModeSymlink:
			e.mode = info.Mode()
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

type syncOptions struct {
	keepExtra bool     // leave files that are not in local
	same      sameFile // defaults to sameSizeAndTime
}

// keepOnlyReplaced drops removals of paths local does not have.
func (p *plan) keepOnlyReplaced(local tree) {
	p.remove = slices.DeleteFunc(p.remove, func(name string) bool {
		_, ok := local[name]
		return !ok
	})
}

// exclude removes paths matching any glob, tried on the base name and on the
// whole relative path, along with everything below them.
func (t tree) exclude(globs []string) error {
	for _, glob := range globs {
		if _, err := path.Match(glob, ""); err != nil {
			return fmt.Errorf("bad pattern %q: %w", glob, err)
		}
	}
	for name := range t {
		for at := name; at != "."; at = path.Dir(at) {
			if matchesAny(globs, at) {
				delete(t, name)
				break
			}
		}
	}
	return nil
}

func matchesAny(globs []string, name string) bool {
	for _, glob := range globs {
		if ok, _ := path.Match(glob, name); ok {
			return true
		}
		if ok, _ := path.Match(glob, path.Base(name)); ok {
			return true
		}
	}
	return false
}

// syncTree makes the directory root on the device match local, read from fsys.
func syncTree(c *sftp.Client, fsys fs.FS, local tree, root string, o syncOptions) (plan, error) {
	if o.same == nil {
		o.same = sameSizeAndTime
	}
	if err := c.MkdirAll(root); err != nil {
		return plan{}, fmt.Errorf("%s: %w", root, err)
	}
	remote, err := scanRemote(c, root)
	if err != nil {
		return plan{}, err
	}
	p := diff(local, remote, o.same)
	if o.keepExtra {
		p.keepOnlyReplaced(local)
	}
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

// pullTree copies files under root to dest when their size or mtime differ, and
// returns how many it copied. A missing root copies nothing.
func pullTree(c *sftp.Client, root, dest string, include func(name string) bool) (int, error) {
	if _, err := c.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	root, err := c.RealPath(root)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", root, err)
	}
	remote, err := scanRemote(c, root)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", root, err)
	}
	copied := 0
	for name, e := range remote {
		if !e.mode.IsRegular() || !include(name) {
			continue
		}
		n, err := pullFile(c, path.Join(root, name), name, e, dest)
		if err != nil {
			return copied, err
		}
		copied += n
	}
	return copied, nil
}

// pullFile copies one file to name under dest unless it is already there, and
// returns how many it copied.
func pullFile(c *sftp.Client, remote, name string, e entry, dest string) (int, error) {
	// The device chooses these names, so they must not escape dest.
	if !filepath.IsLocal(filepath.FromSlash(name)) {
		return 0, fmt.Errorf("refusing remote path %q", name)
	}
	target := filepath.Join(dest, filepath.FromSlash(name))
	if info, err := os.Stat(target); err == nil && info.Size() == e.size && info.ModTime().Unix() == e.mtime {
		return 0, nil
	}
	if err := getFile(c, remote, target); err != nil {
		return 0, fmt.Errorf("download %s: %w", name, err)
	}
	mtime := time.Unix(e.mtime, 0)
	return 1, os.Chtimes(target, mtime, mtime)
}

func getFile(c *sftp.Client, remote, local string) error {
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return err
	}
	src, err := c.Open(remote)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(local)
	if err != nil {
		return err
	}
	if _, err := src.WriteTo(dst); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}
