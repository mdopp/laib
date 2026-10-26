package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// inboxMux is the Inbox's routes as laib serve has them (behind the guard there).
func inboxMux(env *cliEnv) func(method, path string, form url.Values) string {
	mux := http.NewServeMux()
	mux.HandleFunc("/inbox", pages.Inbox(env))
	mux.HandleFunc("/inbox/alle", pages.ApproveAll(env))
	mux.HandleFunc("POST /inbox/{id}/{decision}", pages.Decision(env))
	return func(method, path string, form url.Values) string {
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Body.String()
	}
}

// until polls cond for up to 10 s.
func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; !cond(); i++ {
		if i == 500 {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func settled(env *cliEnv, ids ...int) func() bool {
	return func() bool {
		for _, id := range ids {
			if p, _ := proposal.Get(cli.InboxDir(env), id); p.Status != "approved" || p.Outcome == "waiting" || p.Step != "" || pages.DecisionState(id) != "" {
				return false
			}
		}
		return true
	}
}

// #686: after Freigeben the card shows at once what the box does now — no
// decision buttons while it waits or runs, the steps in the owner's words,
// and then „fertig“; the page reloads itself only while something runs.
func TestAfterFreigebenTheCardSaysWhatTheBoxDoesUntilDone(t *testing.T) {
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	p := proposeService(t, env, "w1")
	var mu sync.Mutex
	var steps []string
	run := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && slices.Contains(args, "w1.service") {
			got, _ := proposal.Get(cli.InboxDir(env), p.ID)
			mu.Lock()
			steps = append(steps, got.Step)
			mu.Unlock()
		}
		return run(ctx, name, args...)
	}
	do := inboxMux(env)
	unlock, _ := apply.TakeLock(env, st, "apply") // the work before it
	do("POST", fmt.Sprintf("/inbox/%d/approve", p.ID), nil)
	until(t, "approved", func() bool { got, _ := proposal.Get(cli.InboxDir(env), p.ID); return got.Status == "approved" })
	// the carry writes its first step just after the approval: wait for it, not for a fixed moment
	var body string
	until(t, "waiting shown", func() bool { body = do("GET", "/inbox", nil); return strings.Contains(body, "wartet, bis") })
	for _, w := range []string{"wartet, bis die Box mit der Arbeit davor fertig ist", "http-equiv=refresh"} {
		if !strings.Contains(body, w) {
			t.Errorf("while it waits the Inbox lacks %q:\n%s", w, body)
		}
	}
	if strings.Contains(body, "/inbox/1/approve") || strings.Contains(body, "/inbox/1/reject") || strings.Contains(body, "/inbox/1/rollback") {
		t.Errorf("buttons while it waits:\n%s", body)
	}
	unlock()
	until(t, "carried", settled(env, p.ID))
	mu.Lock()
	if !slices.Contains(steps, "startet w1") || !slices.ContainsFunc(steps, func(s string) bool { return strings.HasPrefix(s, "prüft, ob w1") }) {
		t.Errorf("the run did not say its steps on the proposal: %q", steps)
	}
	mu.Unlock()
	body = do("GET", "/inbox", nil)
	if !strings.Contains(body, "fertig") || strings.Contains(body, "http-equiv=refresh") || strings.Contains(body, "/inbox/1/approve") {
		t.Errorf("done is not shown as done:\n%s", body)
	}
	for _, w := range []string{"..", "Lauf"} {
		if strings.Contains(body, w) {
			t.Errorf("technical words on the page (%q):\n%s", w, body)
		}
	}
}

// #686: a run that goes red says on each card that it was taken back and
// why, in words, without the run's commit range.
func TestARedRunSaysZurückgenommenWeil(t *testing.T) {
	env, _ := testBox(t)
	p := proposeService(t, env, "w1")
	run := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && slices.Contains(args, "is-active") && slices.Contains(args, "w1.service") {
			return "failed", nil
		}
		return run(ctx, name, args...)
	}
	do := inboxMux(env)
	do("POST", fmt.Sprintf("/inbox/%d/approve", p.ID), nil)
	until(t, "rolled back", settled(env, p.ID))
	got, _ := proposal.Get(cli.InboxDir(env), p.ID)
	body := do("GET", "/inbox", nil)
	if got.Outcome != "rolled-back" || !strings.Contains(body, "zurückgenommen, weil w1 danach nicht richtig lief") {
		t.Errorf("no reason for the rollback: %+v\n%s", got, body)
	}
	if strings.Contains(body, "/inbox/1/") || strings.Contains(body, "http-equiv=refresh") {
		t.Errorf("a decided card keeps buttons or reloads:\n%s", body)
	}
}

// #686: „Alle freigeben“ is one confirmation for every open card, then one
// list: each is approved and applied in turn, each with its own outcome —
// a red one is taken back alone, the others stay.
func TestAlleFreigebenConfirmsOnceAndListsEachOutcome(t *testing.T) {
	env, _ := testBox(t)
	ps := []*proposal.Proposal{proposeService(t, env, "w1"), proposeService(t, env, "w2"), proposeService(t, env, "w3")}
	run := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && slices.Contains(args, "is-active") && slices.Contains(args, "w2.service") {
			return "failed", nil
		}
		return run(ctx, name, args...)
	}
	do := inboxMux(env)
	if body := do("GET", "/inbox", nil); !strings.Contains(body, `href="/inbox/alle"`) || !strings.Contains(body, "Alle freigeben (3)") {
		t.Fatalf("no Alle freigeben for three open cards:\n%s", body)
	}
	confirm := do("GET", "/inbox/alle", nil)
	if strings.Count(confirm, "<button") != 1 || !strings.Contains(confirm, "Ja, alle 3 freigeben") || strings.Count(confirm, "name=id") != 3 {
		t.Fatalf("the confirmation is not one button for three:\n%s", confirm)
	}
	if got, _ := proposal.Get(cli.InboxDir(env), 1); got.Status != "open" {
		t.Fatal("showing the confirmation approved something")
	}
	body := do("POST", "/inbox/alle", url.Values{"id": {"1", "2", "3", "99"}})
	if strings.Contains(body, "fehlgeschlagen") {
		t.Fatalf("Alle freigeben failed: %s", body)
	}
	until(t, "all three settled", settled(env, 1, 2, 3))
	batches := map[string]bool{}
	for _, p := range ps {
		got, _ := proposal.Get(cli.InboxDir(env), p.ID)
		batches[got.Batch] = true
		if want := map[int]string{1: "", 2: "rolled-back", 3: ""}[p.ID]; got.Outcome != want {
			t.Errorf("#%d: outcome %q, want %q", p.ID, got.Outcome, want)
		}
	}
	if len(batches) != 3 {
		t.Errorf("not one run each: %v", batches)
	}
	body = do("GET", "/inbox", nil)
	if strings.Count(body, ">fertig<") != 2 || !strings.Contains(body, "zurückgenommen, weil w2 danach nicht richtig lief") || strings.Contains(body, "Alle freigeben") {
		t.Errorf("the list does not show each outcome:\n%s", body)
	}
}
