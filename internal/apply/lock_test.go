package apply

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
)

// A kill mid-apply (a power cut during the first boot, #37) left the lock
// file behind for ever: every later apply was refused and /ready said
// eingeschränkt. A lock whose process is gone is stale and taken over.
func TestLockTakesOverAStaleLock(t *testing.T) {
	paths := boxfs.Paths{Lock: filepath.Join(t.TempDir(), "apply.lock")}
	os.WriteFile(paths.Lock, []byte("human:core pid 999999999"), 0o644)
	if LockHolder(paths) != "" {
		t.Fatal("a stale lock must not restrict readiness")
	}
	unlock, err := Lock(paths, "human:core")
	if err != nil {
		t.Fatalf("stale lock not taken over: %v", err)
	}
	defer unlock()
	if _, err := Lock(paths, "ops"); err != ErrLocked {
		t.Fatalf("a live lock must still refuse: %v", err)
	}
	if h := LockHolder(paths); h == "" || h == "human:core pid 999999999" {
		t.Fatalf("holder: %q", h)
	}
}
