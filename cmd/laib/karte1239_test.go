package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/registry"
	"github.com/mdopp/laib/internal/sysd"
)

// karteBox: three installed services and a registry whose templates of
// them bring news; quiet is a fourth whose template changes only comments.
func karteBox(t *testing.T) (*cliEnv, *cliEnv) {
	t.Helper()
	ctx := context.Background()
	env, _ := testBox(t)
	human := &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}
	for _, name := range []string{"w1", "w2", "w3", "quiet"} {
		inbox.Approve(ctx, human, []string{fmt.Sprint(proposeService(t, env, name).ID)})
		tpl := filepath.Join(env.Paths.Home, "registries", "test", "templates", name)
		os.MkdirAll(tpl, 0o755)
		unit := "[Container]\nImage=${IMAGE}\nEnvironment=NEU=1\n\n[Install]\nWantedBy=default.target\n"
		if name == "quiet" {
			unit = "# quiet, as its template says now\n[Container]\nImage=${IMAGE}\n\n[Install]\nWantedBy=default.target\n"
		}
		os.WriteFile(filepath.Join(tpl, "laib.yml"), []byte("name: "+name+"\n"), 0o644)
		os.WriteFile(filepath.Join(tpl, "values.example.yml"), []byte("IMAGE: docker.io/traefik/whoami:v1.10\n"), 0o644)
		os.WriteFile(filepath.Join(tpl, name+".container"), []byte(unit), 0o644)
		os.WriteFile(filepath.Join(tpl, "news"), []byte(strings.ToUpper(name)+" kann jetzt mehr\n"), 0o644)
	}
	cli.Version = "2.31.0"
	t.Cleanup(func() { cli.Version = "dev" })
	return env, human
}

// nightSync is the night's `template sync --propose` without the clone: each
// installed service's update, by laib.
func nightSync(t *testing.T, env *cliEnv) {
	t.Helper()
	st, _ := cli.LoadState(env)
	laib := &cliEnv{Paths: env.Paths, Actor: "laib", Out: io.Discard, ErrOut: io.Discard}
	for _, name := range st.ServiceNames() {
		registry.Update(context.Background(), laib, st, name)
	}
}

// #1239: the template updates of one Laib release are one card, wherever
// the owner sees it: `laib inbox` (Homes, the chat), the Inbox page. One
// tap approves all; a service red after it goes back alone (#1209), and
// a template that changes only comments makes no card and no restart.
// Ablauf L-55, L-56
func TestTheTemplateUpdatesOfOneReleaseComeAsOneCardAndOneTap(t *testing.T) {
	ctx := context.Background()
	env, human := karteBox(t)
	nightSync(t, env)

	var out bytes.Buffer
	inbox.List(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", JSON: true, Out: &out, ErrOut: io.Discard}, nil)
	var cards []struct {
		ID    int
		Karte string
		Card  struct{ Summary string }
	}
	if err := json.Unmarshal(out.Bytes(), &cards); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if len(cards) != 1 || cards[0].Karte != "Laib 2.31.0" || cards[0].Card.Summary != "Neue Vorlagen aus Laib 2.31.0 für W1, W2, W3" {
		t.Fatalf("want one card for the three services, got %s", out.String())
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 3 {
		t.Fatalf("under the card, one proposal per service: %d", len(open))
	}
	page := inboxMux(env)("GET", "/inbox", nil)
	if strings.Count(page, "<section class=card") != 1 || !strings.Contains(page, "W2: W2 kann jetzt mehr") || strings.Contains(page, "template update") {
		t.Fatalf("the Inbox page does not show one card in words:\n%s", page)
	}

	// the comment-only template: no card, no change on main, one journal line, once
	if slices.ContainsFunc(open, func(p *proposal.Proposal) bool { return slices.Contains(p.Files, "services/quiet/quiet.container") }) {
		t.Fatal("a comment-only template update got a card")
	}
	nightSync(t, env) // the next night: nothing new, the line is not written again
	es, _ := journal.Read(env.Paths.Journal, time.Now().Add(-time.Hour))
	if n := len(slices.DeleteFunc(es, func(e journal.Entry) bool {
		return e.Service != "quiet" || !strings.Contains(e.Detail, "nur Kommentare")
	})); n != 1 {
		t.Fatalf("want one journal line for quiet's comments, got %d", n)
	}
	if open, _ := proposal.List(cli.InboxDir(env), true); len(open) != 3 {
		t.Fatalf("the second sync filed again: %d open", len(open))
	}

	// one tap; w2 does not come up with its new unit
	run := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && slices.Contains(args, "is-active") && slices.Contains(args, "w2.service") {
			if b, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, "w2", "w2.container")); strings.Contains(string(b), "NEU=1") {
				return "failed", nil
			}
		}
		return run(ctx, name, args...)
	}
	quiet, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, "quiet", "quiet.container"))
	unlock, _ := apply.TakeLock(env, nil, "apply") // the three in one run, as a tap on the box page while nothing else runs
	if code := inbox.Approve(ctx, human, []string{fmt.Sprint(cards[0].ID)}); code != cli.ExitOK {
		t.Fatalf("approve the card: %d", code)
	}
	unlock()
	inbox.Carry(ctx, human)
	for _, p := range open {
		got, _ := proposal.Get(cli.InboxDir(env), p.ID)
		b, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, p.Intent.Update, p.Intent.Update+".container"))
		red := p.Intent.Update == "w2"
		if got.Status != "approved" || (got.Outcome == "rolled-back") != red || strings.Contains(string(b), "NEU=1") == red {
			t.Errorf("%s: %s %s, unit %q", p.Intent.Update, got.Status, got.Outcome, b)
		}
	}
	if now, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, "quiet", "quiet.container")); !bytes.Equal(now, quiet) {
		t.Error("quiet's unit changed for a comment")
	}
}
