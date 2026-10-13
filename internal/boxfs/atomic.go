package boxfs

import (
	"os"
	"path/filepath"
)

// syncFile is the fsync; a test records what was synced, in which order.
var syncFile = func(f *os.File) error { return f.Sync() }

// WriteAtomic replaces path with data so that a power cut leaves the old
// file or the new one, never an empty one (#148; the shell's #107): the
// temp file is synced before the rename, the directory after it. tmp is
// the caller's temp name, which may be ignored by name (the state repo's
// .gitignore holds secrets.age.tmp).
func WriteAtomic(tmp, path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if err := finish(f, data); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(os.Open(filepath.Dir(path)))
}

// WriteAtomicIn is WriteAtomic inside a root (the Operator's jail, #35):
// tmp and rel are relative to it and never leave it.
func WriteAtomicIn(root *os.Root, tmp, rel string, data []byte, perm os.FileMode) error {
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if err := finish(f, data); err != nil {
		root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, rel); err != nil {
		root.Remove(tmp)
		return err
	}
	return syncDir(root.Open(filepath.Dir(rel)))
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
