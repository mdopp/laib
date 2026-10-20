package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLaib answers laib_inbox, laib_approve, laib_reject, laib_note and
// records every call with the hand's token.
func fakeLaib(t *testing.T, calls *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Name string            `json:"name"`
				Args map[string]string `json:"arguments"`
			} `json:"params"`
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &req)
		*calls = append(*calls, req.Params.Name+" "+req.Params.Args["args"]+" as "+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		text, isErr := "", false
		switch req.Params.Name {
		case "laib_inbox":
			text = `[{"id":17,"actor":"bote","reason":"paperless aus dem Internet erreichbar machen","tier":"apply","status":"open","class":"human","reasons":["macht paperless aus dem Internet erreichbar (docs.box.test)"],"files":["services/paperless/laib.yml"]},
			         {"id":20,"actor":"ops","reason":"whoami auf v1.12","tier":"apply","status":"open","class":"template-update","card":{"summary":"whoami bekommt ein neues Image"}},
			         {"id":21,"actor":"ops","reason":"paperless entfernen","tier":"apply","status":"open","class":"remove","card":{"summary":"Paperless wird entfernt"}}]`
		case "laib_approve":
			if a := req.Params.Args["args"]; a == "17" || a == "20" || a == "21" {
				text = "approved #" + a + ", grün"
			} else {
				text, isErr = "no such proposal", true
			}
		case "laib_reject":
			text = "rejected #" + req.Params.Args["args"]
		case "laib_note":
			text = "notiert"
		default:
			text, isErr = "unknown tool "+req.Params.Name, true
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": isErr}})
	}))
}

type sends struct {
	to, text []string
	closed   []string
}

func (s *sends) Send(ctx context.Context, to, text string) error {
	s.to, s.text = append(s.to, to), append(s.text, text)
	return nil
}

func (s *sends) ClosePoll(ctx context.Context, to string, ts int64) error {
	s.closed = append(s.closed, fmt.Sprintf("%s %d", to, ts))
	return nil
}

func TestOnlyTheAdminsNumberApproves(t *testing.T) {
	var calls []string
	lb := fakeLaib(t, &calls)
	defer lb.Close()
	out := &sends{}
	r := New(Config{Admin: "+4915100000001", LaibURL: lb.URL, Token: "approver-token"}, out)
	ctx := context.Background()

	// a family member says the magic words: nothing happens, the journal knows
	r.Handle(ctx, Message{Platform: "signal", From: "+4915100000002", Text: "gib #17 frei"})
	joined := strings.Join(calls, "\n")
	if strings.Contains(joined, "laib_approve") {
		t.Fatalf("a non-admin number approved:\n%s", joined)
	}
	if !strings.Contains(joined, "laib_note") || !strings.Contains(joined, "unbekannter Nummer") {
		t.Fatalf("the ignored attempt is not journaled:\n%s", joined)
	}
	if len(out.text) != 0 {
		t.Fatalf("the relay answered a non-admin: %v", out.text)
	}

	// the admin: the consequence is shown, then approved through laib with the approver token
	r.Handle(ctx, Message{Platform: "signal", From: "+4915100000001", Text: "gib #17 frei"})
	joined = strings.Join(calls, "\n")
	if !strings.Contains(joined, "laib_approve 17 as approver-token") {
		t.Fatalf("the admin's approval did not reach laib:\n%s", joined)
	}
	if len(out.text) == 0 || !strings.Contains(out.text[len(out.text)-1], "#17") || !strings.Contains(out.text[len(out.text)-1], "aus dem Internet erreichbar") {
		t.Fatalf("the reply does not name the proposal and its consequence: %v", out.text)
	}
	if out.to[len(out.to)-1] != "+4915100000001" {
		t.Fatalf("replied to %s", out.to[len(out.to)-1])
	}

	// other text from the admin is not the relay's business
	n := len(calls)
	r.Handle(ctx, Message{Platform: "signal", From: "+4915100000001", Text: "wie wird das Wetter?"})
	if len(calls) != n {
		t.Fatalf("plain chat reached laib: %v", calls[n:])
	}

	// reject, and an unknown id
	r.Handle(ctx, Message{Platform: "signal", From: "+4915100000001", Text: "lehne #17 ab"})
	if !strings.Contains(strings.Join(calls, "\n"), "laib_reject 17 as approver-token") {
		t.Fatal("reject did not reach laib")
	}
	r.Handle(ctx, Message{Platform: "signal", From: "+4915100000001", Text: "gib #99 frei"})
	if last := out.text[len(out.text)-1]; !strings.Contains(last, "#99") {
		t.Fatalf("an unknown proposal is not explained: %q", last)
	}
}

