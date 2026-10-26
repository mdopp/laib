package lease

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// #677: the lease left the kernel and speaks to laib with its own MCP
// client. These are the calls it sends, argument for argument; the
// kernel's cmd/laib/pause624_test.go sends the same strings to the real
// laib serve. A refusal comes back as the error, with laib's text.
func TestTheLeaseSendsLaibTheCallsTheKernelExpects(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name string            `json:"name"`
				Args map[string]string `json:"arguments"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if r.Header.Get("Authorization") != "Bearer laib_lease_t" || req.Method != "tools/call" {
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "unauthorized"}})
			return
		}
		calls = append(calls, req.Params.Name+" "+req.Params.Args["args"])
		text, isErr := "{}", false
		switch req.Params.Name {
		case "laib_status":
			text = `{"services":[{"name":"stimme","provides":["voice"],"units":{"stimme-whisper.service":"active"}}]}`
		case "laib_resume":
			text, isErr = "stimme ist nicht pausiert", true
		}
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": isErr}})
	}))
	t.Cleanup(srv.Close)
	box := MCP{URL: srv.URL, Token: "laib_lease_t"}
	ctx := context.Background()
	svcs, err := box.Services(ctx)
	if err != nil || len(svcs) != 1 || svcs[0].Name != "stimme" || svcs[0].Units["stimme-whisper.service"] != "active" || !slices.Equal(svcs[0].Provides, []string{"voice"}) {
		t.Fatalf("status: %v %+v", err, svcs)
	}
	until := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	if err := box.Pause(ctx, Hold{Service: "stimme", Until: until, Reason: `Grafikkarte reserviert für "bote"`, Stop: []string{"stimme-whisper.service"}, Restart: []string{"stimme-piper.service"}}); err != nil {
		t.Fatal(err)
	}
	if err := box.Note(ctx, "bote hält die Grafikkarte seit 21 min"); err != nil {
		t.Fatal(err)
	}
	if err := box.Resume(ctx, "stimme"); err == nil || !strings.Contains(err.Error(), "nicht pausiert") {
		t.Fatalf("laib's refusal is the error: %v", err)
	}
	want := []string{
		"laib_status ",
		`laib_pause "stimme" "--until" "2026-10-26T12:00:00Z" "--stop" "stimme-whisper.service" "--restart" "stimme-piper.service" "Grafikkarte reserviert für bote"`,
		`laib_note "--kind" "watch" "bote hält die Grafikkarte seit 21 min"`,
		`laib_resume "stimme"`,
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("the calls:\n%s", strings.Join(calls, "\n"))
	}
	if err := (MCP{URL: srv.URL, Token: "wrong"}).Resume(ctx, "stimme"); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("an MCP error is the error: %v", err)
	}
}
