package notify

import (
	"context"
	"io"
	"mime"
	"net/mail"
	"strings"
	"testing"
	"time"
)

// #778 (owner, 2026-10-26, „B3“): approvals happen only in the Inbox, and a
// new proposal reaches the owner as one short line: „Freigabe, Nummer,
// Text, Link“. No impact lines, no decision words, no poll; a destructive
// one reads the same, since the chat decides nothing any more.
func TestANewProposalIsOneLineWithTheInboxLink(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	c := chatConfig(t, f, b, "127.0.0.1:1")
	f.inbox = `[{"id":17,"actor":"ops","reason":"whoami auf v1.12","tier":"apply","class":"template-update","card":{"summary":"whoami bekommt ein neues Image","impact":["kurzer Neustart"],"reversible":"laib rollback"}},
	            {"id":18,"actor":"ops","reason":"paperless entfernen","tier":"apply","class":"remove","card":{"summary":"Paperless wird entfernt","impact":["Dokumente bleiben im Backup"]}},
	            {"id":19,"actor":"ops","reason":"Daten löschen","tier":"destroy","class":"human","card":{}}]`
	now := time.Now()
	if err := Sweep(context.Background(), c, now); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"Freigabe #17: whoami bekommt ein neues Image · https://laib.e2e.home/inbox": true,
		"Freigabe #18: Paperless wird entfernt · https://laib.e2e.home/inbox":        true,
		"Freigabe #19: Daten löschen · https://laib.e2e.home/inbox":                  true, // no headline: the reason
	}
	got := b.messages()
	if len(got) != 3 {
		t.Fatalf("three open proposals, three messages: %v", got)
	}
	for _, m := range got {
		if !want[m["text"]] || len(m) != 2 || m["an"] != "martin" {
			t.Errorf("a proposal is one line and nothing else (no poll): %q", m)
		}
		delete(want, m["text"])
	}
	Sweep(context.Background(), c, now.Add(5*time.Minute))
	if n := len(b.messages()); n != 3 {
		t.Fatalf("a proposal is announced once, not every sweep: %d", n)
	}
	if f.refusals != 0 {
		t.Errorf("the notifier called a tool outside its hand")
	}
}

// #778: without a reachable chat the mail carries the same line, as its
// subject and as its body, and nothing else.
func TestTheMailFallbackIsTheSameLine(t *testing.T) {
	f := newFakeLaib(t)
	g := newGmailSMTP(t)
	c := f.config(t, g.addr)
	c.InboxURL = "https://laib.e2e.home/inbox"
	f.inbox = `[{"id":123,"reason":"x","tier":"apply","class":"human","card":{"summary":"Joshua kennt jetzt alle Namen deiner Geräte","impact":["a","b"],"reversible":"laib rollback"}}]`
	if err := Sweep(context.Background(), c, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, mails := g.got()
	if len(mails) != 1 {
		t.Fatalf("one mail: %q", mails)
	}
	m, err := mail.ReadMessage(strings.NewReader(mails[0]))
	if err != nil {
		t.Fatal(err)
	}
	line := "Freigabe #123: Joshua kennt jetzt alle Namen deiner Geräte · https://laib.e2e.home/inbox"
	if subj, err := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject")); err != nil || subj != line {
		t.Errorf("the subject is the line: %q %v", subj, err)
	}
	if body, _ := io.ReadAll(m.Body); strings.TrimRight(string(body), "\r\n") != line {
		t.Errorf("the body is the line, nothing else: %q", body)
	}
}
