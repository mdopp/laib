package mcpclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPostSendsTheBearerTheHeadersAndOneRequest(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("X-Laib-Ceiling") != "propose" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("headers %v", r.Header)
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}],"isError":true}}`)
	}))
	defer srv.Close()
	a, err := Post(context.Background(), srv.Client(), srv.URL, "tok", map[string]string{"X-Laib-Ceiling": "propose"}, "tools/call", map[string]any{"name": "laib_note", "arguments": Args(`say "hi"`, "now")})
	if err != nil || a.Bad != nil || a.Error != nil || a.StatusCode != 200 {
		t.Fatalf("%v %+v", err, a)
	}
	if got["method"] != "tools/call" || got["params"].(map[string]any)["arguments"].(map[string]any)["args"] != `"say hi" "now"` {
		t.Fatalf("request %v", got)
	}
	if text, isError, ok := a.Tool(); text != "ab" || !isError || !ok {
		t.Fatalf("tool %q %v %v", text, isError, ok)
	}
}

func TestPostKeepsWhatLaibAnswersThatIsNoResult(t *testing.T) {
	for body, check := range map[string]func(*Answer) bool{
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"unknown"}}`: func(a *Answer) bool {
			return a.Bad == nil && a.Error != nil && a.Error.Code == -32601 && a.Error.Message == "unknown"
		},
		`<html>bad gateway</html>`:             func(a *Answer) bool { return a.Bad != nil && string(a.Body) == "<html>bad gateway</html>" },
		`{"jsonrpc":"2.0","id":1,"result":{}}`: func(a *Answer) bool { _, _, ok := a.Tool(); return a.Bad == nil && !ok },
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		a, err := Post(context.Background(), srv.Client(), srv.URL, "t", nil, "tools/list", map[string]any{})
		srv.Close()
		if err != nil || !check(a) {
			t.Errorf("%s: %v %+v", body, err, a)
		}
	}
}
