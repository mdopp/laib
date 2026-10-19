package main

import (
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

// B2 (#184 review): Matrix that fails its check at start — homeserver down,
// token revoked, room changed — leaves the relay running: Signal approvals
// go on, and the journal says why Matrix is off.
func TestSignalApprovesWhileMatrixIsRefused(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	laib := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Name string            `json:"name"`
				Args map[string]string `json:"arguments"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		calls = append(calls, req.Params.Name+" "+req.Params.Args["args"])
		mu.Unlock()
		text := "ok"
		if req.Params.Name == "laib_inbox" {
			text = `[{"id":17,"reason":"paperless öffentlich","card":{"summary":"paperless öffentlich"}}]`
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}})
	}))
	defer laib.Close()
	signal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/rpc" {
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
			return
		}
		// signal-cli's own daemon (#469): the event stream is /api/v1/events for the Bote's account
		if r.URL.Path != "/api/v1/events" || r.URL.Query().Get("account") != "+4915100000009" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", `{"jsonrpc":"2.0","method":"receive","params":{"envelope":{"sourceNumber":"+4915100000001","dataMessage":{"message":"gib #17 frei"}}}}`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer signal.Close()
	env := map[string]string{
		"ADMIN_NUMBER": "+4915100000001", "SIGNAL_ACCOUNT": "+4915100000009", "SIGNAL_HTTP_URL": signal.URL,
		"ADMIN_MXID": "@admin:hs.test", "ADMIN_ROOM": "!raum:hs.test", "MATRIX_HOMESERVER": "http://127.0.0.1:1", "MATRIX_ACCESS_TOKEN": "tok",
		"LAIB_MCP_URL": laib.URL, "LAIB_TOKEN": "approver-token",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- run(ctx, func(k string) string { return env[k] }) }()
	seen := func(s string) bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(strings.Join(calls, "\n"), s)
	}
	for ctx.Err() == nil && !(seen("laib_approve 17") && seen("laib_note")) {
		time.Sleep(20 * time.Millisecond)
	}
	if !seen("laib_approve 17") {
		t.Fatalf("Signal approvals stopped with Matrix: %v", calls)
	}
	if !seen("Matrix") {
		t.Fatalf("the journal does not say Matrix is off: %v", calls)
	}
	select {
	case err := <-errc:
		t.Fatalf("the relay ended: %v", err)
	default:
	}
	cancel()
	if err := <-errc; err != nil {
		t.Fatalf("a clean stop: %v", err)
	}
}

// The relay refuses to start with nothing to decide on.
func TestRunNeedsAnIdentity(t *testing.T) {
	if err := run(context.Background(), func(k string) string { return map[string]string{"LAIB_TOKEN": "x"}[k] }); err == nil {
		t.Fatal("no ADMIN_NUMBER and no ADMIN_MXID must be refused")
	}
}
