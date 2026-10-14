package lease

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func testServer(t *testing.T) (*Server, *httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var sysd []string
	Systemctl = func(_ context.Context, verb string, units []string) error {
		mu.Lock()
		defer mu.Unlock()
		sysd = append(sysd, verb+" "+strings.Join(units, ","))
		return nil
	}
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]any{{"id": "gemma-4-e4b"}, {"id": "qwen3.6-35b-a3b"}, {"id": "gemma-4-12b"}}})
		default:
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			json.NewEncoder(w).Encode(map[string]any{"model": req["model"], "choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "ok"}}}})
		}
	}))
	t.Cleanup(router.Close)
	cfg := config42(t, router.URL)
	cfg.Modes["full"] = Mode{Stops: []string{"model/embed", "voice/wakeword-trainer"}, Voice: "cpu"}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s, httptest.NewServer(s.Handler()), &sysd
}

func post(t *testing.T, url string, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer lease-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestHouseholdOnlyThenModeFollowsThenHeld(t *testing.T) {
	s, srv, sysd := testServer(t)
	defer srv.Close()
	// household: the household preset passes, a foreign preset takes full itself
	if code, _ := post(t, srv.URL+"/v1/chat/completions", `{"model":"gemma-4-e4b","messages":[]}`); code != 200 {
		t.Fatal("household preset refused", code)
	}
	code, out := post(t, srv.URL+"/v1/chat/completions", `{"model":"qwen3.6-35b-a3b","messages":[]}`)
	if code != 200 || out["model"] != "qwen3.6-35b-a3b" {
		t.Fatalf("mode did not follow: %d %v", code, out)
	}
	l := s.read()
	if l == nil || l.Holder != AutoHolder || l.Mode != "full" {
		t.Fatalf("auto lease %+v", l)
	}
	if !strings.Contains(strings.Join(*sysd, "\n"), "stop llama-embed.service,stimme-wakeword-trainer.service") {
		t.Fatalf("full did not stop its units: %v", *sysd)
	}
	// a named holder takes shared over from the automatic window
	code, out = post(t, srv.URL+"/api/model-lease", `{"model":"shared","ttl_s":60,"holder":"foundry-chronicle"}`)
	if code != 200 || out["state"] != "ready" || out["model"] != "shared" {
		t.Fatalf("%d %v", code, out)
	}
	// units come back when leaving full for shared
	if !strings.Contains(strings.Join(*sysd, "\n"), "start llama-embed.service,stimme-wakeword-trainer.service") {
		t.Fatalf("leaving full did not restart units: %v", *sysd)
	}
	// in shared, the large preset is refused with the holder named
	code, out = post(t, srv.URL+"/v1/chat/completions", `{"model":"qwen3.6-35b-a3b","messages":[]}`)
	if code != 409 || !strings.Contains(out["error"].(map[string]any)["message"].(string), "foundry-chronicle") {
		t.Fatalf("%d %v", code, out)
	}
	// another holder is held off
	code, out = post(t, srv.URL+"/api/model-lease", `{"model":"full","ttl_s":60,"holder":"bote"}`)
	if code != 409 || out["reason"] != "held" || out["holder"] != "foundry-chronicle" {
		t.Fatalf("%d %v", code, out)
	}
	// the catalogue lists everything, marked
	resp, _ := http.Get(srv.URL + "/v1/models")
	var listing map[string]any
	json.NewDecoder(resp.Body).Decode(&listing)
	if listing["mode"] != "shared" {
		t.Fatal(listing)
	}
	marks := map[string]bool{}
	for _, e := range listing["data"].([]any) {
		m := e.(map[string]any)
		marks[m["id"].(string)] = m["allowed_in_mode"].(bool)
	}
	if !marks["gemma-4-12b"] || marks["qwen3.6-35b-a3b"] {
		t.Fatal(marks)
	}
	// release by the wrong holder is refused, by the right one works
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/model-lease", strings.NewReader(`{"holder":"bote"}`))
	req.Header.Set("Authorization", "Bearer lease-secret")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 409 {
		t.Fatal("wrong holder released")
	}
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/model-lease", strings.NewReader(`{"holder":"foundry-chronicle"}`))
	req.Header.Set("Authorization", "Bearer lease-secret")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 || s.read() != nil {
		t.Fatal("release failed")
	}
}

func TestExclusiveStopsEverythingAndUnknownIsRefused(t *testing.T) {
	s, srv, sysd := testServer(t)
	defer srv.Close()
	code, out := post(t, srv.URL+"/api/model-lease", `{"model":"exclusive","holder":"maintenance","ttl_s":30}`)
	if code != 202 && code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	if l := s.read(); l == nil || l.Mode != "exclusive" || len(l.Allowed) != 0 {
		t.Fatalf("%+v", l)
	}
	if !strings.Contains(strings.Join(*sysd, "\n"), "stop llama-embed.service,stimme-wakeword-trainer.service") {
		t.Fatal(*sysd)
	}
	code, _ = post(t, srv.URL+"/v1/chat/completions", `{"model":"gemma-4-e4b","messages":[]}`)
	if code != 409 {
		t.Fatal("exclusive still answers")
	}
	s.Release(context.Background(), "")
	code, out = post(t, srv.URL+"/api/model-lease", `{"model":"nope","holder":"x"}`)
	if code != 400 || out["reason"] != "invalid_model" || !strings.Contains(out["message"].(string), "unknown") {
		t.Fatalf("%d %v", code, out)
	}
}