func TestParseSignalEvent(t *testing.T) {
	ev := `{"jsonrpc":"2.0","method":"receive","params":{"envelope":{"source":"+4915100000001","sourceNumber":"+4915100000001","sourceName":"Martin","timestamp":1791700000000,"dataMessage":{"timestamp":1791700000000,"message":"gib #17 frei","expiresInSeconds":0}},"account":"+4915100000009"}}`
	m, ok := ParseEvent([]byte(ev))
	if !ok || m.Platform != "signal" || m.From != "+4915100000001" || m.Text != "gib #17 frei" {
		t.Fatalf("parsed %+v %v", m, ok)
	}
	if _, ok := ParseEvent([]byte(`{"jsonrpc":"2.0","method":"receive","params":{"envelope":{"source":"+49","receiptMessage":{}}}}`)); ok {
		t.Fatal("a receipt is not a message")
	}
}

// #441: "zeig #N" offers the chat's verdict only for what the chat may
// decide; a proposal only a human may allow, a removal or a destroy names
// the Inbox and its second factor instead.
func TestTheCardSendsWhatOnlyTheInboxDecidesToTheInbox(t *testing.T) {
	inbox := &proposal{ID: 17, Class: "human"}
	inbox.Card.Summary = "paperless aus dem Internet erreichbar"
	if s := cardText(inbox); strings.Contains(s, "gib #17 frei") || !strings.Contains(s, "nur in der Inbox") {
		t.Errorf("a human-class card offers the chat's verdict: %q", s)
	}
	gone := &proposal{ID: 18, Tier: "destroy"}
	if s := cardText(gone); strings.Contains(s, "gib #18 frei") {
		t.Errorf("a destroy card offers the chat's verdict: %q", s)
	}
	update := &proposal{ID: 19, Class: "template-update"}
	if s := cardText(update); !strings.Contains(s, "gib #19 frei") {
		t.Errorf("an update card must offer the chat's verdict: %q", s)
	}
}

// #509: a vote on bote-notify's poll, or a 👍/👎 on it, decides like "gib #N
// frei": only the admin's number, only on a poll of the Bote's that
// umfragen.json knows, once, and never what only the Inbox may decide.
func TestAPollVoteDecidesLikeTheWordsAndOnlyOnce(t *testing.T) {
	var calls []string
	lb := fakeLaib(t, &calls)
	defer lb.Close()
	polls := filepath.Join(t.TempDir(), "umfragen.json")
	os.WriteFile(polls, []byte(`{"1792533068939":{"vorschlag":20},"1792533068950":{"vorschlag":21},"1792533068960":{"vorschlag":20}}`), 0o600)
	out := &sends{}
	const admin, bote = "+4915100000001", "+4915100000009"
	r := New(Config{Admin: admin, Account: bote, Polls: polls, LaibURL: lb.URL, Token: "approver-token"}, out)
	ctx := context.Background()
	vote := func(from string, ts int64, author, choice string) Message {
		return Message{Platform: "signal", From: from, Target: ts, TargetAuthor: author, Choice: choice}
	}
	approved := func() int { return strings.Count(strings.Join(calls, "\n"), "laib_approve") }

	// a family member votes Freigeben: journaled, no effect, no answer
	r.Handle(ctx, vote("+4915100000002", 1792533068939, bote, "frei"))
	if approved() != 0 || len(out.text) != 0 || !strings.Contains(strings.Join(calls, "\n"), "laib_note") || !strings.Contains(strings.Join(calls, "\n"), "unbekannter Nummer") {
		t.Fatalf("a foreign vote: %v / %v", calls, out.text)
	}
	// the admin's 👍 on a message the Bote did not poll, or on someone else's message with a known timestamp: Hermes' business
	n := len(calls)
	r.Handle(ctx, vote(admin, 1792533000000, bote, "frei"))
	r.Handle(ctx, vote(admin, 1792533068939, admin, "frei"))
	if len(calls) != n || len(out.text) != 0 {
		t.Fatalf("a vote on an unknown message reached laib: %v / %v", calls[n:], out.text)
	}
	// Später: nothing decided, a short answer, the poll stays open
	r.Handle(ctx, vote(admin, 1792533068939, bote, "später"))
	if approved() != 0 || len(out.text) != 1 || !strings.Contains(out.text[0], "#20 bleibt offen") || len(out.closed) != 0 {
		t.Fatalf("Später: %v / %v / %v", calls, out.text, out.closed)
	}
	// Freigeben: through laib with the approver token, one line back, the poll closed
	r.Handle(ctx, vote(admin, 1792533068939, bote, "frei"))
	if !strings.Contains(strings.Join(calls, "\n"), "laib_approve 20 as approver-token") {
		t.Fatalf("the admin's vote did not reach laib: %v", calls)
	}
	if last := out.text[len(out.text)-1]; last != "Vorschlag #20 angewendet: whoami bekommt ein neues Image" || out.to[len(out.to)-1] != admin {
		t.Fatalf("the answer: %q to %s", last, out.to[len(out.to)-1])
	}
	if len(out.closed) != 1 || out.closed[0] != admin+" 1792533068939" {
		t.Fatalf("the poll is not closed: %v", out.closed)
	}
	// a second vote or a 👍 on the same poll: journaled, no effect
	answers := len(out.text)
	r.Handle(ctx, vote(admin, 1792533068939, bote, "frei"))
	r.Handle(ctx, vote(admin, 1792533068939, bote, "ab"))
	if approved() != 1 || strings.Contains(strings.Join(calls, "\n"), "laib_reject") || len(out.text) != answers || !strings.Contains(calls[len(calls)-1], "Doppelte Stimme") {
		t.Fatalf("a duplicate vote had an effect: %v / %v", calls, out.text)
	}
	// a removal never gets decided by a vote, even on a poll in the file
	r.Handle(ctx, vote(admin, 1792533068950, bote, "frei"))
	if strings.Contains(strings.Join(calls, "\n"), "laib_approve 21") || !strings.Contains(out.text[len(out.text)-1], "nur in der Inbox") {
		t.Fatalf("a destructive proposal was decided by a vote: %v / %v", calls, out.text)
	}
	// 👎 rejects
	r.Handle(ctx, vote(admin, 1792533068960, bote, "ab"))
	if !strings.Contains(strings.Join(calls, "\n"), "laib_reject 20 as approver-token") || out.text[len(out.text)-1] != "Vorschlag #20 abgelehnt." {
		t.Fatalf("👎 did not reject: %v / %v", calls, out.text)
	}
}

