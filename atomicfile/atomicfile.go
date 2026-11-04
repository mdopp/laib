// Package atomicfile writes a file so that a power cut leaves the old file
// or the new one, never an empty one (#148; the shell's #107): the temp
// file is synced before the rename, the directory after it. The kernel and
// the programs that may not import it (laib-haushalt, laib-lease) share
// it instead of keeping copies; it imports nothing of laib.
package atomicfile

import (
	"os"
	"path/filepath"
)

// syncFile is the fsync; a test records what was synced, in which order.
var syncFile = func(f *os.File) error { return f.Sync() }

// Write replaces path with data. tmp is the caller's temp name, which may
// be ignored by name (the state repo's .gitignore holds secrets.age.tmp).
func Write(tmp, path string, data []byte, perm os.FileMode) error {
	return write(host{}, tmp, path, data, perm)
}

// WriteIn is Write inside a root (the Operator's jail, #35): tmp and rel
// are relative to it and never leave it.
func WriteIn(root *os.Root, tmp, rel string, data []byte, perm os.FileMode) error {
	return write(root, tmp, rel, data, perm)
}

// MoveDir renames a directory to its new place and syncs both parents
// (laib#1021, a renamed service's data dir): a power cut leaves it at one
// place or the other, never forgotten in between.
func MoveDir(old, path string) error {
	if err := os.Rename(old, path); err != nil {
		return err
	}
	if err := syncDir(os.Open(filepath.Dir(path))); err != nil {
		return err
	}
	return syncDir(os.Open(filepath.Dir(old)))
}

// fsys is what a write needs of the file system: the host's, or an *os.Root.
type fsys interface {
	OpenFile(name string, flag int, perm os.FileMode) (*os.File, error)
	Open(name string) (*os.File, error)
	Rename(oldname, newname string) error
	Remove(name string) error
}

type host struct{}

func (host) OpenFile(n string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(n, flag, perm)
}
func (host) Open(n string) (*os.File, error) { return os.Open(n) }
func (host) Rename(o, n string) error        { return os.Rename(o, n) }
func (host) Remove(n string) error           { return os.Remove(n) }

func write(fs fsys, tmp, path string, data []byte, perm os.FileMode) error {
	f, err := fs.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if err := finish(f, data); err != nil {
		fs.Remove(tmp)
		return err
	}
	if err := fs.Rename(tmp, path); err != nil {
		fs.Remove(tmp)
		return err
	}
	return syncDir(fs.Open(filepath.Dir(path)))
}

func finish(f *os.File, data []byte) error {
	_, err := f.Write(data)
	if err == nil {
		err = syncFile(f)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func syncDir(d *os.File, err error) error {
	if err != nil {
		return err
	}
	defer d.Close()
	return syncFile(d)
}
