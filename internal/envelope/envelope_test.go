package envelope

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
)

func TestJailKeepsReadsInsideTheServiceData(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "stacks")
	os.MkdirAll(filepath.Join(data, "media", "config"), 0o755)
	os.MkdirAll(filepath.Join(data, "immich"), 0o755)
	os.MkdirAll(filepath.Join(data, "beets"), 0o755)
	os.WriteFile(filepath.Join(data, "immich", "secret.env"), []byte("x"), 0o644)
	os.Symlink(filepath.Join(data, "immich", "secret.env"), filepath.Join(data, "media", "config", "link"))
	st := &state.State{Box: state.Box{DataDir: data}}
	svc := &state.Service{Name: "media", Contract: state.Contract{Data: []state.DataPath{{Path: "${DATA_DIR}/media/config"}}}}
	// read inside the jail, through the root
	read := func(s *state.Service, p string) error {
		r, rel, err := jail(st, s, p)
		if err != nil {
			return err
		}
		defer r.Close()
		_, err = r.ReadFile(rel)
		return err
	}
	os.WriteFile(filepath.Join(data, "media", "config", "a.yml"), []byte("ok"), 0o644)
	if err := read(svc, filepath.Join(data, "media", "config", "a.yml")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		filepath.Join(data, "immich", "secret.env"),
		filepath.Join(data, "media", "config", "..", "..", "immich", "secret.env"),
		filepath.Join(data, "media", "config", "link"), // a symlink out: the root refuses to follow it
		"/etc/passwd",
	} {
		if err := read(svc, bad); err == nil {
			t.Fatalf("%s allowed", bad)
		}
	}
	// no data declared: the service's own folder under DATA_DIR
	bare := &state.Service{Name: "beets"}
	os.WriteFile(filepath.Join(data, "beets", "config.yaml"), []byte("ok"), 0o644)
	if err := read(bare, filepath.Join(data, "beets", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := read(bare, filepath.Join(data, "media", "x")); err == nil {
		t.Fatal("other service's folder allowed")
	}
}

// The budget counts what ran, not what it refused: 2026-10-29 eight
// refusals in a row kept paperless locked for an hour longer than the eight
// actions did. A human's apply never counts, the Operator's own do.
func TestSpentCountsActionsNotRefusals(t *testing.T) {
	recent := []journal.Entry{
		{Service: "paperless", Verb: "exec", Result: "info"},
		{Service: "paperless", Verb: "exec", Result: "red", Detail: "exit status 1"},
		{Service: "paperless", Verb: "exec", Result: "red", Detail: refused + "8/h). Ein Mensch muss ran: exec"},
		{Service: "paperless", Verb: "restart", Result: "red", Detail: refused + "8/h). Ein Mensch muss ran: restart"},
		{Service: "paperless", Verb: "apply", Actor: "ops"},
		{Service: "paperless", Verb: "apply", Actor: "human:mdopp"},
		{Service: "paperless", Verb: "snapshot", Result: "info"},
		{Service: "scan", Verb: "exec", Result: "info"},
	}
	used, snapped := spent(recent, "paperless", "ops")
	if used != 3 || !snapped {
		t.Fatalf("used %d snapped %v, want 3 true", used, snapped)
	}
	if used, _ := spent(recent, "scan", "ops"); used != 1 {
		t.Fatalf("scan used %d", used)
	}
}
