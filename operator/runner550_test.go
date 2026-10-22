package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// #550: on 2026-10-22 03:11 the same note stood five times in the journal
// within 14 s. The runner carried out every laib_note a model asked for, in
// one reply or over passes; a session writes its note once.
func TestASessionWritesItsNoteOnce(t *testing.T) {
	note := `{"kind":"incident","text":"whoami war rot, Neustart um 03:11, jetzt grün."}`
	mcp, calls := fakeMCP(t, nil)
	llm, asked := recLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", note), tcall("2", "laib_note", note)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("3", "laib_note", note)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("4", "laib_note", note)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir()}
	tr := Trigger{Kind: "event", Service: "whoami", Detail: "whoami: health red"}
	s, err := op.Run(context.Background(), skillFor(t, tr, ""), tr)
	if err != nil {
		t.Fatal(err)
	}
	if n := countPrefix(*calls, "laib_note"); n != 1 {
		t.Fatalf("the note was written %d times:\n%s", n, strings.Join(*calls, "\n"))
	}
	if s.Outcome != "green" || countPrefix(s.Calls, "laib_note") != 1 {
		t.Fatalf("%+v", s)
	}
	last := (*asked)[len(*asked)-1]
	if !strings.Contains(last[len(last)-1].Content, "Notiz") {
		t.Fatalf("the model was not told its note stands: %+v", last[len(last)-1])
	}
}

// #550: seven red sessions in 40 h, about 100 s and 12 calls each, and the
// log said only "red". A red session's line names what was red, why the
// session ended red and what came of it.
func TestARedSessionsLineSaysWhatWasRedAndWhatCameOfIt(t *testing.T) {
	mcp, _ := fakeMCP(t, map[string]string{"laib_propose": "Vorschlag #12 angelegt"})
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_propose", `{"reason":"jellyfin neu","service":"jellyfin","key":"IMAGE","value":"j:1.2","finding":"unit jellyfin","evidence":"unit failed","undo":"IMAGE zurück"}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("2", "laib_diag", `{}`)}}, // loops until the limit
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), MaxCalls: 4}
	tr := Trigger{Kind: "event", Service: "jellyfin", Detail: "jellyfin: apply: unit failed"}
	s, err := op.Run(context.Background(), skillFor(t, tr, ""), tr)
	if err != nil {
		t.Fatal(err)
	}
	line := s.Line()
	for _, want := range []string{"session vorfall/jellyfin: red", "jellyfin: apply: unit failed", "4 Aufrufen", "→ vorgeschlagen #12"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line lacks %q: %s", want, line)
		}
	}
	green := Session{Skill: "routineblick", Outcome: "green", Calls: []string{"laib_note x"}, Duration: 900}
	if l := green.Line(); l != "session routineblick/: green, 1 calls, 900ms" {
		t.Errorf("a green line changed: %s", l)
	}
}

// kraftwerkContext is laib context as the Kraftwerk answered it on
// 2026-10-21 (#550): 20 services and 232 of the Operator's own notes in a
// day, the real events (lease, backup) among them.
func kraftwerkContext() string {
	var services []map[string]any
	for i := range 20 {
		services = append(services, map[string]any{"name": fmt.Sprintf("dienst%02d", i), "state": "grün", "units": map[string]string{fmt.Sprintf("dienst%02d.service", i): "active"}})
	}
	day := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	var journal []map[string]any
	for i := range 232 {
		journal = append(journal, map[string]any{"t": day.Add(time.Duration(i) * 6 * time.Minute), "actor": "ops", "verb": "note", "result": "info",
			"reason": "watch: Um 05:40 Uhr: Alle 20 Dienste sind grün, kein Drift. Gelb: kein Wächter außerhalb der Box, der Wiederherstellungstest ist älter als 35 Tage."})
		if i == 200 {
			journal = append(journal, map[string]any{"t": day.Add(20 * time.Hour), "actor": "lease", "verb": "lease", "service": "llama", "result": "gelb", "detail": "werkstatt hält die Grafikkarte seit 61 min"})
		}
	}
	b, _ := json.Marshal(map[string]any{"ready": map[string]any{"state": "bereit"}, "services": services, "journal": journal, "domain": "example.org", "lessons": []string{}, "upstream": map[string][]string{}})
	return string(b)
}

// sized is a transport that keeps each chat request's size in bytes.
type sized struct{ sizes *[]int }

func (s sized) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/chat/completions") {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		*s.sizes = append(*s.sizes, len(b))
	}
	return http.DefaultTransport.RoundTrip(r)
}

