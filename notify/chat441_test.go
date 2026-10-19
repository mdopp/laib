package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBote is bote-notify's POST /mitteilung (templates/bote/notify.py):
// it keeps each message, checks the bearer, and fails while down.
type fakeBote struct {
	mu   sync.Mutex
	down bool
	got  []map[string]string
	srv  *httptest.Server
}

func newFakeBote(t *testing.T) *fakeBote {
	b := &fakeBote{}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if r.URL.Path != "/mitteilung" || r.Header.Get("Authorization") != "Bearer bote-n" {
			http.Error(w, "nein", http.StatusUnauthorized)
			return
		}
		if b.down {
			http.Error(w, `{"ok": false, "grund": "kein Kanal erreichbar"}`, http.StatusBadGateway)
			return
		}
		var m map[string]string
		json.NewDecoder(r.Body).Decode(&m)
		b.got = append(b.got, m)
		w.Write([]byte(`{"ok": true, "kanal": "discord"}`))
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *fakeBote) messages() []map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]map[string]string(nil), b.got...)
}

func chatConfig(t *testing.T, f *fakeLaib, b *fakeBote, smtpAddr string) Config {
	c := f.config(t, smtpAddr)
	c.BoteURL, c.BoteToken, c.Person, c.InboxURL = b.srv.URL, "bote-n", "martin", "https://laib.e2e.home/inbox"
	return c
}

// #441 (owner, 2026-10-19): Laib tells the owner through the Bote's
// channels first; mail only when no channel is reachable.
func TestTheOwnerHearsInTheChatFirstAndByMailOnlyAsFallback(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	addr, mails := fakeSMTP(t)
	c := chatConfig(t, f, b, addr)
	f.red = true
	now := time.Now()
	Sweep(context.Background(), c, now)
	if err := Sweep(context.Background(), c, now.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := b.messages()
	if len(got) != 1 || got[0]["an"] != "martin" || !strings.Contains(got[0]["text"], "seit 10 Minuten rot") || !strings.Contains(got[0]["text"], "whoami whoami.service ist inactive") {
		t.Fatalf("the incident goes to the owner in the chat: %v", got)
	}
	if n := len(mails()); n != 0 {
		t.Fatalf("a message the chat delivered is mailed too: %d", n)
	}
	// the chat down: the next incident goes by mail
	b.down = true
	f.red = false
	Sweep(context.Background(), c, now.Add(12*time.Minute))
	f.red = true
	Sweep(context.Background(), c, now.Add(13*time.Minute))
	if err := Sweep(context.Background(), c, now.Add(24*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if m := mails(); len(m) != 1 || !strings.Contains(m[0], "Auf der Box ist seit mindestens zehn Minuten rot") {
		t.Fatalf("with no channel reachable the incident is mailed: %q", m)
	}
}

// Without mail settings the chat alone is enough to tell the owner.
func TestTheChatAloneIsEnough(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	c := chatConfig(t, f, b, "127.0.0.1:1")
	c.SMTPHost, c.MailTo = "", ""
	f.updates = `[{"service":"whoami","image":"docker.io/traefik/whoami","current":"v1.11","minor":"v1.12"}]`
	if err := Sweep(context.Background(), c, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := b.messages(); len(got) != 1 || !strings.Contains(got[0]["text"], "- whoami: docker.io/traefik/whoami:v1.11 → v1.12") {
		t.Fatalf("the weekly updates go to the chat: %v", got)
	}
}

// #441: an open proposal reaches the owner once. A non-destructive one says
// how to decide it in the chat; a destructive one (tier destroy, a removal)
// only links the Inbox, where Authelia asks for the second factor: the chat
// never approves it.
func TestProposalsReachTheChatOnceAndDestructiveOnesOnlyLinkTheInbox(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	c := chatConfig(t, f, b, "127.0.0.1:1")
	f.inbox = `[{"id":17,"actor":"ops","reason":"whoami auf v1.12","tier":"apply","class":"template-update","card":{"summary":"whoami bekommt ein neues Image","impact":["kurzer Neustart"],"reversible":"laib rollback"}},
	            {"id":18,"actor":"ops","reason":"paperless entfernen","tier":"apply","class":"remove","card":{"summary":"Paperless wird entfernt","impact":["Dokumente bleiben im Backup"]}},
	            {"id":19,"actor":"ops","reason":"Daten löschen","tier":"destroy","class":"human","card":{"summary":"Die alten Fotos werden gelöscht"}}]`
	now := time.Now()
	if err := Sweep(context.Background(), c, now); err != nil {
		t.Fatal(err)
	}
	got := b.messages()
	if len(got) != 3 {
		t.Fatalf("three open proposals, three messages: %v", got)
	}
	byID := map[string]string{}
	for _, m := range got {
		for _, id := range []string{"#17", "#18", "#19"} {
			if strings.Contains(m["text"], "Vorschlag "+id) {
				byID[id] = m["text"]
			}
		}
	}
	if s := byID["#17"]; !strings.Contains(s, "whoami bekommt ein neues Image") || !strings.Contains(s, "gib #17 frei") || !strings.Contains(s, "lehne #17 ab") {
		t.Errorf("a non-destructive proposal says how to decide it in the chat: %q", s)
	}
	for _, id := range []string{"#18", "#19"} {
		s := byID[id]
		if !strings.Contains(s, "https://laib.e2e.home/inbox") || !strings.Contains(s, "zweiten Faktor") || strings.Contains(s, "gib "+id+" frei") {
			t.Errorf("a destructive proposal only links the Inbox: %q", s)
		}
	}
	Sweep(context.Background(), c, now.Add(5*time.Minute))
	if n := len(b.messages()); n != 3 {
		t.Fatalf("a proposal is announced once, not every sweep: %d", n)
	}
	if f.refusals != 0 {
		t.Errorf("the notifier called a tool outside its hand")
	}
}
