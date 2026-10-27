package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/secrets"
)

// fakeGitHub answers POST /repos/<repo>/issues like GitHub and records it.
type fakeGitHub struct {
	srv   *httptest.Server
	calls []map[string]any
	auth  []string
	paths []string
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in map[string]any
		json.Unmarshal(body, &in)
		g.calls = append(g.calls, in)
		g.auth = append(g.auth, r.Header.Get("Authorization"))
		g.paths = append(g.paths, r.Method+" "+r.URL.Path)
		if !strings.HasSuffix(r.URL.Path, "/issues") || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"number": 42, "html_url": "https://github.com/mdopp/x/issues/42"})
	}))
	t.Cleanup(g.srv.Close)
	return g
}

// intake configures box.yml intake and starts laib-haushalt with the token
// in its own environment, as its unit gets it (#797). secrets.age carries a
// different value: the kernel must not be the one that sends it.
func (c *cbox) intake(t *testing.T, repo string, g *fakeGitHub) {
	t.Helper()
	if repo != "" {
		p := filepath.Join(c.env.Paths.State, "box.yml")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, append(b, []byte("intake:\n  repo: "+repo+"\n  label: wunsch\n")...), 0o644)
	}
	sec, _ := secrets.Load(c.env.Paths.Secrets, c.env.Paths.Key)
	sec["GITHUB_INTAKE_TOKEN"] = "ghp_kernel"
	secrets.Save(c.env.Paths.Secrets, c.env.Paths.Key, sec)
	t.Setenv("GITHUB_API_URL", g.srv.URL)
	t.Setenv("GITHUB_INTAKE_TOKEN", "ghp_test")
	withHaushalt(t, c.env)
}

// wish runs laib wish as the verb table has it.
func (c *cbox) wish(t *testing.T, args ...string) int {
	t.Helper()
	return verb(t, "wish").Run(context.Background(), c.env, args)
}

// #56 (1): a wish becomes a GitHub issue with the intake label — the Bote's
// "Wunsch aufnehmen" — and a journal line that names it. Since #797
// laib-haushalt files it behind the verb door with its own token.
func TestWishFilesAnIssueWithTheIntakeLabel(t *testing.T) {
	c := newCbox(t)
	g := newFakeGitHub(t)
	c.intake(t, "mdopp/x", g)
	c.env.Actor = "bote"
	if rc := c.wish(t, "Ich brauche einen Dienst, der Rezepte sammelt", "und mit dem Handy teilt"); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	if len(g.calls) != 1 || g.paths[0] != "POST /repos/mdopp/x/issues" {
		t.Fatalf("one issue expected: %v %v", g.paths, g.calls)
	}
	in := g.calls[0]
	if g.auth[0] != "Bearer ghp_test" {
		t.Fatalf("auth %q: the issue went out with another token than laib-haushalt's (#797)", g.auth[0])
	}
	if title, _ := in["title"].(string); !strings.HasPrefix(title, "Wunsch: Ich brauche einen Dienst") {
		t.Fatalf("title %q", title)
	}
	if body, _ := in["body"].(string); !strings.Contains(body, "Rezepte sammelt und mit dem Handy teilt") || !strings.Contains(body, "bote") {
		t.Fatalf("body %q", body)
	}
	labels, _ := in["labels"].([]any)
	if len(labels) != 1 || labels[0] != "wunsch" {
		t.Fatalf("labels %v — never pi:ready: a wish is triaged by a person first", labels)
	}
	if !c.journalHas(t, "issues/42") {
		t.Fatal("the journal names the issue")
	}
}

// #56 (1): without an intake, the wish is kept on the box and the answer says
// that nobody reads it yet — not an error, not silently dropped. Since #797
// it is kept in the journal: laib-haushalt has no folder of the box.
func TestWishWithoutIntakeIsKeptInTheJournal(t *testing.T) {
	c := newCbox(t)
	g := newFakeGitHub(t)
	c.intake(t, "", g)
	if rc := c.wish(t, "ein Kalender für alle"); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	if len(g.calls) != 0 {
		t.Fatal("no intake, no issue")
	}
	if !c.journalHas(t, "kein Intake") || !c.journalHas(t, "ein Kalender für alle") {
		t.Fatal("the journal keeps the wish")
	}
}

// #56 (1): an escalation becomes an issue too, so something reads it.
func TestEscalateFilesAnIssueWhenIntakeIsConfigured(t *testing.T) {
	c := newCbox(t)
	g := newFakeGitHub(t)
	c.intake(t, "mdopp/x", g)
	c.env.Actor = "ops"
	if rc := envelope.Escalate(context.Background(), c.env, []string{"caddy", "startet nicht, Config-Schlüssel unbekannt"}); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	if len(g.calls) != 1 {
		t.Fatalf("one issue: %v", g.calls)
	}
	in := g.calls[0]
	if title, _ := in["title"].(string); !strings.Contains(title, "caddy") {
		t.Fatalf("title %q", title)
	}
	if body, _ := in["body"].(string); !strings.Contains(body, "Config-Schlüssel") || !strings.Contains(body, "## inspect") {
		t.Fatalf("the bundle travels with the issue: %q", body)
	}
	labels, _ := in["labels"].([]any)
	if len(labels) != 1 || labels[0] != "needs-claude" {
		t.Fatalf("labels %v", labels)
	}
	if !c.journalHas(t, "issues/42") {
		t.Fatal("the journal names the issue")
	}
}

// #797 (ADR 0026 §4): the kernel holds no GitHub client and reads no
// GitHub token; laib-haushalt files every ticket behind the verb door.
func TestTheKernelHoldsNoGitHubClient(t *testing.T) {
	for _, dir := range []string{"../../cmd", "../../internal"} {
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			b, _ := os.ReadFile(p)
			for _, s := range []string{"api.github.com", `sec["GITHUB_INTAKE_TOKEN"]`, "TokenSecret"} {
				if strings.Contains(string(b), s) {
					t.Errorf("%s: %s — the kernel files no GitHub issue itself (#797)", p, s)
				}
			}
			return nil
		})
	}
}