// signal-cli 0.14's events (#509, as seen on the Kraftwerk 2026-10-20).
func TestParsePollVoteAndReaction(t *testing.T) {
	env := func(dm string) []byte {
		return []byte(`{"jsonrpc":"2.0","method":"receive","params":{"envelope":{"source":"+4915100000001","sourceNumber":"+4915100000001","timestamp":1792533099000,"dataMessage":` + dm + `},"account":"+4915100000009"}}`)
	}
	for _, c := range []struct {
		dm, choice string
		ok         bool
	}{
		{`{"timestamp":1,"pollVote":{"author":"+4915100000009","authorNumber":"+4915100000009","targetSentTimestamp":1792533068939,"optionIndexes":[0],"voteCount":1}}`, "frei", true},
		{`{"timestamp":1,"pollVote":{"authorNumber":"+4915100000009","targetSentTimestamp":1792533068939,"optionIndexes":[2],"voteCount":2}}`, "später", true},
		{`{"timestamp":1,"pollVote":{"authorNumber":"+4915100000009","targetSentTimestamp":1792533068939,"optionIndexes":[],"voteCount":3}}`, "", false},
		{`{"timestamp":1,"reaction":{"emoji":"👍","targetAuthorNumber":"+4915100000009","targetSentTimestamp":1792533068939,"isRemove":false}}`, "frei", true},
		{`{"timestamp":1,"reaction":{"emoji":"👎🏽","targetAuthorNumber":"+4915100000009","targetSentTimestamp":1792533068939,"isRemove":false}}`, "ab", true},
		{`{"timestamp":1,"reaction":{"emoji":"👍","targetAuthorNumber":"+4915100000009","targetSentTimestamp":1792533068939,"isRemove":true}}`, "", false},
		{`{"timestamp":1,"reaction":{"emoji":"❤️","targetAuthorNumber":"+4915100000009","targetSentTimestamp":1792533068939,"isRemove":false}}`, "", false},
	} {
		m, ok := ParseEvent(env(c.dm))
		if ok != c.ok || ok && (m.Choice != c.choice || m.Target != 1792533068939 || m.TargetAuthor != "+4915100000009" || m.From != "+4915100000001" || m.Text != "") {
			t.Errorf("%s: %+v %v", c.dm, m, ok)
		}
	}
}

// #509: the poll is closed with signal-cli's sendPollTerminate, to the admin, by its timestamp.
func TestClosePollIsSendPollTerminate(t *testing.T) {
	var got map[string]any
	d := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"timestamp":1792533099999}}`))
	}))
	defer d.Close()
	out := Senders{Signal: &SignalSender{URL: d.URL, Account: "+4915100000009"}}
	if err := out.ClosePoll(context.Background(), "+4915100000001", 1792533068939); err != nil {
		t.Fatal(err)
	}
	p, _ := got["params"].(map[string]any)
	if got["method"] != "sendPollTerminate" || p["account"] != "+4915100000009" || fmt.Sprint(p["recipient"]) != "[+4915100000001]" || p["pollTimestamp"] != float64(1792533068939) {
		t.Fatalf("sent %v", got)
	}
}
