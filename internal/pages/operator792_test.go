package pages

import (
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
)

// #792, the owner: „ER soll das lösen. Aber ich weiß nicht, wo ich sehe, ob
// er es macht.“ The Box page and laib_status say what the Operator works
// on, why it waits, and what it did today, with the Inbox and the Journal.
func TestTheBoxPageSaysWhatTheOperatorDoes(t *testing.T) {
	env := &cli.Env{Paths: boxfs.Paths{Journal: t.TempDir()}}
	now := time.Now()
	for _, e := range []journal.Entry{
		{T: now.Add(-48 * time.Hour), Actor: "ops", Verb: "restart", Service: "media", Result: "green"},
		{T: now, Actor: "ops", Verb: "note", Result: "info", Detail: "Vorfall: bote: unit bote-check.service ist failed."},
		{T: now, Actor: "ops", Verb: "restart", Service: "bote", Result: "red", Detail: "bote-check.service failed"},
		{T: now, Actor: "ops", Verb: "escalate", Service: "bote", Result: "red", Detail: "Vorfall seit 10:31"},
		{T: now, Actor: "human:mdopp", Verb: "restart", Service: "immich", Result: "green"},
	} {
		journal.Append(env.Paths.Journal, e)
	}
	w := operatorToday(env, diag.OpWork{Incident: "bote: unit bote-check.service ist failed", Since: now, Step: "startet bote neu", Waiting: "die Grafikkarte ist vergeben", Queued: 1}, now)
	if w.Restarts != 1 || w.Escalations != 1 || w.Notes != 1 || w.Proposals != 0 || len(w.Today) != 3 || !strings.Contains(w.Today[0], "Eskalation bote") {
		t.Fatalf("today counts another hand's or another day's lines: %+v", w)
	}
	page := statusBody(Readiness{State: "bereit", OperatorWork: w}, nil, nil, "", [2]string{}, false, "")
	for _, want := range []string{"<h2>Operator</h2>", "Arbeitet an: bote: unit bote-check.service ist failed", "zuletzt: startet bote neu", "Wartet: die Grafikkarte ist vergeben · 1 Vorfall in der Warteschlange",
		"Heute: 1 Neustart · 0 Vorschläge · 1 Eskalation · 1 Notiz", `href="/inbox"`, `href="/journal"`, "Neustart bote: bote-check.service failed"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if idle := statusBody(Readiness{State: "bereit"}, nil, nil, "", [2]string{}, false, ""); !strings.Contains(idle, "Gerade kein Vorfall.") || strings.Contains(idle, "Wartet") {
		t.Errorf("an idle Operator: %s", idle)
	}
}
