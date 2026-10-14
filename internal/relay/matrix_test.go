package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// #184, ADR 0016 §3: on Matrix only the admin's MXID in the one trusted room
// decides; another MXID or room is refused and journaled; Telegram never
// approves, not even from the admin's own number.
func TestMatrixApprovalOnlyFromTheAdminsMXIDInItsRoom(t *testing.T) {
	var calls []string
	lb := fakeLaib(t, &calls)
	defer lb.Close()
	out := &sends{}
	r := New(Config{Admin: "+4915100000001", AdminMXID: "@admin:hs.test", Room: "!raum:hs.test", LaibURL: lb.URL, Token: "approver-token"}, out)
	ctx := context.Background()
	for _, m := range []Message{
		{Platform: "matrix", From: "@mallory:hs.test", Room: "!raum:hs.test", Text: "gib #17 frei"},
		{Platform: "matrix", From: "@admin:hs.test", Room: "!anders:hs.test", Text: "gib #17 frei"},
		{Platform: "matrix", From: "@admin:hs.test", Text: "gib #17 frei"},
		{Platform: "telegram", From: "+4915100000001", Text: "gib #17 frei"},
		{Platform: "telegram", From: "@admin:hs.test", Room: "!raum:hs.test", Text: "gib #17 frei"},
		{From: "+4915100000001", Text: "gib #17 frei"}, // no platform: never
		{Platform: "matrix", From: "@mallory:hs.test", Room: "!raum:hs.test", Text: "zeig #17"},
	} {
		r.Handle(ctx, m)
		if j := strings.Join(calls, "\n"); strings.Contains(j, "laib_approve") {
			t.Fatalf("%+v approved:\n%s", m, j)
		}
	}
	if len(out.text) != 0 {
		t.Fatalf("the relay answered someone who is not the admin: %v", out.text)
	}
	if n := strings.Count(strings.Join(calls, "\n"), "laib_note"); n != 6 {
		t.Fatalf("every refused verdict is journaled, got %d notes:\n%s", n, strings.Join(calls, "\n"))
	}
	r.Handle(ctx, Message{Platform: "matrix", From: "@admin:hs.test", Room: "!raum:hs.test", Text: "gib #17 frei"})
	if !strings.Contains(strings.Join(calls, "\n"), "laib_approve 17 as approver-token") {
		t.Fatalf("the admin's MXID in its room did not approve:\n%s", strings.Join(calls, "\n"))
	}
	if len(out.to) != 1 || out.to[0] != "!raum:hs.test" || !strings.Contains(out.text[0], "#17") {
		t.Fatalf("the answer goes to the admin's room: %v %v", out.to, out.text)
	}
}

// hs is a fake homeserver: whoami, the room's encryption and members,
// /messages from a token, and sends.
type hs struct {
	mu        sync.Mutex
	encrypted bool
	srvAdmin  bool // the Bote's account is a homeserver admin (Synapse admin API answers)
	noEnd     bool // pages without `end`: the same events again on every poll
	members   []string
	events    []string // returned once, after the head token
	sent      []string
	srv       *httptest.Server
}

