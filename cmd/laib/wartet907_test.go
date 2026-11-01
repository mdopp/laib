package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// whoamiUnit is diag's unit finding for whoami.
func whoamiUnit(t *testing.T, env *cliEnv) diag.Finding {
	t.Helper()
	return whoamiFinding(t, env, "unit")
}

// whoamiFinding is diag's finding of that probe for whoami.
func whoamiFinding(t *testing.T, env *cliEnv, probe string) diag.Finding {
	t.Helper()
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range diag.Probes(context.Background(), env, st, "whoami") {
		if f.Probe == probe && f.Service == "whoami" {
			return f
		}
	}
	t.Fatalf("no %s finding for whoami", probe)
	return diag.Finding{}
}

// sickWhoami is a health endpoint that answers 503: whoami runs, but its
// probe is red.
func sickWhoami(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

// #907, 2026-10-29: the owner approved paperless' template update; the
// apply stopped paperless to rewrite it, and the look in between saw its
// health red and took it for an incident — a pre-repair snapshot, a second
// restart and a „Vorfall“ inside the apply's 58 seconds. While an apply
// activates a service, its red is expected: diag says so, offers no
// repair, and red it is again once the apply let go. #978: the apply
// announces it with its marker (ApplyPlan); an apply that dies leaves the
// marker, which past its until is the finding itself
// (TestAMarkerNotClearedByItsUntilIsTheFinding).
func TestAServiceAnApplyActivatesIsNotRedUntilTheApplyLetsGo(t *testing.T) {
	env, _ := testBox(t)
	failedWhoami(t)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	done := apply.Mark(st, env.Paths, "apply", "human:mdopp", "#261", []string{"whoami"}, 15*time.Minute)
	f := whoamiUnit(t, env)
	if f.Severity == "rot" || f.Repair != nil || !strings.Contains(f.Detail, "Anwenden") {
		t.Fatalf("a red the apply causes is red to the look, or offers a repair: %+v", f)
	}
	done(false)
	if f := whoamiUnit(t, env); f.Severity != "rot" || !slices.Equal(f.Repair, []string{"restart", "whoami"}) {
		t.Fatalf("a red that outlives the apply is not red again: %+v", f)
	}
}

// A service a hand stopped (laib stop) is paused: its stopped units are
// what the hand asked for, not an incident for the Operator to restart.
func TestAPausedServiceIsNotRed(t *testing.T) {
	env, _ := testBox(t)
	failedWhoami(t)
	if err := apply.WritePause(env.Paths, apply.Pause{Service: "whoami", By: "claude", Until: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if f := whoamiUnit(t, env); f.Severity == "rot" || f.Repair != nil || !strings.Contains(f.Detail, "pausiert") {
		t.Fatalf("a paused service's stopped unit is red: %+v", f)
	}
}

// busyWhoami gives whoami a status file with a busy field, as scan's
// laib-status.json says `zustand: scannt` mid-stack, written at `at`, and
// a health probe at url.
func busyWhoami(t *testing.T, env *cliEnv, data, url, zustand string, at time.Time) {
	t.Helper()
	yml := "name: whoami\nsecrets:\n  - name: WHOAMI_TOKEN\nhealth: {http: " + url + "}\nstatus:\n  file: ${DATA_DIR}/whoami/laib-status.json\n  busy: {field: zustand, values: [scannt, trennt, legt-ab], grace: 30m}\n"
	if err := os.WriteFile(filepath.Join(env.Paths.State, "services", "whoami", "laib.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(data, "whoami"), 0o755)
	body := fmt.Sprintf(`{"at": %q, "detail": "Stapel läuft", "zustand": %q}`, at.UTC().Format(time.RFC3339), zustand)
	if err := os.WriteFile(filepath.Join(data, "whoami", "laib-status.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// #908: a restart is not a free repair. Scan holds a stack across pages;
// a restart mid-stack lost the owner's stack. While it runs and its status
// file says it is busy, a red waits — up to the template's grace from what
// the file last said, then it is red and repaired as before. A service
// whose unit is down works on nothing: its red does not wait.
func TestABusyServiceWaitsUpToItsGraceThenIsRed(t *testing.T) {
	env, data := testBox(t)
	url := sickWhoami(t)
	busyWhoami(t, env, data, url, "scannt", time.Now().Add(-5*time.Minute))
	f := whoamiFinding(t, env, "health")
	if f.Severity == "rot" || f.Repair != nil || !strings.Contains(f.Detail, "scannt") {
		t.Fatalf("a busy service's red is an incident: %+v", f)
	}
	busyWhoami(t, env, data, url, "scannt", time.Now().Add(-31*time.Minute))
	if f := whoamiFinding(t, env, "health"); f.Severity != "rot" || !slices.Equal(f.Repair, []string{"restart", "whoami"}) {
		t.Fatalf("busy longer than its grace still waits: %+v", f)
	}
	busyWhoami(t, env, data, url, "bereit", time.Now())
	if f := whoamiFinding(t, env, "health"); f.Severity != "rot" {
		t.Fatalf("a service that is not busy waits: %+v", f)
	}
	failedWhoami(t)
	busyWhoami(t, env, data, url, "scannt", time.Now())
	if f := whoamiUnit(t, env); f.Severity != "rot" {
		t.Fatalf("a service whose unit is down waits for its busy word: %+v", f)
	}
}

// The envelope holds the same line for every hand that is not a person:
// the Operator's (or an agent's) restart of a busy service waits and says
// so in the journal — no snapshot, no restart, no budget spent; a person
// restarts at once.
func TestAHandsRestartOfABusyServiceWaits(t *testing.T) {
	ctx := context.Background()
	env, data := testBox(t)
	var mu sync.Mutex
	var log []string // whoami runs: every systemctl call, recorded
	prev := sysd.Run
	t.Cleanup(func() { sysd.Run = prev })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" {
			mu.Lock()
			log = append(log, strings.Join(args, " "))
			mu.Unlock()
		}
		return prev(ctx, name, args...)
	}
	calls := func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(log) }
	well := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(well.Close)
	busyWhoami(t, env, data, well.URL, "scannt", time.Now().Add(-time.Minute))
	st, _ := cli.LoadState(env)
	out, code := mcp.Call(ctx, env, st, "ops", "laib_restart", map[string]any{"service": "whoami"})
	if code == cli.ExitOK || restartedWhoami(calls()) {
		t.Fatalf("the Operator restarted a busy service: exit %d: %s", code, out)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if !slices.ContainsFunc(es, func(e journal.Entry) bool {
		return e.Verb == "restart" && e.Actor == "ops" && e.Result == "info" && strings.Contains(e.Detail, "wartet") && strings.Contains(e.Detail, "scannt")
	}) {
		t.Fatalf("the wait is not journaled: %+v", es)
	}
	if slices.ContainsFunc(es, func(e journal.Entry) bool { return e.Verb == "snapshot" }) {
		t.Fatalf("a restart that waits snapshotted: %+v", es)
	}
	human := &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}
	envelope.Restart(ctx, human, []string{"whoami"})
	if !restartedWhoami(calls()) {
		t.Fatal("a person's restart of a busy service waited")
	}
}
