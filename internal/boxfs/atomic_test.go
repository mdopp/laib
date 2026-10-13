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
