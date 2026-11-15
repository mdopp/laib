package inbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
)

func update(id int, actor, karte string) *proposal.Proposal {
	return &proposal.Proposal{ID: id, Actor: actor, Reason: "template update w", Kind: "template-update", Class: "template-update", Tier: "apply", Status: "open", Karte: karte,
		Files: []string{"services/w/w.container"}}
}

// #1239: the box's own template update goes on the release's card, and an
// older one still open joins it; a person's or an agent's stays its own.
// Ablauf L-55
func TestOnlyTheBoxsOwnTemplateUpdatesGoOnTheReleasesCard(t *testing.T) {
	env, _, _ := chainBox(t)
	cli.Version = "2.31.0"
	t.Cleanup(func() { cli.Version = "dev" })
	older, plain, agent := save(t, env, update(1, "laib", "Laib 2.30.0")), save(t, env, update(2, "laib", "")), save(t, env, update(3, "claude", ""))
	values := update(4, "laib", "")
	values.Kind = "values"
	save(t, env, values)
	p := save(t, env, update(5, "laib", ""))

	env.Actor = "claude"
	OnCard(env, p)
	if p.Karte != "" {
		t.Fatalf("an agent's template update went on the card: %q", p.Karte)
	}
	env.Actor = "laib"
	OnCard(env, p)
	if p.Karte != "Laib 2.31.0" {
		t.Fatalf("the box's own update is not on the release's card: %q", p.Karte)
	}
	for _, q := range []*proposal.Proposal{older, plain, p, agent, values} {
		got, _ := proposal.Get(cli.InboxDir(env), q.ID)
		want := ""
		if q.Actor == "laib" && q.Kind == "template-update" {
			want = "Laib 2.31.0"
		}
		if got.Karte != want {
			t.Errorf("#%d: karte %q, want %q", q.ID, got.Karte, want)
		}
	}
	p.Kind = "values"
	p.Karte = ""
	OnCard(env, p)
	if p.Karte != "" {
		t.Error("a values change of laib went on the template card")
	}
}

// #1239: one decision is the whole card; a proposal on no card, or on
// another, stays as it is; an agent's approve of a card with one member
// only a human may approve is refused before anything merges.
// Ablauf L-55
func TestOneDecisionIsTheWholeCard(t *testing.T) {
	ctx := context.Background()
	env, _, out := chainBox(t)
	a, b := save(t, env, update(1, "laib", "Laib 2.31.0")), save(t, env, update(2, "laib", "Laib 2.31.0"))
	other, alone := save(t, env, update(3, "laib", "Laib 2.30.0")), save(t, env, update(4, "pi", ""))
	if code := Decide(ctx, env, []string{"#2", "nicht", "jetzt"}, false); code != cli.ExitOK {
		t.Fatalf("reject the card: %d", code)
	}
	status := func(p *proposal.Proposal) string {
		got, _ := proposal.Get(cli.InboxDir(env), p.ID)
		return got.Status + " " + got.Outcome
	}
	if status(a) != "rejected later" || status(b) != "rejected later" {
		t.Errorf("one no is the whole card's: %s, %s", status(a), status(b))
	}
	if status(other) != "open " || status(alone) != "open " {
		t.Errorf("another card or a proposal on none went with it: %s, %s", status(other), status(alone))
	}
	if code := Decide(ctx, env, []string{"#4"}, false); code != cli.ExitOK || status(other) != "open " {
		t.Errorf("a proposal on no card decides only itself: %d, %s", code, status(other))
	}

	pol := filepath.Join(env.Paths.State, "policy.yml") // only what the content review marks needs a human
	b0, _ := os.ReadFile(pol)
	os.WriteFile(pol, []byte(strings.Replace(string(b0), "  apply: {by: human}\n", "", 1)), 0o644)
	h, w := save(t, env, update(10, "laib", "Laib 2.32.0")), update(11, "laib", "Laib 2.32.0")
	w.Class = "human"
	save(t, env, w)
	env.Actor = "claude"
	if code := Decide(ctx, env, []string{"#10"}, true); code != cli.ExitRefused {
		t.Errorf("an agent's approve of a card with a human-only member: %d, want refused", code)
	}
	if !strings.Contains(out.String(), "nur ein Mensch: Stufe apply verlangt einen Menschen") {
		t.Errorf("the refusal does not say why: %s", out.String())
	}
	if status(h) != "open " || status(w) != "open " {
		t.Errorf("something of the card was decided: %s, %s", status(h), status(w))
	}
}

// `laib inbox` shows the card; `laib inbox <id>` the member as it is.
func TestTheInboxListsTheCardAndAMemberByItsNumber(t *testing.T) {
	env, _, _ := chainBox(t)
	save(t, env, update(1, "laib", "Laib 2.31.0"))
	save(t, env, update(2, "laib", "Laib 2.31.0"))
	gone := update(3, "pi", "")
	gone.Status = "rejected"
	save(t, env, gone)
	all, _ := listed(env, true, 0)
	one, _ := listed(env, true, 2)
	if len(all) != 1 || all[0].ID != 1 || all[0].Reason != "Neue Vorlagen aus Laib 2.31.0" {
		t.Errorf("the open list is one card: %+v", all)
	}
	if len(one) == 0 || one[0].Reason != "template update w" {
		t.Errorf("a member by its number is itself: %+v", one)
	}
}

// #1239: a template that changes only comments is one journal line, not
// one a night; another service's, or the same after 30 days, is its own.
// Ablauf L-56
func TestACommentOnlyTemplateIsOneJournalLine(t *testing.T) {
	env, _, _ := chainBox(t)
	old := journal.Entry{T: time.Now().AddDate(0, 0, -31), Actor: "laib", Verb: "template", Service: "quiet", Result: "info",
		Detail: "die Vorlage von quiet ändert nur Kommentare: keine Karte, kein Neustart"}
	journal.Append(env.Paths.Journal, old)
	Inert(env, "quiet")
	Inert(env, "quiet")
	Inert(env, "still")
	es, _ := journal.Read(env.Paths.Journal, time.Now().Add(-time.Hour))
	var lines []string
	for _, e := range es {
		lines = append(lines, e.Service+": "+e.Detail)
	}
	if len(lines) != 2 || !strings.Contains(lines[0], "quiet: die Vorlage von quiet ändert nur Kommentare") || !strings.HasPrefix(lines[1], "still: die Vorlage von still") {
		t.Errorf("want one line per service: %q", lines)
	}
	if _, err := os.Stat(filepath.Join(env.Paths.Journal, old.T.UTC().Format("2006-01-02")+".jsonl")); err != nil {
		t.Fatal(err)
	}
}
