package main

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// #855: no raw or technical text in front of people. The visible line is
// German, says what it means and names the next step; the raw text lives
// under Details. Two halves: a lint over the source of what people read,
// and the known offenders rendered and read like a person reads them.

// peopleFiles put text in front of people. osupdate.go and watchdog.go
// are loopback endpoints for scripts, not people.
var peopleFiles = []string{
	"internal/pages/serve.go", "internal/pages/inbox.go", "internal/pages/schluessel.go", "internal/pages/status.go",
	"internal/proposal/card.go",
}

// plainSinks may take a raw error: they turn it into a sentence, put it
// under Details, or write it to the journal.
// keyEnd's third argument is its Details.
var plainSinks = map[string]bool{"Plain": true, "failure": true, "details": true, "Entry": true, "Record": true, "keyEnd": true}

func TestNoRawErrorTextInWhatPeopleRead(t *testing.T) {
	for _, f := range peopleFiles {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join("..", "..", f), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var stack []ast.Node
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, n)
			switch x := n.(type) {
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" && len(x.Args) == 0 && !inSink(stack[:len(stack)-1]) {
					t.Errorf("%s: err.Error() in front of people (#855): through proposal.Plain or under Details", fset.Position(x.Pos()))
				}
			case *ast.BasicLit:
				// an HTML fragment is for people; other literals may be logic or the shell's
				html := strings.HasPrefix(strings.Trim(x.Value, "`\""), "<")
				if x.Kind == token.STRING && (strings.Contains(x.Value, "exit %d") || html && technicalText.MatchString(visibleText(strings.Trim(x.Value, "`\"")))) {
					t.Errorf("%s: technical text in front of people (#855): %s", fset.Position(x.Pos()), x.Value)
				}
			}
			return true
		})
	}
}

// inSink: an enclosing call or literal takes raw text on purpose.
func inSink(stack []ast.Node) bool {
	for _, n := range stack {
		var name string
		switch x := n.(type) {
		case *ast.CallExpr:
			switch f := x.Fun.(type) {
			case *ast.Ident:
				name = f.Name
			case *ast.SelectorExpr:
				name = f.Sel.Name
			}
		case *ast.CompositeLit:
			if sel, ok := x.Type.(*ast.SelectorExpr); ok {
				name = sel.Sel.Name
			}
		}
		if plainSinks[name] {
			return true
		}
	}
	return false
}

// visibleCard is what the Inbox shows of a card above its Technische Details.
func visibleCard(c proposal.Decision) string {
	parts := []string{c.Summary, c.Why, c.Reversible, c.RiskWhy, c.Decider}
	for _, l := range c.Changes {
		parts = append(parts, l.Text)
	}
	return strings.Join(append(append(parts, c.Impact...), c.Warnings...), "\n")
}

// #855: a repair's card read „Reparieren: whoami.service ist failed —
// laib restart whoami“ under „Wer will das“.
func TestARepairCardSpeaksPlainly(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	failedWhoami(t)
	st, _ := cli.LoadState(env)
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_repair", map[string]any{"probe": "unit", "service": "whoami"}); code != cli.ExitOK {
		t.Fatalf("repair: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	c := proposal.Card(open[0], "box.test")
	plainEnough(t, "the repair card", visibleCard(c))
	if !strings.Contains(c.Details, "whoami.service") || !strings.Contains(c.Details, "laib restart whoami") {
		t.Errorf("the finding and the command belong under Details: %q", c.Details)
	}
}

// #855: a value change's headline was the raw key and image, „Vaultwarden:
// IMAGE wird docker.io/… → …“, and the same line went to Signal (#778).
func TestAValueChangeHeadlineSaysWhatChanges(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	if code := inbox.Propose(ctx, &cliEnv{Paths: env.Paths, Actor: "human:other"}, []string{"--set", "services/whoami/values.yml:IMAGE=docker.io/traefik/whoami:v1.11", "neueres whoami"}); code != cli.ExitOK {
		t.Fatalf("propose: %d", code)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	c := proposal.Card(open[0], "box.test")
	if strings.Contains(c.Summary, "IMAGE") || strings.Contains(c.Summary, "docker.io") || !strings.Contains(c.Summary, "neue Version") || !strings.Contains(c.Summary, "v1.11") {
		t.Errorf("the headline is the key and the image, not what changes: %q", c.Summary)
	}
}

// #855: the readiness lock read „firewall: table inet laib fehlt im Kernel
// (laib converge --force lädt sie)“; since #856 the missing firewall is a
// red line on Status, in words, diag's line under Details.
func TestTheReadinessLockSpeaksPlainly(t *testing.T) {
	env, _ := testBox(t)
	os.MkdirAll(filepath.Dir(apply.FirewallFile(env.Paths)), 0o755)
	os.WriteFile(apply.FirewallFile(env.Paths), []byte("table inet laib {}\n"), 0o644)
	prev := sysd.Run
	t.Cleanup(func() { sysd.Run = prev })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "sudo" {
			return "", os.ErrNotExist
		}
		return prev(ctx, name, args...)
	}
	mux := pages.Routes(env, func(h http.HandlerFunc) http.HandlerFunc { return h }) // since #856 it is red on Status, not a lock
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(visibleText(w.Body.String()), "Firewall") {
		t.Fatalf("the missing firewall is not on the Status page:\n%s", visibleText(w.Body.String()))
	}
	plainEnough(t, "the Status page without its firewall", w.Body.String())
}

// #855: what diag finds reaches people (the red mail, a repair's card) as
// one plain sentence, "say"; detail and fix stay for the admin.
func TestEveryDiagFindingCarriesAPlainSentence(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	svc := filepath.Join(env.Paths.State, "services", "whoami", "laib.yml")
	b, _ := os.ReadFile(svc)
	os.WriteFile(svc, append(b, []byte("health: {http: http://127.0.0.1:1/}\n")...), 0o644)
	failedWhoami(t)
	st, _ := cli.LoadState(env)
	out, code := mcp.Call(ctx, env, st, "ops", "laib_diag", map[string]any{})
	if code != cli.ExitOK && code != cli.ExitRed {
		t.Fatalf("diag: exit %d: %s", code, out)
	}
	var fs []map[string]any
	if err := json.Unmarshal([]byte(out), &fs); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	probes := map[string]bool{}
	for _, f := range fs {
		probes[f["probe"].(string)] = true
		say, _ := f["say"].(string)
		if say == "" {
			t.Errorf("a finding without its plain sentence: %v", f)
			continue
		}
		plainEnough(t, "diag's say", say)
	}
	if !probes["unit"] || !probes["health"] {
		t.Fatalf("the fixture lacks the unit and the health finding: %s", out)
	}
}

// #855: the Status page while a run holds the lock and a unit failed: the
// lock holder and the unit's state under Details, the lines in words.
func TestTheStatusPageSpeaksPlainlyWhileTheBoxWorks(t *testing.T) {
	env, _ := testBox(t)
	failedWhoami(t)
	st, _ := cli.LoadState(env)
	unlock, _ := apply.TakeLock(env, st, "apply")
	defer unlock()
	mux := pages.Routes(env, func(h http.HandlerFunc) http.HandlerFunc { return h })
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	plainEnough(t, "the Status page", w.Body.String())
	if !strings.Contains(w.Body.String(), "whoami.service=failed") || !strings.Contains(w.Body.String(), "gesperrt durch apply:") {
		t.Errorf("the unit and the lock holder are gone, not under Details:\n%s", w.Body.String())
	}
}
