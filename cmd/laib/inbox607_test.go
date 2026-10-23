package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/proposal"
)

// #607: the owner approved four proposals in a row; every tap while an
// apply held the lock failed with "gesperrt durch approve:…", and an apply
// ran in the request's context, so a closed connection could cut it off.
// A decision is taken at once and carried out after the one before it, on
// its own, and the Inbox says which one waits and which one runs.
func TestDecisionsQueueBehindTheLockAndOutliveTheRequest(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	pages.DecisionRetry = 20 * time.Millisecond
	p, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), "pi", "neueres whoami",
		proposal.Edit{Set: map[string]string{"services/whoami/values.yml:IMAGE": "docker.io/traefik/whoami:v1.11"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	unlock, _, err := apply.Lock(env.Paths, "apply:human:other")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /inbox/{id}/{decision}", pages.Decision(env))
	req, cancel := context.WithCancel(ctx)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/inbox/1/approve", nil).WithContext(req))
	cancel() // the browser is gone
	if body := w.Body.String(); strings.Contains(body, "fehlgeschlagen") || w.Code >= 400 {
		t.Fatalf("a decision behind the lock failed: %d\n%s", w.Code, body)
	}
	if s := pages.DecisionState(p.ID); !strings.Contains(s, "wartet") {
		t.Errorf("while the lock is held the Inbox should say it waits: %q", s)
	}
	unlock()
	for range 200 {
		if got, _ := proposal.Get(cli.InboxDir(env), p.ID); got.Status == "approved" && pages.DecisionState(p.ID) == "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := proposal.Get(cli.InboxDir(env), p.ID)
	t.Fatalf("the queued approval never ran: %+v, state %q", got, pages.DecisionState(p.ID))
}
