package diag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

func statusBox(t *testing.T, check string) (*state.State, *state.Service, string) {
	t.Helper()
	dir := t.TempDir()
	svc := &state.Service{Name: "ha", Contract: state.Contract{Status: &state.StatusFile{File: "${DATA_DIR}/ha/s.json", Check: check}}}
	st := &state.State{Box: state.Box{DataDir: dir}, Services: map[string]*state.Service{"ha": svc}}
	os.MkdirAll(filepath.Join(dir, "ha"), 0o755)
	return st, svc, filepath.Join(dir, "ha", "s.json")
}

// #309: laib reads what a template says about itself fresh, with its age;
// the kernel names no template.
func TestAStatusFileIsReadFreshWithItsAge(t *testing.T) {
	st, svc, p := statusBox(t, "")
	if r := ReadStatus(st, svc); r.Found {
		t.Fatalf("no file, no report: %+v", r)
	}
	at := time.Now().Add(-12 * time.Minute).UTC().Format(time.RFC3339)
	os.WriteFile(p, []byte(`{"at":"`+at+`","detail":"fehlt: CalDAV","findings":[{"severity":"gelb","detail":"CalDAV: fehlt","fix":"einrichten"},{"severity":"bunt","detail":"x"}]}`), 0o644)
	r := ReadStatus(st, svc)
	if !r.Found || r.Detail != "fehlt: CalDAV" || len(r.Findings) != 2 {
		t.Fatalf("report %+v", r)
	}
	if f := r.Findings[0]; f.Severity != "gelb" || f.Service != "ha" || f.Probe != "status" || f.Fix != "einrichten" {
		t.Errorf("finding %+v", f)
	}
	if r.Findings[1].Severity != "info" {
		t.Errorf("an unknown severity is info, never red: %+v", r.Findings[1])
	}
	if a := Age(r.At, time.Now()); a != "geprüft vor 12 min" {
		t.Errorf("age %q", a)
	}
	// written again, read again: no cache
	os.WriteFile(p, []byte(`{"detail":"alles da"}`), 0o644)
	if r := ReadStatus(st, svc); r.Detail != "alles da" || time.Since(r.At) > time.Minute {
		t.Errorf("a file without at takes its mtime: %+v", r)
	}
}

// #309: a stale status with a check is refreshed in the background
// (systemctl --no-block); a check already running is left alone; a fresh
// one, or a template without a check, starts nothing.
func TestAStaleStatusStartsTheTemplatesCheck(t *testing.T) {
	st, svc, p := statusBox(t, "ha-check.service")
	var calls []string
	active := "inactive"
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if len(args) > 1 && args[1] == "is-active" {
			return active, nil
		}
		return "", nil
	}
	now := time.Now()
	if !Stale(svc, ReadStatus(st, svc), now) {
		t.Fatal("no file yet is stale")
	}
	if !Refresh(context.Background(), svc) || !strings.Contains(strings.Join(calls, "\n"), "systemctl --user start --no-block ha-check.service") {
		t.Fatalf("the check must start without waiting: %v", calls)
	}
	calls, active = nil, "activating"
	if !Refresh(context.Background(), svc) || strings.Contains(strings.Join(calls, "\n"), "start") {
		t.Fatalf("a running check is not started again: %v", calls)
	}
	os.WriteFile(p, []byte(`{"at":"`+now.UTC().Format(time.RFC3339)+`","detail":"ok"}`), 0o644)
	if Stale(svc, ReadStatus(st, svc), now.Add(4*time.Minute)) || !Stale(svc, ReadStatus(st, svc), now.Add(6*time.Minute)) {
		t.Error("stale after five minutes by default")
	}
	_, plain, _ := statusBox(t, "")
	if Stale(plain, Report{}, now) || Refresh(context.Background(), plain) {
		t.Error("a status without a check is never refreshed by laib: its service writes it")
	}
}

// #688: a status file's further fields are its own, passed on by name;
// what does not fit the cap is named, never silently missing.
func TestAStatusFilesOwnFieldsArePassedOnCapped(t *testing.T) {
	st, svc, p := statusBox(t, "")
	os.WriteFile(p, []byte(`{"at":"2026-10-26T10:00:00Z","detail":"d","findings":[],"wartet":1,"letzte":[{"eingang":"a.zip","von":"mama","titel":12,"alben":2}]}`), 0o644)
	r := ReadStatus(st, svc)
	if len(r.Eigenes) != 2 || string(r.Eigenes["wartet"]) != "1" || !strings.Contains(string(r.Eigenes["letzte"]), `"von":"mama"`) || len(r.Gekappt) != 0 {
		t.Fatalf("own fields without at, detail, findings: %v %v", r.Eigenes, r.Gekappt)
	}
	os.WriteFile(p, []byte(`{"detail":"d","wartet":0,"riesig":"`+strings.Repeat("x", EigenesMax)+`"}`), 0o644)
	if r := ReadStatus(st, svc); string(r.Eigenes["wartet"]) != "0" || r.Eigenes["riesig"] != nil || len(r.Gekappt) != 1 || r.Gekappt[0] != "riesig" {
		t.Fatalf("a field over the cap is left out and named: %v %v", r.Eigenes, r.Gekappt)
	}
}
