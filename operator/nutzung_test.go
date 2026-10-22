package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeUsage is usage-metrics: /summary and /misses behind the bearer token.
func fakeUsage(t *testing.T, replies map[string]json.RawMessage) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer um-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		body, ok := replies[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok || r.URL.Query().Get("days") != "7" {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The week the owner described (#270): Energie is never used, "Wann kommt
// der Müll" went unanswered nine times.
var usageWeek = map[string]json.RawMessage{
	"summary": json.RawMessage(`{"days":7,"from":"2026-10-10","total":182,"apps":[{"app":"haus","total":182,"events":[{"event":"start.view","count":140},{"event":"einkauf.view","count":35},{"event":"wetter.view","count":7}]}],"knownApps":["energie","haus"]}`),
	"misses":  json.RawMessage(`{"days":7,"misses":[{"source":"bote","text":"Wann kommt der Müll?","count":9,"lastDay":"2026-10-15"},{"source":"stimme","text":"Mach die Musik im Bad an","count":2,"lastDay":"2026-10-14"}]}`),
}

func suggest(id, saw, suggestion, better string) ToolCall {
	b, _ := json.Marshal(map[string]string{"text": suggestion, "saw": saw, "better": better})
	return tcall(id, "laib_wish", string(b))
}

// #270: the weekly skill reads the counts and the unanswered sentences,
// proposes at most three improvements — each with what it saw in numbers,
// the suggestion and what gets better — and never quotes a family
// member's sentence into a ticket. The cap and the quote rule are code.
func TestNutzungProposesAtMostThreeFromTheUsageData(t *testing.T) {
	var mu sync.Mutex
	var wishes []string
	var firstUser string
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		res := map[string]any{}
		switch req.Method {
		case "tools/list":
			res["tools"] = []map[string]any{{"name": "laib_wish"}, {"name": "laib_note"}, {"name": "laib_restart"}}
		case "tools/call":
			var p struct {
				Name string         `json:"name"`
				Args map[string]any `json:"arguments"`
			}
			json.Unmarshal(req.Params, &p)
			mu.Lock()
			if p.Name == "laib_wish" {
				wishes = append(wishes, shownArgs(p.Args))
			}
			mu.Unlock()
			res["content"] = []map[string]string{{"type": "text", "text": "Vorschlag angelegt"}}
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
	}))
	t.Cleanup(mcp.Close)
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{suggest("1", "9 unbeantwortete Fragen nach der Müllabfuhr in 7 Tagen", "Den Abfuhrkalender als Kalender in Haus anbinden, damit Bote und Stimme antworten können.", "familie")}},
		{Role: "assistant", ToolCalls: []ToolCall{suggest("2", "Bote: Wann kommt der Müll? (9×)", "Abfuhrkalender anbinden.", "familie")}},
		{Role: "assistant", ToolCalls: []ToolCall{suggest("3", "energie: 0 Aufrufe in 7 Tagen, haus 182", "Die Energie-Seite von der Startseite nach hinten schieben.", "beides")}},
		{Role: "assistant", ToolCalls: []ToolCall{suggest("4", "keine Zahlen", "Mehr Musik.", "familie")}},
		{Role: "assistant", ToolCalls: []ToolCall{suggest("5", "2 Bitten um Musik im Bad in 7 Tagen", "Den Lautsprecher im Bad in Music Assistant aufnehmen.", "familie")}},
		{Role: "assistant", ToolCalls: []ToolCall{suggest("6", "1 weitere Sache, 3 Mal", "Noch ein Vorschlag.", "wartung")}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("7", "laib_note", `{"kind":"watch","text":"Drei Vorschläge zur Nutzung angelegt."}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true,
		Usage: &Usage{URL: fakeUsage(t, usageWeek).URL, Token: "um-token"}}
	op.LLM.Client = &http.Client{Transport: peek(func(body string) {
		if firstUser == "" {
			firstUser = body
		}
	})}
	s, err := op.Run(context.Background(), skillFor(t, Trigger{}, "nutzung"), Trigger{Kind: "tick"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"energie", "182", "9×", "Müll"} {
		if !strings.Contains(firstUser, want) {
			t.Fatalf("the model was not shown %q:\n%s", want, firstUser)
		}
	}
	if len(wishes) != 3 {
		t.Fatalf("at most three suggestions a week, %d went out: %q", len(wishes), wishes)
	}
	all := strings.Join(wishes, "\n")
	if strings.Contains(all, "Wann kommt der Müll") || strings.Contains(all, "Mehr Musik") || strings.Contains(all, "Noch ein Vorschlag") {
		t.Fatalf("a quote, a suggestion without numbers or a fourth one went out: %q", wishes)
	}
	for _, w := range wishes {
		if !strings.Contains(w, `"saw":`) || !strings.Contains(w, `"better":`) {
			t.Fatalf("a suggestion without what it saw or what gets better: %q", w)
		}
	}
	if !strings.Contains(all, "Abfuhrkalender") || !strings.Contains(all, "Energie-Seite") || !strings.Contains(all, `"better":"beides"`) {
		t.Fatalf("%q", wishes)
	}
	if s.Outcome != "green" || !strings.Contains(s.Note, "Vorschläge") {
		t.Fatalf("%+v", s)
	}
}

// #270: without usage-metrics (no service, no token, not reachable) the
// skill says so in one note, asks no model and proposes nothing.
func TestNutzungWithoutUsageMetricsNotesAndProposesNothing(t *testing.T) {
	mcp, calls := fakeMCP(t, nil)
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: "http://127.0.0.1:1", Model: "test"}, Home: t.TempDir(), Typed: true}
	s, err := op.Run(context.Background(), skillFor(t, Trigger{}, "nutzung"), Trigger{Kind: "tick"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(*calls, "\n")
	if strings.Contains(got, "laib_wish") || !strings.Contains(got, `laib_note {"kind":"watch"`) || !strings.Contains(s.Note, "Nutzungsdaten") {
		t.Fatalf("calls %v, session %+v", *calls, s)
	}
}

// peek is a transport that shows each request body's last user message.
type peek func(string)

func (p peek) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost {
		return http.DefaultTransport.RoundTrip(r) // the lease and the catalogue (#550)
	}
	var req struct {
		Messages []Message `json:"messages"`
	}
	b, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(b))
	json.Unmarshal(b, &req)
	for _, m := range req.Messages {
		if m.Role == "user" {
			p(m.Content)
			break
		}
	}
	return http.DefaultTransport.RoundTrip(r)
}