// #550: a look that runs read about 28,000 tokens (4.0 million in 143
// prompts a day), because the context grew with the model's 131k window.
// The look's context is diag and the last journal lines, inside its own
// budget, whatever the window: under 6,000 tokens for the first request.
func TestALooksContextIsSmallWhateverTheWindow(t *testing.T) {
	ctxJSON := kraftwerkContext()
	mcp, _ := fakeMCP(t, map[string]string{"laib_context": ctxJSON, "laib_diag": "[" + restoreTest + "," + watchdog + "]", "laib_repair": "Vorschlag #7 angelegt"})
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"watch","text":"Zwei Punkte sind gelb."}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	var sizes []int
	var user string
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test", Client: &http.Client{Transport: sized{&sizes}}}, Home: t.TempDir(), Window: 131072}
	sk := skillFor(t, Trigger{Kind: "tick"}, "routineblick")
	before, _ := op.budgets()
	if _, err := op.Run(context.Background(), sk, Trigger{Kind: "tick"}); err != nil {
		t.Fatal(err)
	}
	op.LLM.Client = &http.Client{Transport: peek(func(c string) { user = c })}
	op.Home = t.TempDir()
	op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if len(sizes) == 0 {
		t.Fatal("the look asked no model")
	}
	// about three chars a token, as budgets() counts
	t.Logf("first request: %d bytes, ~%d tokens; the full bundle would have been %d chars, ~%d tokens", sizes[0], sizes[0]/3, min(len(ctxJSON), before), min(len(ctxJSON), before)/3)
	if sizes[0]/3 >= 6000 {
		t.Errorf("the look's first request is ~%d tokens, want under 6000", sizes[0]/3)
	}
	for _, want := range []string{"restore-test", "Grafikkarte"} {
		if !strings.Contains(user, want) {
			t.Errorf("the look's context lacks %q: %.300s…", want, user)
		}
	}
}

// fakeLease is the lease proxy in front of the model (#550): the lease, the
// catalogue with the router's status per preset, and the chat it counts.
func fakeLease(t *testing.T, lease, catalogue *string, script []Message) (*httptest.Server, *int) {
	t.Helper()
	asked := 0
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/model-lease":
			w.Write([]byte(*lease))
		case "/v1/models":
			w.Write([]byte(*catalogue))
		case "/v1/chat/completions":
			m := script[min(asked, len(script)-1)]
			asked++
			json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": m}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

const (
	household = `{"state":"none","mode":"haushalt","holder":"","allowed":["qwen"]}`
	werkstatt = `{"state":"ready","mode":"werkstatt","holder":"werkstatt","allowed":["gemma"]}`
	preparing = `{"state":"preparing","mode":"haushalt","holder":"","allowed":["qwen"]}`
	loaded    = `{"data":[{"id":"qwen","status":{"value":"loaded"}}]}`
	unloaded  = `{"data":[{"id":"qwen","status":{"value":"unloaded"}}]}`
)

// #550: an Operator session evicted the Bote's prompt cache and ran into the
// household model's cold start. While the lease is not haushalt, the model
// is loading or not loaded, no session asks the model: it is skipped with
// its reason — no error, no note — and a look keeps its news for the next.
func TestNoSessionWhileTheGraphicsCardIsElsewhereOrCold(t *testing.T) {
	lease, catalogue := werkstatt, loaded
	llm, asked := fakeLease(t, &lease, &catalogue, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"watch","text":"Zwei Punkte sind gelb."}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	mcp, calls := fakeMCP(t, map[string]string{"laib_diag": "[" + watchdog + "]"})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir()}
	look := skillFor(t, Trigger{Kind: "tick"}, "routineblick")
	for _, c := range []struct{ lease, catalogue, why string }{
		{werkstatt, loaded, "werkstatt"},
		{preparing, loaded, "lädt"},
		{household, unloaded, "nicht geladen"},
	} {
		lease, catalogue = c.lease, c.catalogue
		s, err := op.Run(context.Background(), look, Trigger{Kind: "tick"})
		if err != nil || s.Outcome != "skipped" || !strings.Contains(s.Why, c.why) {
			t.Fatalf("lease %s, catalogue %s: %+v %v", c.lease, c.catalogue, s, err)
		}
		if !strings.Contains(s.Line(), "skipped") || !strings.Contains(s.Line(), c.why) {
			t.Errorf("the log line hides the reason: %s", s.Line())
		}
	}
	if *asked != 0 || countPrefix(*calls, "laib_note") != 0 {
		t.Fatalf("a skipped session asked the model %d times or wrote a note: %v", *asked, *calls)
	}
	// the household model is back and warm: the same findings are news still
	lease, catalogue = household, loaded
	if s, _ := op.Run(context.Background(), look, Trigger{Kind: "tick"}); s.Outcome != "green" || *asked == 0 {
		t.Fatalf("the look after a skip did not run: %+v", s)
	}
}

// A skipped daily skill tries again at the next step, not tomorrow; an
// interval skill keeps its interval.
func TestASkippedDailySkillTriesAgainAtTheNextStep(t *testing.T) {
	lease, catalogue := werkstatt, loaded
	llm, asked := fakeLease(t, &lease, &catalogue, []Message{{Role: "assistant", Content: "Bericht: alles ruhig."}})
	mcp, _ := fakeMCP(t, nil)
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir()}
	report := skillFor(t, Trigger{Kind: "report"}, "")
	r := &Runner{Op: op}
	r.LoadLast()
	day := time.Date(2026, 10, 22, 0, 0, 0, 0, time.Local)
	r.Last[report.Name] = day.Add(-17 * time.Hour)
	if ss := r.Step(context.Background(), day.Add(7*time.Hour+time.Minute), []Skill{report}); len(ss) != 1 || ss[0].Outcome != "skipped" {
		t.Fatalf("%+v", ss)
	}
	lease = household
	if ss := r.Step(context.Background(), day.Add(7*time.Hour+2*time.Minute), []Skill{report}); len(ss) != 1 || ss[0].Outcome != "green" || *asked != 1 {
		t.Fatalf("the skipped report did not run at the next step: %+v", ss)
	}
}
