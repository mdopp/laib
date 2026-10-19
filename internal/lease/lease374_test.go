package lease

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #374: on the Kraftwerk Stimme is not on Laib yet, so nobody provides the
// role voice, and laib-lease refused to start at all ("mode full: no
// service provides voice") and restart-looped: one unprovided role took the
// whole proxy and lease down. It starts, leaves out what nobody provides,
// and says so in its status file (gelb, which mode, which role), which
// laib status and diag read.
func TestAnUnprovidedRoleDegradesInsteadOfStoppingTheLease(t *testing.T) {
	var calls []string
	Systemctl = func(_ context.Context, verb string, units []string) error {
		calls = append(calls, verb+" "+strings.Join(units, ","))
		return nil
	}
	cfg := config42(t, router42(t).URL)
	real := cfg.Units
	cfg.Units = func(role string) (string, []string) { // the llama template only, no Stimme
		if role == "voice" {
			return "", nil
		}
		return real(role)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("the lease must start without a voice provider: %v", err)
	}
	if _, err := s.Acquire(context.Background(), "bote", "full", time.Hour); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "stop llama-embed.service") || strings.Contains(joined, "stimme") {
		t.Fatalf("full stops what is provided and nothing else: %v", calls)
	}
	var st struct {
		Findings []struct{ Severity, Detail string }
	}
	b, err := os.ReadFile(filepath.Join(cfg.LeaseDir, "laib-status.json"))
	if err != nil || json.Unmarshal(b, &st) != nil {
		t.Fatalf("no status file: %v %s", err, b)
	}
	var gelb []string
	for _, f := range st.Findings {
		if f.Severity == "gelb" {
			gelb = append(gelb, f.Detail)
		}
	}
	all := strings.Join(gelb, "\n")
	for _, want := range []string{"full", "voice/whisper", "voice/wakeword-trainer", "voice/piper"} {
		if !strings.Contains(all, want) {
			t.Errorf("the status does not name %s: %q", want, gelb)
		}
	}
	// a container the provider does not have is still a stale presets.json: refused at start
	cfg.Modes["full"] = Mode{Stops: []string{"model/embedder"}}
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "embedder") {
		t.Fatalf("a unit the provider does not have must be refused at start: %v", err)
	}
	// with every role provided the status has no finding
	cfg = config42(t, router42(t).URL)
	if _, err := New(cfg); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(cfg.LeaseDir, "laib-status.json"))
	if json.Unmarshal(b, &st) != nil || len(st.Findings) != 0 {
		t.Fatalf("every role provided, yet findings: %s", b)
	}
}

// The llama contract points laib at the file the lease writes.
func TestTheLlamaContractReadsTheLeaseStatus(t *testing.T) {
	b, _ := os.ReadFile("../../templates/llama/laib.yml")
	if !strings.Contains(string(b), "file: ${DATA_DIR}/llama/lease/laib-status.json") {
		t.Fatal("templates/llama/laib.yml has no status file at the lease dir")
	}
}
