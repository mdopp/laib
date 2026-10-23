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

// #607, #609: the owner approved four proposals in a row; every tap while
// an apply held the lock failed, and then waited its turn. Approving is a
// merge now: the tap answers at once, the proposal is approved before the
// running apply ends, and the run after it applies it — outside the
// request, so a closed connection cuts nothing off.
func TestDecisionsAnswerAtOnceAndOutliveTheRequest(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	p, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), "pi", "neueres whoami",
		proposal.Edit{Set: map[string]string{"services/whoami/values.yml:IMAGE": "docker.io/traefik/whoami:v1.11"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := cli.LoadState(env)
	unlock, _ := apply.TakeLock(env, st, "apply")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /inbox/{id}/{decision}", pages.Decision(env))
	req, cancel := context.WithCancel(ctx)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/inbox/1/approve", nil).WithContext(req))
	cancel() // the browser is gone
	if body := w.Body.String(); strings.Contains(body, "fehlgeschlagen") || w.Code >= 400 {
		t.Fatalf("a decision behind the lock failed: %d\n%s", w.Code, body)
	}
	approved := func() bool {
		got, _ := proposal.Get(cli.InboxDir(env), p.ID)
		return got.Status == "approved" && pages.DecisionState(p.ID) == ""
	}
	for i := 0; !approved(); i++ {
		if i == 100 {
			got, _ := proposal.Get(cli.InboxDir(env), p.ID)
			t.Fatalf("not approved while the lock is held: %+v, state %q", got, pages.DecisionState(p.ID))
		}
		time.Sleep(20 * time.Millisecond)
	}
	unlock()
	for i := 0; ; i++ {
		got, _ := proposal.Get(cli.InboxDir(env), p.ID)
		if got.Outcome != "waiting" && got.Batch != "" {
			return
		}
		if i == 200 {
			t.Fatalf("the run after the lock did not carry it: %+v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