func newHS(t *testing.T) *hs {
	h := &hs{members: []string{"@bote:hs.test", "@admin:hs.test"}}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer matrix-tok" {
			w.WriteHeader(401)
			io.WriteString(w, `{"errcode":"M_UNKNOWN_TOKEN"}`)
			return
		}
		p := r.URL.Path
		switch {
		case p == "/_matrix/client/v3/account/whoami":
			io.WriteString(w, `{"user_id":"@bote:hs.test","device_id":"BOTE"}`)
		case strings.HasPrefix(p, "/_synapse/admin/"):
			if !h.srvAdmin {
				w.WriteHeader(403)
				io.WriteString(w, `{"errcode":"M_FORBIDDEN","error":"You are not a server admin"}`)
				return
			}
			io.WriteString(w, `{"name":"@bote:hs.test","admin":true}`)
		case !strings.HasPrefix(p, "/_matrix/client/v3/rooms/!raum:hs.test/"):
			w.WriteHeader(403)
			io.WriteString(w, `{"errcode":"M_FORBIDDEN"}`)
		case strings.HasSuffix(p, "/state/m.room.encryption/"):
			if !h.encrypted {
				w.WriteHeader(404)
				io.WriteString(w, `{"errcode":"M_NOT_FOUND"}`)
				return
			}
			io.WriteString(w, `{"algorithm":"m.megolm.v1.aes-sha2"}`)
		case strings.HasSuffix(p, "/joined_members"):
			joined := map[string]any{}
			for _, m := range h.members {
				joined[m] = map[string]string{}
			}
			json.NewEncoder(w).Encode(map[string]any{"joined": joined})
		case strings.HasSuffix(p, "/messages"):
			q := r.URL.Query()
			switch {
			case q.Get("dir") == "b":
				io.WriteString(w, `{"chunk":[],"start":"s0","end":"s0"}`)
			case q.Get("from") == "s0" && h.noEnd:
				io.WriteString(w, `{"chunk":[`+strings.Join(h.events, ",")+`],"start":"s0"}`)
			case q.Get("from") == "s0":
				io.WriteString(w, `{"chunk":[`+strings.Join(h.events, ",")+`],"start":"s0","end":"s1"}`)
			default:
				io.WriteString(w, `{"chunk":[],"start":"s1"}`)
			}
		case r.Method == http.MethodPut && strings.Contains(p, "/send/m.room.message/"):
			b, _ := io.ReadAll(r.Body)
			h.sent = append(h.sent, string(b))
			io.WriteString(w, `{"event_id":"$sent"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(h.srv.Close)
	return h
}

// #184, ADR 0016 §3: one homeserver is trusted, the Bote's own, and the
// admin's MXID must live there and not be the Bote; the Bote's account is no
// server admin; the room is a room id, unencrypted (the relay cannot read
// Megolm) and holds exactly the admin and the Bote. Each rule refuses with
// its own reason.
func TestMatrixTrustsOneHomeserverAndAPrivateRoom(t *testing.T) {
	h := newHS(t)
	ctx := context.Background()
	x := &Matrix{URL: h.srv.URL, Token: "matrix-tok", Room: "!raum:hs.test"}
	if err := x.Check(ctx, "@admin:hs.test"); err != nil {
		t.Fatalf("the admin on the Bote's homeserver in a private unencrypted room: %v", err)
	}
	refused := func(what string, x *Matrix, admin, reason string) {
		t.Helper()
		if err := x.Check(ctx, admin); err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("%s: want a refusal naming %q, got %v", what, reason, err)
		}
	}
	set := func(f func()) { h.mu.Lock(); f(); h.mu.Unlock() }
	refused("an admin on another homeserver", x, "@admin:other.test", "homeserver")
	refused("the admin is the Bote", x, "@bote:hs.test", "the Bote itself")
	refused("a room alias instead of a room id", &Matrix{URL: h.srv.URL, Token: "matrix-tok", Room: "#raum:hs.test"}, "@admin:hs.test", "room id")
	refused("a wrong token", &Matrix{URL: h.srv.URL, Token: "nope", Room: "!raum:hs.test"}, "@admin:hs.test", "whoami")
	refused("no token", &Matrix{URL: h.srv.URL, Room: "!raum:hs.test"}, "@admin:hs.test", "BOTE_MATRIX_TOKEN")
	refused("an unset secret's placeholder (#185)", &Matrix{URL: h.srv.URL, Token: "${secret:BOTE_MATRIX_TOKEN}", Room: "!raum:hs.test"}, "@admin:hs.test", "BOTE_MATRIX_TOKEN")
	refused("a room the Bote is not in", &Matrix{URL: h.srv.URL, Token: "matrix-tok", Room: "!fremd:hs.test"}, "@admin:hs.test", "is the Bote in it")
	set(func() { h.members = append(h.members, "@eve:hs.test") })
	refused("a third member in the room", x, "@admin:hs.test", "@eve:hs.test")
	set(func() { h.members = []string{"@bote:hs.test"} })
	refused("the admin not in the room", x, "@admin:hs.test", "not in room")
	set(func() { h.members, h.encrypted = []string{"@bote:hs.test", "@admin:hs.test"}, true })
	refused("an encrypted room", x, "@admin:hs.test", "encrypted")
	set(func() { h.encrypted, h.srvAdmin = false, true })
	refused("a Bote account that is a server admin", x, "@admin:hs.test", "server admin")
}

func (h *hs) sentCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sent)
}

func ev(sender, body string) string {
	b, _ := json.Marshal(map[string]any{"type": "m.room.message", "sender": sender, "room_id": "!raum:hs.test", "event_id": "$" + sender + body,
		"content": map[string]string{"msgtype": "m.text", "body": body}})
	return string(b)
}

// #184: the relay reads the trusted room from the Bote's homeserver with
// /messages (no sync: Hermes' device keeps its own), decides on the
// homeserver-asserted sender and answers in the room.
func TestMatrixRelayDecidesFromThePolledRoom(t *testing.T) {
	var calls []string
	lb := fakeLaib(t, &calls)
	defer lb.Close()
	h := newHS(t)
	h.events = []string{ev("@mallory:hs.test", "gib #17 frei"), ev("@admin:hs.test", "gib #17 frei")}
	x := &Matrix{URL: h.srv.URL, Token: "matrix-tok", Room: "!raum:hs.test", Every: 10 * time.Millisecond}
	if err := x.Check(context.Background(), "@admin:hs.test"); err != nil {
		t.Fatal(err)
	}
	r := New(Config{AdminMXID: "@admin:hs.test", Room: "!raum:hs.test", LaibURL: lb.URL, Token: "approver-token"}, Senders{Matrix: x})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.RunMatrix(ctx, x); close(done) }()
	for end := time.Now().Add(3 * time.Second); h.sentCount() == 0 && time.Now().Before(end); {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	j := strings.Join(calls, "\n")
	if strings.Count(j, "laib_approve 17 as approver-token") != 1 || !strings.Contains(j, "laib_note") {
		t.Fatalf("one approval from the admin, one journaled refusal:\n%s", j)
	}
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "#17") || !strings.Contains(h.sent[0], `"msgtype":"m.text"`) {
		t.Fatalf("the answer in the room: %v", h.sent)
	}
}

// #184: a room that turns encrypted or gains a member stops Matrix
// approvals: the relay refuses instead of guessing, and the journal says so.
func TestMatrixRelayStopsWhenTheRoomChanges(t *testing.T) {
	for name, change := range map[string]string{
		"encrypted": `{"type":"m.room.encryption","sender":"@admin:hs.test","room_id":"!raum:hs.test","state_key":"","content":{"algorithm":"m.megolm.v1.aes-sha2"}}`,
		"a member":  `{"type":"m.room.member","sender":"@admin:hs.test","room_id":"!raum:hs.test","state_key":"@eve:hs.test","content":{"membership":"join"}}`,
	} {
		var calls []string
		lb := fakeLaib(t, &calls)
		h := newHS(t)
		h.events = []string{change, ev("@admin:hs.test", "gib #17 frei")}
		x := &Matrix{URL: h.srv.URL, Token: "matrix-tok", Room: "!raum:hs.test", Every: 10 * time.Millisecond}
		if err := x.Check(context.Background(), "@admin:hs.test"); err != nil {
			t.Fatal(err)
		}
		r := New(Config{AdminMXID: "@admin:hs.test", Room: "!raum:hs.test", LaibURL: lb.URL, Token: "approver-token"}, Senders{Matrix: x})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		r.RunMatrix(ctx, x) // returns on the change
		if ctx.Err() != nil {
			t.Errorf("%s: the relay kept reading the room", name)
		}
		cancel()
		lb.Close()
		j := strings.Join(calls, "\n")
		if strings.Contains(j, "laib_approve") || !strings.Contains(j, "laib_note") || !strings.Contains(j, "Matrix") {
			t.Errorf("%s: no approval after the change, and a journal line naming Matrix:\n%s", name, j)
		}
	}
}

// Only a plain text message is a verdict: notices (bots), edits and
// encrypted events are not.
func TestParseMatrixEvent(t *testing.T) {
	m, ok := ParseMatrix([]byte(ev("@admin:hs.test", "gib #17 frei")))
	if !ok || m.Platform != "matrix" || m.From != "@admin:hs.test" || m.Room != "!raum:hs.test" || m.Text != "gib #17 frei" {
		t.Fatalf("parsed %+v %v", m, ok)
	}
	for _, e := range []string{
		`{"type":"m.room.message","sender":"@admin:hs.test","room_id":"!raum:hs.test","content":{"msgtype":"m.notice","body":"gib #17 frei"}}`,
		`{"type":"m.room.message","sender":"@admin:hs.test","room_id":"!raum:hs.test","content":{"msgtype":"m.text","body":"* gib #17 frei","m.new_content":{"msgtype":"m.text","body":"gib #17 frei"},"m.relates_to":{"rel_type":"m.replace","event_id":"$a"}}}`,
		`{"type":"m.room.encrypted","sender":"@admin:hs.test","room_id":"!raum:hs.test","content":{"algorithm":"m.megolm.v1.aes-sha2","ciphertext":"x"}}`,
		`{"type":"m.room.message","room_id":"!raum:hs.test","content":{"msgtype":"m.text","body":"gib #17 frei"}}`,
	} {
		if m, ok := ParseMatrix([]byte(e)); ok {
			t.Errorf("not a verdict: %s → %+v", e, m)
		}
	}
}

// #184 review: sender and identity edge cases. The compare is exact (a
// homeserver folds nothing for us), Matrix never takes the Signal number,
// and an empty admin never matches an empty sender.
func TestOnlyExactIdentitiesDecide(t *testing.T) {
	var calls []string
	lb := fakeLaib(t, &calls)
	defer lb.Close()
	ctx := context.Background()
	for what, c := range map[string]struct {
		cfg Config
		m   Message
	}{
		"A6 case":         {Config{AdminMXID: "@admin:hs.test", Room: "!raum:hs.test"}, Message{Platform: "matrix", From: "@Admin:hs.test", Room: "!raum:hs.test"}},
		"A9 signal no.":   {Config{Admin: "+4915100000001", AdminMXID: "@admin:hs.test", Room: "!raum:hs.test"}, Message{Platform: "matrix", From: "+4915100000001", Room: "!raum:hs.test"}},
		"A1 empty signal": {Config{AdminMXID: "@admin:hs.test", Room: "!raum:hs.test"}, Message{Platform: "signal"}},
		"A3 empty matrix": {Config{Admin: "+4915100000001"}, Message{Platform: "matrix"}},
	} {
		c.cfg.LaibURL, c.cfg.Token = lb.URL, "approver-token"
		c.m.Text = "gib #17 frei"
		New(c.cfg, &sends{}).Handle(ctx, c.m)
		if j := strings.Join(calls, "\n"); strings.Contains(j, "laib_approve") {
			t.Fatalf("%s approved:\n%s", what, j)
		}
	}
}

// P3: only m.room.message is a message; another type with a text body is not.
func TestParseMatrixOnlyMessages(t *testing.T) {
	for _, typ := range []string{"m.reaction", "m.sticker", "m.room.message.feedback", "m.room.topic"} {
		e := `{"type":"` + typ + `","sender":"@admin:hs.test","room_id":"!raum:hs.test","content":{"msgtype":"m.text","body":"gib #17 frei"}}`
		if m, ok := ParseMatrix([]byte(e)); ok {
			t.Errorf("%s is no verdict: %+v", typ, m)
		}
	}
}

// pollRelay runs the relay on h's room until it answered or 2 s passed.
func pollRelay(t *testing.T, h *hs, calls *[]string, until func() bool) {
	t.Helper()
	lb := fakeLaib(t, calls)
	t.Cleanup(lb.Close)
	x := &Matrix{URL: h.srv.URL, Token: "matrix-tok", Room: "!raum:hs.test", Every: 10 * time.Millisecond}
	if err := x.Check(context.Background(), "@admin:hs.test"); err != nil {
		t.Fatal(err)
	}
	r := New(Config{AdminMXID: "@admin:hs.test", Room: "!raum:hs.test", LaibURL: lb.URL, Token: "approver-token"}, Senders{Matrix: x})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { r.RunMatrix(ctx, x); close(done) }()
	for ctx.Err() == nil && !until() {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

// R1: the Bote's own words never reach the relay's decision or the journal.
func TestTheBotesOwnMessagesAreIgnored(t *testing.T) {
	var calls []string
	h := newHS(t)
	h.events = []string{ev("@bote:hs.test", "gib #17 frei"), ev("@admin:hs.test", "zeig #17")}
	pollRelay(t, h, &calls, func() bool { return h.sentCount() > 0 })
	if j := strings.Join(calls, "\n"); strings.Contains(j, "laib_approve") || strings.Contains(j, "laib_note") {
		t.Fatalf("the Bote's own message was handled:\n%s", j)
	}
}

// #184 review: a page without `end` returns the same events again; an event
// is handled once, by its event_id.
func TestMatrixEventsAreHandledOnce(t *testing.T) {
	var calls []string
	h := newHS(t)
	h.noEnd = true
	h.events = []string{ev("@admin:hs.test", "gib #17 frei")}
	pollRelay(t, h, &calls, func() bool { return false }) // the whole window: every poll replays the page
	if n := strings.Count(strings.Join(calls, "\n"), "laib_approve"); n != 1 {
		t.Fatalf("one event, %d approvals", n)
	}
}

// B2: a Matrix check that fails (homeserver down, token revoked, room
// changed) does not stop the relay: it journals why, keeps trying, and
// Matrix approvals start once the check passes.
func TestAFailedMatrixCheckKeepsTheRelay(t *testing.T) {
	var calls []string
	lb := fakeLaib(t, &calls)
	defer lb.Close()
	h := newHS(t)
	h.encrypted = true
	h.events = []string{ev("@admin:hs.test", "gib #17 frei")}
	x := &Matrix{URL: h.srv.URL, Token: "matrix-tok", Room: "!raum:hs.test", Every: 10 * time.Millisecond, Retry: 20 * time.Millisecond}
	r := New(Config{AdminMXID: "@admin:hs.test", Room: "!raum:hs.test", LaibURL: lb.URL, Token: "approver-token"}, Senders{Matrix: x})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { r.WatchMatrix(ctx, x, "@admin:hs.test"); close(done) }()
	time.Sleep(100 * time.Millisecond) // several failed checks
	h.mu.Lock()
	h.encrypted = false
	h.mu.Unlock()
	for ctx.Err() == nil && h.sentCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	j := strings.Join(calls, "\n")
	if strings.Count(j, "laib_note") != 1 || !strings.Contains(j, "encrypted") {
		t.Fatalf("one journal line for the refused check, not one per retry:\n%s", j)
	}
	if !strings.Contains(j, "laib_approve 17") {
		t.Fatalf("Matrix approvals start once the check passes:\n%s", j)
	}
}
