package automat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeHA answers the four endpoints the tool uses and records writes.
func fakeHA(t *testing.T) (*httptest.Server, map[string]map[string]any) {
	store := map[string]map[string]any{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/states", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"entity_id": "light.flur", "state": "on", "attributes": map[string]any{"friendly_name": "Flur Licht"}},
			{"entity_id": "switch.kaffee", "state": "off", "attributes": map[string]any{"friendly_name": "Kaffeemaschine"}},
			{"entity_id": "automation.flur_aus", "state": "on", "attributes": map[string]any{"id": "flur_aus", "friendly_name": "Flur aus", "last_triggered": "2026-10-10T21:00:00+00:00"}},
		})
	})
	mux.HandleFunc("/api/config/automation/config/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/config/automation/config/")
		switch r.Method {
		case "GET":
			if cfg, ok := store[id]; ok {
				json.NewEncoder(w).Encode(cfg)
			} else {
				w.WriteHeader(404)
			}
		case "POST":
			var cfg map[string]any
			json.NewDecoder(r.Body).Decode(&cfg)
			store[id] = cfg
			w.Write([]byte(`{"result":"ok"}`))
		case "DELETE":
			delete(store, id)
			w.Write([]byte(`{"result":"ok"}`))
		}
	})
	mux.HandleFunc("/api/services/automation/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("[]")) })
	return httptest.NewServer(mux), store
}

func call(t *testing.T, h http.Handler, method string, params any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/mcp", bytes.NewReader(body)))
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func text(res map[string]any) (string, bool) {
	r, _ := res["result"].(map[string]any)
	c, _ := r["content"].([]any)
	if len(c) == 0 {
		return "", false
	}
	m, _ := c[0].(map[string]any)
	isErr, _ := r["isError"].(bool)
	return m["text"].(string), isErr
}

func TestToolsRoundTrip(t *testing.T) {
	ha, store := fakeHA(t)
	defer ha.Close()
	h := Server(Config{HAURL: ha.URL, Token: "tok"}, "test").Handler()

	if res := call(t, h, "tools/list", nil); len(res["result"].(map[string]any)["tools"].([]any)) != 6 {
		t.Fatalf("tools: %v", res)
	}
	out, isErr := text(call(t, h, "tools/call", map[string]any{"name": "entitaeten", "arguments": map[string]any{"domain": "light"}}))
	if isErr || !strings.Contains(out, "light.flur · Flur Licht · on") || strings.Contains(out, "switch.") {
		t.Fatalf("entitaeten: %q %v", out, isErr)
	}
	out, _ = text(call(t, h, "tools/call", map[string]any{"name": "automationen", "arguments": map[string]any{}}))
	if !strings.Contains(out, "flur_aus · Flur aus · on · zuletzt 2026") {
		t.Fatalf("automationen: %q", out)
	}
	y := "alias: Flur Licht aus\ntriggers:\n  - trigger: time\n    at: \"23:00:00\"\nactions:\n  - action: light.turn_off\n    target:\n      entity_id: light.flur\n"
	out, isErr = text(call(t, h, "tools/call", map[string]any{"name": "automation_schreiben", "arguments": map[string]any{"yaml": y}}))
	if isErr || !strings.Contains(out, "flur_licht_aus") {
		t.Fatalf("schreiben: %q %v", out, isErr)
	}
	cfg := store["flur_licht_aus"]
	if cfg["id"] != "flur_licht_aus" || !strings.Contains(cfg["description"].(string), "laib:") || cfg["alias"] != "Flur Licht aus" {
		t.Fatalf("stored: %v", cfg)
	}
	out, isErr = text(call(t, h, "tools/call", map[string]any{"name": "automation_lesen", "arguments": map[string]any{"id": "flur_licht_aus"}}))
	if isErr || !strings.Contains(out, "alias: Flur Licht aus") || !strings.Contains(out, "light.turn_off") {
		t.Fatalf("lesen: %q", out)
	}
	// refusals: no actions, bad id
	_, isErr = text(call(t, h, "tools/call", map[string]any{"name": "automation_schreiben", "arguments": map[string]any{"yaml": "alias: x\ntriggers: []\n"}}))
	if !isErr {
		t.Fatal("an automation without actions was accepted")
	}
	_, isErr = text(call(t, h, "tools/call", map[string]any{"name": "automation_loeschen", "arguments": map[string]any{"id": "../x"}}))
	if !isErr {
		t.Fatal("a path-like id was accepted")
	}
	out, isErr = text(call(t, h, "tools/call", map[string]any{"name": "automation_loeschen", "arguments": map[string]any{"id": "flur_licht_aus"}}))
	if isErr || store["flur_licht_aus"] != nil {
		t.Fatalf("loeschen: %q %v", out, store)
	}
	out, isErr = text(call(t, h, "tools/call", map[string]any{"name": "automation_schalten", "arguments": map[string]any{"id": "flur_aus", "an": false}}))
	if isErr || !strings.Contains(out, "turn_off") {
		t.Fatalf("schalten: %q", out)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Flur Licht aus": "flur_licht_aus", "Küche: Kaffee an!": "kueche_kaffee_an", "  Rolläden  hoch ": "rollaeden_hoch"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnauthorizedHA(t *testing.T) {
	ha, _ := fakeHA(t)
	defer ha.Close()
	h := Server(Config{HAURL: ha.URL, Token: "wrong"}, "test").Handler()
	if _, isErr := text(call(t, h, "tools/call", map[string]any{"name": "automationen", "arguments": map[string]any{}})); !isErr {
		t.Fatal("a 401 from HA must surface as a tool error")
	}
}
