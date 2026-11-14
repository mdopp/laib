package main

import (
	"bytes"
	"context"
	"html"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
)

// Durchgang rot (owner, 2026-11-14): laib note --karten puts the cause the
// Operator read onto a red run's cards and the follow-ups laib filed for
// them, and the Inbox shows it there instead of „was schiefging, steht im
// Journal“. Another card keeps its words, and a technical text reaches no
// card (#855): it stays in the journal.
// Ablauf L-50
func TestTheCauseOfARedRunGoesOntoItsCards(t *testing.T) {
	env, _ := testBox(t)
	dir, now := cli.InboxDir(env), time.Now().UTC()
	run := "#1, #2 (f548fec..7fc9485)"
	os.MkdirAll(dir, 0o755)
	for _, p := range []*proposal.Proposal{
		{ID: 1, Actor: "ops", Reason: "Neue Versionen: beets", Status: "approved", Outcome: "rolled-back", Batch: run, Restore: 3, Decided: now, Settled: now,
			Because: "weil home-assistant danach nicht richtig lief – mit #2 im selben Durchgang", Files: []string{"services/beets/values.yml"}},
		{ID: 2, Actor: "ops", Reason: "home-assistant: Erweiterungen installieren", Status: "approved", Outcome: "rolled-back", Batch: run, Restore: 3, Decided: now, Settled: now,
			Because: "weil home-assistant danach nicht richtig lief – mit #1 im selben Durchgang", Files: []string{"services/home-assistant/values.yml"}},
		{ID: 3, Actor: "laib", Reason: "Das Update von Home Assistant lief nicht.", Status: "open", Class: "human", Created: now, Files: []string{"repairs/x.md"}},
		{ID: 4, Actor: "claude", Reason: "Homes: neues Bild", Status: "open", Class: "human", Created: now, Files: []string{"services/whoami/values.yml"}},
	} {
		if err := proposal.Save(dir, p); err != nil {
			t.Fatal(err)
		}
	}
	cause := "Home Assistant hat die Erweiterungen abgelehnt: der Marktplatz-Hinweis ist für Laibs Benutzer nicht bestätigt."
	var out bytes.Buffer
	env.Out = &out
	if code := cmdNote(context.Background(), env, []string{"--kind", "incident", "--karten", "1,3,4", cause}); code != cli.ExitOK {
		t.Fatalf("laib note --karten: exit %d", code)
	}
	if !strings.Contains(out.String(), "auf den Karten #1, #3") {
		t.Errorf("the note says on which cards it is: %q", out.String())
	}
	for id, want := range map[int]string{1: cause, 2: "", 3: cause, 4: ""} {
		if p, _ := proposal.Get(dir, id); p.Ursache != want {
			t.Errorf("#%d: Ursache %q, want %q", id, p.Ursache, want)
		}
	}
	body := html.UnescapeString(inboxMux(env)("GET", "/inbox", nil))
	if strings.Count(body, "Der Operator hat nachgesehen: "+cause) != 2 {
		t.Errorf("the run's card and its follow-up show the cause:\n%s", body)
	}

	out.Reset()
	cmdNote(context.Background(), env, []string{"--kind", "incident", "--karten", "2", "home-assistant-drucker.service failed: exit status 1"})
	if p, _ := proposal.Get(dir, 2); p.Ursache != "" || !strings.Contains(out.String(), "nicht auf den Karten") {
		t.Errorf("a technical text reached a card: %q, %q", p.Ursache, out.String())
	}
}
