package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The 48 hours a human read on the Kraftwerk (#551), in the shapes the
// journal had: journalctl -o short-iso lines, most of them INFO on stderr.
func kraftwerkDay(adguardTimeouts int) map[string]map[string]string {
	var b strings.Builder
	line := func(unit, format string, a ...any) {
		fmt.Fprintf(&b, "2026-10-21T%02d:%02d:%02d+02:00 kraftwerk %s[%d]: %s\n", len(a)%24, (b.Len()/7)%60, b.Len()%60, unit, 1000+b.Len()%900, fmt.Sprintf(format, a...))
	}
	take := func() string { s := b.String(); b.Reset(); return s }

	for i := 0; i < adguardTimeouts; i++ {
		line("adguard", "2026/10/21 03:%02d:%02d.%06d [error] dnsproxy: upstream 192.168.178.1:53 failed to respond: exchanging with 192.168.178.1:53 over udp: read udp 10.88.0.4:%d->192.168.178.1:53: i/o timeout", i%60, i%60, 100000+i, 40000+i)
	}
	line("adguard", "2026/10/21 05:00:01.000001 [info] dnsproxy: starting dns proxy server")
	adguard := take()

	for i := 0; i < 44; i++ {
		line("immich-server", "[Nest] 7  - 10/21/2026, 3:%02d:%02d AM   ERROR [Microservices:JobService] Unable to run job handler (thumbnailGeneration/generate-thumbnails): Error: Input file is missing: /usr/src/app/upload/upload/%08x-1c2d-4e5f-8a9b-%012x/IMG_%04d.jpg", i%60, i%60, 0xa1b2c3d4+i, 0xdeadbeef00+i, 1000+i)
		line("immich-server", "    at Sharp.toFile (/usr/src/app/node_modules/sharp/lib/output.js:%d:5)", 90+i%3)
	}
	line("immich-server", "[Nest] 7  - 10/21/2026, 4:00:00 AM   ERROR [Api:DatabaseRepository] database connection lost: Connection terminated unexpectedly")
	line("immich-server", "[Nest] 7  - 10/21/2026, 4:00:01 AM     LOG [Api:Bootstrap] Immich Server is listening on http://[::1]:2283 [v1.119.0] [production]")
	immich := take()

	for i := 0; i < 300; i++ {
		line("radicale", "[2026-10-21 03:%02d:%02d +0200] [%d/Thread-%d (process_request_thread)] [INFO] Successful login: 'cdopp'", i%60, i%60, 1, i)
		line("radicale", "[2026-10-21 03:%02d:%02d +0200] [%d/Thread-%d (process_request_thread)] [INFO] PROPFIND response status for '/cdopp/' in 0.0%02d seconds: 207 Multi-Status", i%60, i%60, 1, i, i%100)
	}
	radicale := take()

	for i := 0; i < 12; i++ {
		line("caddy", `{"level":"error","ts":1729560679.%03d,"logger":"http.handlers.reverse_proxy","msg":"aborting with incomplete response","upstream":"127.0.0.1:8650","duration":%d.5,"request":{"remote_ip":"192.168.178.%d","proto":"HTTP/2.0","method":"GET","host":"haus.dopp.cloud","uri":"/napi/events?since=%d","headers":{"User-Agent":["Mozilla/5.0 (iPhone; CPU iPhone OS 18_%d like Mac OS X)"]}},"error":"reading: context canceled"}`, i, 30+i, 20+i, 1729560000+i, i)
	}
	line("caddy", `{"level":"info","ts":1729560600.1,"logger":"tls","msg":"certificate is valid","identifiers":["haus.dopp.cloud"]}`)
	caddy := take()

	for i := 0; i < 200; i++ {
		line("stimme-whisper", `INFO:     10.88.0.%d:%d - "POST /v1/audio/transcriptions HTTP/1.1" 200 OK`, 2+i%5, 50000+i)
	}
	whisper := take()

	return map[string]map[string]string{
		"adguard":  {"adguard.service": adguard},
		"immich":   {"immich-pod.service": "", "immich-server.service": immich},
		"radicale": {"radicale.service": radicale},
		"caddy":    {"caddy.service": caddy},
		"stimme":   {"stimme-whisper.service": whisper},
	}
}

// reviewMCP is a laib MCP for the log review: status lists the services,
// logs answers per service, everything else is recorded.
func reviewMCP(t *testing.T, day *map[string]map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		res := map[string]any{}
		switch req.Method {
		case "tools/list":
			var tools []map[string]any
			for _, n := range []string{"laib_status", "laib_logs", "laib_wish", "laib_note", "laib_restart", "laib_context"} {
				tools = append(tools, map[string]any{"name": n, "description": n, "inputSchema": map[string]any{"type": "object"}})
			}
			res["tools"] = tools
		case "tools/call":
			var p struct {
				Name string         `json:"name"`
				Args map[string]any `json:"arguments"`
			}
			json.Unmarshal(req.Params, &p)
			mu.Lock()
			calls = append(calls, p.Name+" "+shownArgs(p.Args))
			mu.Unlock()
			text := "ok"
			switch p.Name {
			case "laib_status":
				var svcs []map[string]any
				for _, n := range []string{"adguard", "caddy", "immich", "radicale", "stimme"} {
					svcs = append(svcs, map[string]any{"name": n, "state": "grün"})
				}
				b, _ := json.Marshal(map[string]any{"ready": map[string]any{"state": "bereit"}, "services": svcs})
				text = string(b)
			case "laib_logs":
				a, _ := p.Args["args"].(string)
				svc, _, _ := strings.Cut(a, " ")
				b, _ := json.Marshal((*day)[svc])
				text = string(b)
			case "laib_inbox":
				text = "[]"
			}
			res["content"] = []map[string]string{{"type": "text", "text": text}}
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// #550 point 6, #551 point 7: the log review groups the units' error lines
// by pattern in code, tells real errors from noise by their content (the
// journal's err is stderr, and most of it is INFO), and shows the model
// only what is new, recurring or growing since the last review. A day like
// the last asks no model and writes no note.
func TestDurchsichtShowsOnlyNewRecurringOrGrowingRealErrors(t *testing.T) {
	day := kraftwerkDay(40)
	mcp, calls := reviewMCP(t, &day)
	llm, asked := recLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"watch","text":"AdGuard: 40 PTR-Timeouts."}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	sk := skillFor(t, Trigger{Kind: "tick"}, "durchsicht")
	if sk.Trigger.Daily == "" || sk.Context != "logs" {
		t.Fatalf("durchsicht is a daily skill that starts from the logs: %+v", sk)
	}
	run := func() (Session, string) {
		t.Helper()
		before := len(*asked)
		s, err := op.Run(context.Background(), sk, Trigger{Kind: "tick"})
		if err != nil {
			t.Fatal(err)
		}
		if len(*asked) == before {
			return s, ""
		}
		return s, (*asked)[before][1].Content
	}

	// the first review: every real pattern seen at least three times
	s, user := run()
	if user == "" {
		t.Fatalf("the first review asks the model: %+v", s)
	}
	for _, want := range []string{"adguard.service", "40×", "i/o timeout", "immich-server.service", "44×", "Input file is missing", "caddy.service", "12×", "aborting with incomplete response", "Beispiel:"} {
		if !strings.Contains(user, want) {
			t.Errorf("the model is not shown %q:\n%s", want, user)
		}
	}
	for _, noise := range []string{"Successful login", "PROPFIND", "POST /v1/audio", "starting dns proxy", "certificate is valid", "Sharp.toFile", "database connection lost"} {
		if strings.Contains(user, noise) {
			t.Errorf("noise or a one-off reached the model (%q):\n%s", noise, user)
		}
	}
	if !strings.Contains(user, "radicale.service") {
		t.Errorf("the stderr noise is counted per unit, for the owner: %s", user)
	}
	if len(user) > 4000 {
		t.Errorf("the model sees a small summary, not the journal: %d bytes", len(user))
	}
	all := strings.Join(*calls, "\n")
	if strings.Contains(all, "laib_context") || !strings.Contains(all, "laib_logs adguard --since 24h") || !strings.Contains(all, "--priority warning") {
		t.Errorf("the runner reads each service's warnings and errors of a day, and no box context:\n%s", all)
	}

	// the next day, the same: only the error that came back is news
	s, user = run()
	if user == "" || !strings.Contains(user, "database connection lost") || !strings.Contains(user, "wiederkehrend") {
		t.Fatalf("an error seen in two reviews is recurring news: %+v\n%s", s, user)
	}
	if strings.Contains(user, "i/o timeout") || strings.Contains(user, "Input file is missing") {
		t.Errorf("a pattern already shown is not shown again while it holds:\n%s", user)
	}

	// and again: nothing new — no model, no note
	n := len(*calls)
	s, user = run()
	if user != "" || s.Outcome != "unchanged" {
		t.Fatalf("an unchanged day asks no model: %q %+v", user, s)
	}
	for _, c := range (*calls)[n:] {
		if strings.HasPrefix(c, "laib_note") || strings.HasPrefix(c, "laib_wish") {
			t.Errorf("an unchanged day writes nothing: %s", c)
		}
	}

	// the PTR timeouts grow fivefold: that is news again
	day = kraftwerkDay(200)
	_, user = run()
	if !strings.Contains(user, "200×") || !strings.Contains(user, "wächst") || strings.Contains(user, "Input file is missing") {
		t.Errorf("a growing pattern is shown, the stable ones are not:\n%s", user)
	}
}

// A review whose model failed is not remembered: the next one shows the
// same news again.
func TestDurchsichtForgetsAReviewWhoseSessionFailed(t *testing.T) {
	day := kraftwerkDay(40)
	mcp, _ := reviewMCP(t, &day)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "busy", 503) }))
	t.Cleanup(down.Close)
	home := t.TempDir()
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: down.URL, Model: "test"}, Home: home, Typed: true}
	sk := skillFor(t, Trigger{Kind: "tick"}, "durchsicht")
	if _, err := op.Run(context.Background(), sk, Trigger{Kind: "tick"}); err == nil {
		t.Fatal("a model that answers 503 is an error")
	}
	llm, asked := recLLM(t, []Message{{Role: "assistant", Content: "AdGuard: 40 PTR-Timeouts."}})
	op.LLM = &LLM{URL: llm.URL, Model: "test"}
	if _, err := op.Run(context.Background(), sk, Trigger{Kind: "tick"}); err != nil {
		t.Fatal(err)
	}
	if len(*asked) == 0 || !strings.Contains((*asked)[0][1].Content, "i/o timeout") {
		t.Fatalf("the failed review's news is shown again: %v", *asked)
	}
}

