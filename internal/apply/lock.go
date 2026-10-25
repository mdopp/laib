package apply

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
)

// ErrLocked means another apply holds the lock (state `eingeschränkt`).
var ErrLocked = errors.New("apply is locked")

// Lock takes the one apply lock (#39): a flock on the lock file, so a
// process that dies releases it, plus holder, pid and start in the file for
// /ready. Everything that mutates the box holds it for its whole duration:
// apply, converge, restore, remove, approve, rollback. stale names the
// holder whose process was gone when the lock was taken over.
func Lock(paths boxfs.Paths, holder string) (unlock func(), stale string, err error) {
	if err := os.MkdirAll(filepath.Dir(paths.Lock), 0o755); err != nil {
		return nil, "", err
	}
	f, err := os.OpenFile(paths.Lock, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, "", err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, "", ErrLocked
		}
		return nil, "", err
	}
	if b, _ := os.ReadFile(paths.Lock); len(strings.TrimSpace(string(b))) > 0 {
		stale = strings.TrimSpace(string(b))
	}
	f.Truncate(0)
	f.Seek(0, 0)
	fmt.Fprintf(f, "%s pid %d since %s", holder, os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	f.Sync()
	return func() {
		os.Remove(paths.Lock)
		f.Close() // releases the flock
	}, stale, nil
}

// LockHolder returns who holds the lock, or "" when nobody does — a file
// whose flock is free is a leftover, not a lock.
func LockHolder(paths boxfs.Paths) string {
	f, err := os.Open(paths.Lock)
	if err != nil {
		return ""
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return ""
	}
	b, _ := os.ReadFile(paths.Lock)
	return strings.TrimSpace(string(b))
}

// --- pending activation (#39): files are written before daemon-reload and
// restart; an apply killed in between leaves the old config running and a
// plan that looks clean. The marker names the services whose activation is
// owed until verify ran; apply and converge finish it.

type pending struct {
	Services []string  `json:"services"`
	Reason   string    `json:"reason"`
	Since    time.Time `json:"since"`
}

func pendingPath(paths boxfs.Paths) string { return filepath.Join(paths.Home, "apply.pending") }

func WritePending(paths boxfs.Paths, services []string, reason string) error {
	b, _ := json.Marshal(pending{Services: services, Reason: reason, Since: time.Now().UTC()})
	return os.WriteFile(pendingPath(paths), b, 0o644)
}

func Pending(paths boxfs.Paths) []string {
	b, err := os.ReadFile(pendingPath(paths))
	if err != nil {
		return nil
	}
	var p pending
	json.Unmarshal(b, &p)
	return p.Services
}

func ClearPending(paths boxfs.Paths) { os.Remove(pendingPath(paths)) }

// --- pause (#39): a service a human stopped on purpose, one a restore is
// rewriting, one the GPU lease holds down. Converge leaves it alone, the
// readiness shows "pausiert von … bis …".

type Pause struct {
	Service string    `json:"service"`
	By      string    `json:"by"`
	Until   time.Time `json:"until"`
	Reason  string    `json:"reason,omitempty"`
	// Stop and Restart are the units the pause stopped or restarted (the
	// GPU lease, ADR 0027); resume starts and restarts them again
	Stop    []string `json:"stop,omitempty"`
	Restart []string `json:"restart,omitempty"`
}

func pauseDir(paths boxfs.Paths) string { return filepath.Join(paths.Home, "paused") }

func WritePause(paths boxfs.Paths, p Pause) error {
	if err := os.MkdirAll(pauseDir(paths), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(p)
	return os.WriteFile(filepath.Join(pauseDir(paths), p.Service+".json"), b, 0o644)
}

func Resume(paths boxfs.Paths, service string) {
	os.Remove(filepath.Join(pauseDir(paths), service+".json"))
}

// Paused lists the pauses in force; an expired one is removed on the way.
func Paused(paths boxfs.Paths) map[string]Pause {
	out := map[string]Pause{}
	entries, _ := os.ReadDir(pauseDir(paths))
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(pauseDir(paths), e.Name()))
		if err != nil {
			continue
		}
		var p Pause
		if json.Unmarshal(b, &p) != nil || p.Service == "" {
			continue
		}
		if !p.Until.IsZero() && time.Now().After(p.Until) {
			os.Remove(filepath.Join(pauseDir(paths), e.Name()))
			continue
		}
		out[p.Service] = p
	}
	return out
}
