package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
			text = `[{"id":17,"actor":"bote","reason":"paperless aus dem Internet erreichbar machen","tier":"apply","status":"open","class":"human","reasons":["macht paperless aus dem Internet erreichbar (docs.box.test)"],"files":["services/paperless/laib.yml"]}]`
		case "laib_approve":
			if req.Params.Args["args"] == "17" {
				text = "approved #17, grün"
			} else {
				text, isErr = "no such proposal", true
			}
		case "laib_reject":
			text = "rejected #17"
		case "laib_note":
			text = "notiert"
		default:
			text, isErr = "unknown tool "+req.Params.Name, true
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": isErr}})
	}))
}

type sends struct{ to, text []string }

func (s *sends) Send(ctx context.Context, to, text string) error {
	s.to, s.text = append(s.to, to), append(s.text, text)
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
	r.Handle(ctx, Message{From: "+4915100000002", Text: "gib #17 frei"})
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
	r.Handle(ctx, Message{From: "+4915100000001", Text: "gib #17 frei"})
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
	r.Handle(ctx, Message{From: "+4915100000001", Text: "wie wird das Wetter?"})
	if len(calls) != n {
		t.Fatalf("plain chat reached laib: %v", calls[n:])
	}

	// reject, and an unknown id
	r.Handle(ctx, Message{From: "+4915100000001", Text: "lehne #17 ab"})
	if !strings.Contains(strings.Join(calls, "\n"), "laib_reject 17 as approver-token") {
		t.Fatal("reject did not reach laib")
	}
	r.Handle(ctx, Message{From: "+4915100000001", Text: "gib #99 frei"})
	if last := out.text[len(out.text)-1]; !strings.Contains(last, "#99") {
		t.Fatalf("an unknown proposal is not explained: %q", last)
	}
}

func TestParseSignalEvent(t *testing.T) {
	ev := `{"jsonrpc":"2.0","method":"receive","params":{"envelope":{"source":"+4915100000001","sourceNumber":"+4915100000001","sourceName":"Martin","timestamp":1791700000000,"dataMessage":{"timestamp":1791700000000,"message":"gib #17 frei","expiresInSeconds":0}},"account":"+4915100000009"}}`
	m, ok := ParseEvent([]byte(ev))
	if !ok || m.From != "+4915100000001" || m.Text != "gib #17 frei" {
		t.Fatalf("parsed %+v %v", m, ok)
	}
	if _, ok := ParseEvent([]byte(`{"jsonrpc":"2.0","method":"receive","params":{"envelope":{"source":"+49","receiptMessage":{}}}}`)); ok {
		t.Fatal("a receipt is not a message")
	}
}
