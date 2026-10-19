package relay

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

// fakeDiscord is Discord's REST API as the relay uses it: the bot's own id,
// the DM channel, its messages (newest first, `after` honoured) and sends.
type fakeDiscord struct {
	mu        sync.Mutex
	chType    int
	recipient string
	msgs      []map[string]any // oldest first
	sent      []string
	auth      []string
}

func (f *fakeDiscord) post(id, author string, bot bool, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, map[string]any{"id": id, "content": text, "author": map[string]any{"id": author, "bot": bot}})
}

func (f *fakeDiscord) server(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		switch {
		case r.URL.Path == "/users/@me":
			json.NewEncoder(w).Encode(map[string]string{"id": "900"})
		case r.URL.Path == "/channels/555" && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"type": f.chType, "recipients": []map[string]string{{"id": f.recipient}}})
		case r.URL.Path == "/channels/555/messages" && r.Method == "GET":
			after, out := r.URL.Query().Get("after"), []map[string]any{}
			for i := len(f.msgs) - 1; i >= 0; i-- { // newest first, as Discord answers
				if id := f.msgs[i]["id"].(string); after == "" || newer(id, after) {
					out = append(out, f.msgs[i])
				}
				if r.URL.Query().Get("limit") == "1" && len(out) == 1 {
					break
				}
			}
			json.NewEncoder(w).Encode(out)
		case r.URL.Path == "/channels/555/messages" && r.Method == "POST":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			f.sent = append(f.sent, b["content"])
			w.Write([]byte("{}"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// #463: the owner's verdict on Discord counts when the relay itself reads it
// in the owner's DM with the bot and Discord names the owner as its author;
// history from before the start, the bot's own messages, another author or
// another channel never decide.
func TestDiscordVerdictsOnlyFromTheOwnerInTheirDM(t *testing.T) {
	var calls []string
	lb := fakeLaib(t, &calls)
	defer lb.Close()
	f := &fakeDiscord{chType: 1, recipient: "111"}
	srv := f.server(t)
	d := &Discord{API: srv.URL, Token: "bot-tok", Channel: "555", Every: 20 * time.Millisecond}
	r := New(Config{AdminDiscord: "111", DiscordChannel: "555", LaibURL: lb.URL, Token: "approver-token"}, Senders{Discord: d})
	if err := d.Check(context.Background(), "111"); err != nil {
		t.Fatal(err)
	}
	f.post("1000", "111", false, "gib #17 frei") // before the start: history
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.RunDiscord(ctx, d); close(done) }()
	time.Sleep(60 * time.Millisecond)
	if strings.Contains(strings.Join(calls, "\n"), "laib_approve") {
		t.Fatalf("a verdict from before the start was acted on:\n%s", strings.Join(calls, "\n"))
	}
	f.post("1001", "900", true, "gib #17 frei") // the bot itself
	f.post("1002", "222", false, "gib #17 frei") // someone else
	f.post("1003", "111", false, "gib #17 frei") // the owner
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	j := strings.Join(calls, "\n")
	if strings.Count(j, "laib_approve 17 as approver-token") != 1 {
		t.Fatalf("the owner's verdict must approve once, and only it:\n%s", j)
	}
	if !strings.Contains(j, "laib_note --kind watch") {
		t.Errorf("another author's verdict is not journaled:\n%s", j)
	}
	if len(f.sent) != 1 || !strings.Contains(f.sent[0], "#17") {
		t.Fatalf("the answer goes to the owner's DM: %v", f.sent)
	}
	for _, a := range f.auth {
		if a != "Bot bot-tok" {
			t.Fatalf("a Discord call without the bot token: %q", a)
		}
	}
	// the same words with the owner's id, in another channel, never decide
	r.Handle(context.Background(), Message{Platform: "discord", From: "111", Room: "666", Text: "gib #17 frei"})
	if strings.Count(strings.Join(calls, "\n"), "laib_approve") != 1 {
		t.Fatal("a verdict outside the owner's DM decided")
	}
}

// Check refuses a channel the relay cannot trust: not a direct chat, or
// one with somebody else, and an owner who is the bot itself.
func TestDiscordCheckRefusesAChannelItCannotTrust(t *testing.T) {
	for what, f := range map[string]*fakeDiscord{
		"a group DM":         {chType: 3, recipient: "111"},
		"someone else's DM":  {chType: 1, recipient: "222"},
		"a server's channel": {chType: 0, recipient: "111"},
	} {
		d := &Discord{API: f.server(t).URL, Token: "bot-tok", Channel: "555"}
		if err := d.Check(context.Background(), "111"); err == nil {
			t.Errorf("%s passed the check", what)
		}
	}
	f := &fakeDiscord{chType: 1, recipient: "900"}
	if err := (&Discord{API: f.server(t).URL, Token: "bot-tok", Channel: "555"}).Check(context.Background(), "900"); err == nil {
		t.Error("the bot as its own owner passed the check")
	}
	if err := (&Discord{Channel: "555"}).Check(context.Background(), "111"); err == nil || !strings.Contains(err.Error(), "BOTE_DISCORD_TOKEN") {
		t.Errorf("no token must say which secret: %v", err)
	}
}
