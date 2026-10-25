package lease

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeLaib is laib as the lease reaches it over MCP (ADR 0027): the
// services laib status lists, and the pause, resume and note verbs. It
// keeps the pauses as laib's markers do: a renewal keeps the units.
type fakeLaib struct {
	mu       sync.Mutex
	services []Service
	calls    []string
	paused   map[string]Hold
	notes    []string
	down     error
	onPause  func(Hold)
}

func newFake(services []Service) *fakeLaib {
	return &fakeLaib{services: services, paused: map[string]Hold{}}
}

func (f *fakeLaib) Services(context.Context) ([]Service, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.services), f.down
}

func (f *fakeLaib) Pause(_ context.Context, h Hold) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down != nil {
		return f.down
	}
	if f.onPause != nil {
		f.onPause(h)
	}
	line := "pause " + h.Service
	if len(h.Stop) > 0 {
		line += " stop " + strings.Join(h.Stop, ",")
	}
	if len(h.Restart) > 0 {
		line += " restart " + strings.Join(h.Restart, ",")
	}
	f.calls = append(f.calls, line)
	if old, ok := f.paused[h.Service]; ok && len(h.Stop)+len(h.Restart) == 0 {
		h.Stop, h.Restart = old.Stop, old.Restart
	}
	f.paused[h.Service] = h
	return nil
}

func (f *fakeLaib) Resume(_ context.Context, service string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down != nil {
		return f.down
	}
	f.calls = append(f.calls, "resume "+service)
	delete(f.paused, service)
	return nil
}

func (f *fakeLaib) Note(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notes = append(f.notes, text)
	return f.down
}

// ADR 0027: the lease depends on laib serve for pausing. While laib does
// not answer, the proxy still serves the household model, the status file
// says the lease cannot pause anything, and a window that would stop
// units is refused with that reason — the card is never taken from
// services that keep running. Once laib answers, the same request works.
func TestWithoutLaibTheLeaseServesTheHouseholdAndSaysItCannotPause(t *testing.T) {
	cfg := config42(t, router42(t).URL)
	laib := cfg.Laib.(*fakeLaib)
	laib.down = errors.New("connection refused")
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("the lease must start while laib is down: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(cfg.LeaseDir, "laib-status.json"))
	if !strings.Contains(string(b), "Laib antwortet nicht") {
		t.Fatalf("the status file does not say the lease cannot pause: %s", b)
	}
	if _, err := s.Acquire(context.Background(), "bote", "full", time.Hour); err == nil || !strings.Contains(err.Error(), "Laib") {
		t.Fatalf("full without laib: %v", err)
	}
	if l := s.read(); l != nil {
		t.Fatalf("a refused window stands: %+v", l)
	}
	if allowed, mode := s.Policy(); mode != "haushalt" || !slices.Equal(allowed, []string{"gemma-4-e4b"}) {
		t.Fatalf("the household is not served: %v %s", allowed, mode)
	}
	// a window that stops nothing needs no laib
	if _, err := s.Acquire(context.Background(), "chronicle", "shared", time.Hour); err != nil {
		t.Fatalf("shared stops nothing and needs no laib: %v", err)
	}
	s.Release(context.Background(), "chronicle")
	laib.mu.Lock()
	laib.down = nil
	laib.mu.Unlock()
	if _, err := s.Acquire(context.Background(), "bote", "full", time.Hour); err != nil {
		t.Fatalf("laib answers again, full must work: %v", err)
	}
	if !slices.Equal(slices.Sorted(maps.Keys(laib.paused)), []string{"llama", "stimme"}) {
		t.Fatalf("full did not pause through laib: %v", laib.calls)
	}
	b, _ = os.ReadFile(filepath.Join(cfg.LeaseDir, "laib-status.json"))
	var st struct{ Findings []struct{ Detail string } }
	json.Unmarshal(b, &st)
	for _, f := range st.Findings {
		if strings.Contains(f.Detail, "Laib antwortet nicht") {
			t.Fatalf("the finding stayed after laib answered: %s", b)
		}
	}
}
