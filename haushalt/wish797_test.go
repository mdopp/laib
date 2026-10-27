package haushalt

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gitHub answers POST /repos/<repo>/issues like GitHub and records each call.
type gitHub struct {
	srv   *httptest.Server
	calls []map[string]any
	auth  []string
	paths []string
}

func newGitHub(t *testing.T) *gitHub {
	t.Helper()
	g := &gitHub{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var in map[string]any
		json.Unmarshal(b, &in)
		g.calls, g.auth, g.paths = append(g.calls, in), append(g.auth, r.Header.Get("Authorization")), append(g.paths, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"number": 42, "html_url": "https://github.com/mdopp/x/issues/42"})
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func run797(t *testing.T, gh GitHub, in *Intake, args ...string) (map[string]string, int, string, string) {
	t.Helper()
	var out, errb, jr bytes.Buffer
	env := &Env{Call: Call{JSON: true, Actor: "bote", Ceiling: "propose", Intake: in}, GitHub: gh, Out: &out, Err: &errb, Journal: &jr}
	code := Run(context.Background(), env, args)
	var reply map[string]string
	json.Unmarshal(out.Bytes(), &reply)
	return reply, code, errb.String(), jr.String()
}

// #797 (ADR 0026 §4, #56): laib wish moved behind the verb door. A wish
// becomes a GitHub issue in box.yml intake.repo with the intake label —
// never pi:ready, a person triages it first — filed with this program's own
// token (GITHUB_INTAKE_TOKEN in its unit), which the kernel no longer reads.
// The journal line names the issue.
func TestAWishBecomesAnIssueWithThisProgramsToken(t *testing.T) {
	g := newGitHub(t)
	gh := GitHub{API: g.srv.URL, Token: "ghp_haushalt"}
	got, code, errs, journal := run797(t, gh, &Intake{Repo: "mdopp/x", Label: "wunsch", Domain: "nuc.home"}, "wish", "Ich brauche einen Dienst, der Rezepte sammelt", "und mit dem Handy teilt")
	if code != exitOK || got["issue"] != "https://github.com/mdopp/x/issues/42" {
		t.Fatalf("exit %d: %v %s", code, got, errs)
	}
	if len(g.calls) != 1 || g.paths[0] != "POST /repos/mdopp/x/issues" || g.auth[0] != "Bearer ghp_haushalt" {
		t.Fatalf("one issue with this program's token expected: %v %v", g.paths, g.auth)
	}
	in := g.calls[0]
	if title, _ := in["title"].(string); !strings.HasPrefix(title, "Wunsch: Ich brauche einen Dienst") {
		t.Fatalf("title %q", title)
	}
	if body, _ := in["body"].(string); !strings.Contains(body, "Rezepte sammelt und mit dem Handy teilt") || !strings.Contains(body, "bote") || !strings.Contains(body, "nuc.home") {
		t.Fatalf("body %q", body)
	}
	if labels, _ := in["labels"].([]any); len(labels) != 1 || labels[0] != "wunsch" {
		t.Fatalf("labels %v — never pi:ready", labels)
	}
	if !strings.Contains(journal, "issues/42") {
		t.Fatalf("the journal names the issue: %q", journal)
	}

	// without a token: an error that says which secret, nothing filed
	if _, code, errs, _ := run797(t, GitHub{API: g.srv.URL}, &Intake{Repo: "mdopp/x"}, "wish", "noch einer"); code == exitOK || !strings.Contains(errs, "GITHUB_INTAKE_TOKEN") || len(g.calls) != 1 {
		t.Fatalf("no token: exit %d, %q, %d calls", code, errs, len(g.calls))
	}
	// no text: usage, nothing filed
	if _, code, _, _ := run797(t, gh, &Intake{Repo: "mdopp/x"}, "wish", "  "); code == exitOK || len(g.calls) != 1 {
		t.Fatalf("an empty wish: exit %d", code)
	}
}

// #797: without box.yml intake the wish is kept in the journal, which the
// kernel writes, and the answer says nobody reads it yet: not an error, not
// dropped. This program has no folder of the box to keep it in.
func TestAWishWithoutIntakeIsKeptInTheJournal(t *testing.T) {
	g := newGitHub(t)
	got, code, errs, journal := run797(t, GitHub{API: g.srv.URL, Token: "ghp_haushalt"}, &Intake{}, "wish", "ein Kalender für alle")
	if code != exitOK || got["issue"] != "" || len(g.calls) != 0 {
		t.Fatalf("exit %d: %v %s, %d calls", code, got, errs, len(g.calls))
	}
	if !strings.Contains(journal, "kein Intake") || !strings.Contains(journal, "ein Kalender für alle") {
		t.Fatalf("the journal keeps the wish: %q", journal)
	}
}

// #797: the kernel's own tickets (an escalation, a wish the admin approved)
// come with title, body and label set by the kernel in the door's call,
// never from argv; this program files them as they are and journals
// nothing, the kernel does.
func TestTheKernelsOwnTicketIsFiledAsItIs(t *testing.T) {
	g := newGitHub(t)
	in := &Intake{Repo: "mdopp/x", Label: "needs-claude", Title: "Eskalation caddy: startet nicht", Body: "# Eskalation\n\n## inspect\n"}
	got, code, errs, journal := run797(t, GitHub{API: g.srv.URL, Token: "ghp_haushalt"}, in, "wish")
	if code != exitOK || got["issue"] == "" || len(g.calls) != 1 {
		t.Fatalf("exit %d: %v %s", code, got, errs)
	}
	if g.calls[0]["title"] != in.Title || g.calls[0]["body"] != in.Body {
		t.Fatalf("filed %v", g.calls[0])
	}
	if labels, _ := g.calls[0]["labels"].([]any); len(labels) != 1 || labels[0] != "needs-claude" {
		t.Fatalf("labels %v", labels)
	}
	if journal != "" {
		t.Fatalf("the kernel journals its own ticket, not this program: %q", journal)
	}
}
