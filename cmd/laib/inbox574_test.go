package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/proposal"
)

// #574: the owner approved a proposal, the apply ran green, and a second
// submit of the same form showed "Entscheidung fehlgeschlagen (exit 1)". A
// decision on a proposal that is no longer open says what is true; a real
// failure shows its error line; the button says it is working.
func TestTheInboxTellsTheTruthAboutASecondSubmit(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	if out, code := mcp.Call(ctx, env, st, "bote", "laib_propose", map[string]any{"args": `--set services/whoami/values.yml:IMAGE=docker.io/traefik/whoami:v1.11 "neueres whoami"`}); code != cli.ExitOK {
		t.Fatalf("propose: exit %d: %s", code, out)
	}
	all, _ := proposal.List(cli.InboxDir(env), false)
	if len(all) != 1 || all[0].Status != "approved" {
		t.Fatalf("want one approved proposal: %+v", all)
	}
	p := all[0]
	mux := http.NewServeMux()
	mux.HandleFunc("POST /inbox/{id}/{decision}", pages.Decision(env))
	post := func(path string) (int, string) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		return w.Code, w.Body.String()
	}

	for _, d := range []string{"approve", "reject"} {
		code, body := post("/inbox/1/" + d)
		at := p.Decided.Local().Format("15:04")
		if code != http.StatusOK || !strings.Contains(body, "#1 ist schon freigegeben, von laib, um "+at) || strings.Contains(body, "fehlgeschlagen") || strings.Contains(body, "class=rot") {
			t.Errorf("a second %s: %d\n%s", d, code, body)
		}
	}
	if got, _ := proposal.Get(cli.InboxDir(env), 1); got.Status != "approved" || got.DecidedBy != "laib" {
		t.Errorf("the second submit changed the proposal: %+v", got)
	}

	// a real failure names its reason, not only the exit code
	if _, body := post("/inbox/99/approve"); !strings.Contains(body, "<p class=rot>Entscheidung fehlgeschlagen: ") || !strings.Contains(body, "99") || strings.Contains(body, "exit 1") {
		t.Errorf("a failure without its error line:\n%s", body)
	}

	// the button gives feedback while the apply runs, so a second tap does not happen
	form := pages.DecisionForm(7, "approve", "Freigeben", "go")
	if !strings.Contains(form, `action=/inbox/7/approve`) || !strings.Contains(form, "disabled=true") || !strings.Contains(form, "wird angewendet") || !strings.Contains(form, "onsubmit=") {
		t.Errorf("no feedback on submit: %s", form)
	}
}