// The classifier (#551 point 7): the level a line names decides, else its
// words; numbers, ids, paths and timestamps are not part of a pattern.
func TestLogLinePatterns(t *testing.T) {
	for _, c := range []struct {
		msg, pattern string
		real         bool
	}{
		{"[2026-10-21 03:11:19 +0200] [1/Thread-7 (process_request_thread)] [INFO] Successful login: 'cdopp'", "", false},
		{`INFO:     10.88.0.3:50111 - "POST /v1/audio/transcriptions HTTP/1.1" 500 Internal Server Error`, "", false},
		{"[2026-10-21 03:11:19,120] [INFO] [paperless.consumer] Consuming error-report.pdf", "", false},
		{"srv  update_slots: id  0 | task 4711 | prompt done, n_past = 812", "", false},
		{"2026/10/21 03:11:19.123456 [error] dnsproxy: exchanging with 192.168.178.1:53 over udp: i/o timeout", "[error] dnsproxy: exchanging with <ip> over udp: i/o timeout", true},
		{"[03:11:19] [ERR] [42] MediaBrowser.Providers.Music.AudioFileProber: End of file: /media/cdopp/music/8f3a9c2e41b7.1.aac", "[ERR] [<n>] MediaBrowser.Providers.Music.AudioFileProber: End of file: <pfad>", true},
		{"[03:11:19] [WRN] [12] Jellyfin.Server: slow response", "", false},
		{`{"level":"error","ts":1729560679.1,"logger":"http.handlers.reverse_proxy","msg":"aborting with incomplete response","request":{"remote_ip":"192.168.178.20"},"error":"reading: context canceled"}`, "http.handlers.reverse_proxy: aborting with incomplete response (reading: context canceled)", true},
		{"2026-10-21 03:11:19.552 ERROR (MainThread) [homeassistant.components.websocket_api.http.connection] [140234] Unauthorized: Bote", "ERROR (MainThread) [homeassistant.components.websocket_api.http.connection] [<n>] Unauthorized: Bote", true},
		{"AttributeError: 'NoneType' object has no attribute 'speech'", "AttributeError: 'NoneType' object has no attribute 'speech'", true},
		{"sse: write tcp 127.0.0.1:8650->127.0.0.1:51234: write: transport endpoint is not connected", "sse: write tcp <ip>-><ip>: write: transport endpoint is not connected", true},
		{"thumbnail 0b5c7f2e-9d41-4c3a-b8e6-1f2a3b4c5d6e failed after 3 attempts", "thumbnail <id> failed after <n> attempts", true},
		{"    at Sharp.toFile (/usr/src/app/node_modules/sharp/lib/output.js:90:5)", "", false},
	} {
		got, real := logPattern(c.msg)
		if real != c.real || (real && got != c.pattern) {
			t.Errorf("%s\n got  %v %q\n want %v %q", c.msg, real, got, c.real, c.pattern)
		}
	}
}
