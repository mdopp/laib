package apply

import (
	"os"
	"path/filepath"
	"strings"
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
	unlock, _, err := Lock(paths, "human:core")
	if err != nil {
		t.Fatalf("stale lock not taken over: %v", err)
	}
	defer unlock()
	if _, _, err := Lock(paths, "ops"); err != ErrLocked {
		t.Fatalf("a live lock must still refuse: %v", err)
	}
	if h := LockHolder(paths); h == "" || h == "human:core pid 999999999" {
		t.Fatalf("holder: %q", h)
	}
}

// The lock is the kernel's (flock): a process that dies releases it, and
// the content only names the holder. Lock reports what it took over.
func TestLockIsAFlockAndReportsATakeover(t *testing.T) {
	paths := boxfs.Paths{Lock: filepath.Join(t.TempDir(), "apply.lock")}
	os.WriteFile(paths.Lock, []byte("converge:laib pid 999999999 since x"), 0o644)
	unlock, stale, err := Lock(paths, "apply:human")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stale, "converge:laib") {
		t.Fatalf("stale holder not reported: %q", stale)
	}
	if h := LockHolder(paths); !strings.HasPrefix(h, "apply:human pid ") {
		t.Fatalf("holder while held: %q", h)
	}
	if _, _, err := Lock(paths, "other"); err != ErrLocked {
		t.Fatalf("a second lock while held: %v", err)
	}
	unlock()
	if LockHolder(paths) != "" {
		t.Fatal("released")
	}
}

// Files are written before daemon-reload and restart: an apply killed in
// between leaves the old config running. The pending marker names the
// services whose activation is owed, until verify ran.
func TestRunRecordsPendingActivationUntilVerified(t *testing.T) {
	paths := boxfs.Paths{Home: t.TempDir()}
	if err := WritePending(paths, []string{"a", "b"}, "x"); err != nil {
		t.Fatal(err)
	}
	if got := Pending(paths); len(got) != 2 || got[0] != "a" {
		t.Fatalf("%v", got)
	}
	ClearPending(paths)
	if len(Pending(paths)) != 0 {
		t.Fatal("not cleared")
	}
}
