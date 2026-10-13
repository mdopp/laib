package boxfs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// #148: a temp file renamed over the target without an fsync can surface
// as a zero-length file after a power cut (XFS, the data disk), the same
// shape #107 found in the shell. Every durable write goes through
// WriteAtomic, so a rename anywhere else is a write that skipped the sync.
func TestEveryRenameIsADurableWrite(t *testing.T) {
	allowed := map[string]string{
		"internal/boxfs/atomic.go": "the helper itself",
		"internal/stick/stream.go": "the ISO download cache on the admin's computer: re-downloaded and hash-checked, not box state",
	}
	rename := regexp.MustCompile(`\bRename\(`)
	var bad []string
	for _, top := range []string{"cmd", "internal"} {
		filepath.WalkDir(filepath.Join("..", "..", top), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			rel, _ := filepath.Rel(filepath.Join("..", ".."), p)
			if _, ok := allowed[filepath.ToSlash(rel)]; ok {
				return nil
			}
			b, _ := os.ReadFile(p)
			for i, line := range strings.Split(string(b), "\n") {
				if rename.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "//") {
					bad = append(bad, filepath.ToSlash(rel)+":"+strconv.Itoa(i+1))
				}
			}
			return nil
		})
	}
	if len(bad) > 0 {
		t.Fatalf("temp+rename without fsync (use boxfs.WriteAtomic): %s", strings.Join(bad, ", "))
	}
}

func TestWriteAtomicSyncsTheFileThenTheDirectory(t *testing.T) {
	dir := t.TempDir()
	var synced []string
	real := syncFile
	t.Cleanup(func() { syncFile = real })
	syncFile = func(f *os.File) error { synced = append(synced, f.Name()); return real(f) }
	path := filepath.Join(dir, "secrets.age")
	if err := WriteAtomic(path+".tmp", path, []byte("neu"), 0o600); err != nil {
		t.Fatal(err)
	}
	if want := []string{path + ".tmp", dir}; strings.Join(synced, " ") != strings.Join(want, " ") {
		t.Fatalf("synced %v, want the temp file before the rename and the directory after: %v", synced, want)
	}
	if b, _ := os.ReadFile(path); string(b) != "neu" {
		t.Fatalf("content %q", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("the temp file is left behind")
	}
}

func TestWriteAtomicInStaysInsideTheRoot(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := WriteAtomicIn(root, "../x.laib-tmp", "x", []byte("raus"), 0o644); err == nil {
		t.Fatal("a temp name outside the root was written")
	}
	if err := os.MkdirAll(filepath.Join(dir, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomicIn(root, "conf/a.laib-tmp", "conf/a", []byte("drin"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "conf", "a")); string(b) != "drin" {
		t.Fatalf("content %q", b)
	}
}
