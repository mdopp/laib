package lease

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// #157: the lease contract (ADR 0011, amendment 2026-10-14). These tests
// read the shipped presets.json, so a window named after a consumer, or an
// old name kept for one, fails here.

func shippedConfig(t *testing.T, router string) Config {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "templates", "llama", "presets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cfg.Units, cfg.RouterURL, cfg.LeaseDir = realUnits(t), router, filepath.Join(home, "lease")
	cfg.AutoIdle, cfg.Token, cfg.Renew, cfg.WarnAfter = time.Minute, "lease-secret", 10*time.Minute, 20*time.Minute
	cfg.Paths.Home, cfg.Paths.Journal = home, filepath.Join(home, "journal")
	return cfg
}

func contractServer(t *testing.T, router string) *httptest.Server {
	t.Helper()
	Systemctl = func(context.Context, string, []string) error { return nil }
	s, err := New(shippedConfig(t, router))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, method, url, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// has fails unless every key is present: the contract names them, and a
// consumer reads them by name.
func has(t *testing.T, what string, out map[string]any, keys ...string) {
	t.Helper()
	var missing []string
	for _, k := range keys {
		if _, ok := out[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%s lacks %v: %v", what, missing, out)
	}
}

func TestTheWindowsAreNamedForWhatTheyDoToTheCard(t *testing.T) {
	cfg := shippedConfig(t, "http://127.0.0.1:1")
	var modes []string
	for m := range cfg.Modes {
		modes = append(modes, m)
	}
	sort.Strings(modes)
	if strings.Join(modes, " ") != "full shared" {
		t.Fatalf("windows %v, want full and shared (exclusive is built in)", modes)
	}
	if !slices.Contains(cfg.Modes["shared"].Presets, "gemma-4-12b") || len(cfg.Modes["shared"].Stops) > 0 {
		t.Fatalf("shared is the 12B beside the voice stack, nothing stops: %+v", cfg.Modes["shared"])
	}
}

func TestTheLeaseContract(t *testing.T) {
	srv := contractServer(t, router42(t).URL)
	api := srv.URL + "/api/model-lease"

	// the key: taking or giving back the card is not for anyone on the LAN (#42)
	if code, out := call(t, "POST", api, "", `{"model":"gemma-4-12b","ttl_s":900,"holder":"chronicle"}`); code != 401 || out["ok"] != false || out["reason"] != "unauthorized" {
		t.Fatalf("no key: %d %v", code, out)
	}
	// a closed payload: every refusal names its reason
	for body, reason := range map[string]string{
		`not json`: "invalid_json",
		`[1]`:      "invalid_payload",
		`{"model":"gemma-4-12b","holder":"chronicle","session":"r7"}`: "unexpected_field",
		`{"holder":"chronicle"}`:                     "invalid_model",
		`{"model":"nope","holder":"chronicle"}`:      "invalid_model",
		`{"model":"gemma-4-12b","ttl_s":"900"}`:      "invalid_ttl",
		`{"model":"gemma-4-12b","ttl_s":0}`:          "invalid_ttl",
		`{"model":"gemma-4-12b","holder":"Runde 7"}`: "invalid_holder",
		`{"model":"gemma-4-12b"}`:                    "invalid_holder",
		`{"model":"foundry","holder":"chronicle"}`:   "invalid_model",
		`{"model":"thinking","holder":"bote"}`:       "invalid_model",
	} {
		if code, out := call(t, "POST", api, "lease-secret", body); code != 400 || out["ok"] != false || out["reason"] != reason {
			t.Errorf("%s: %d %v, want 400 %s", body, code, out, reason)
		}
	}

	// GET with nothing held
	code, out := call(t, "GET", api, "", "")
	if code != 200 || out["state"] != "none" || out["holder"] != "" || out["model"] != "" || out["alias"] != "qwen3.6-35b-a3b" || out["expires_at"] != nil {
		t.Fatalf("idle GET: %d %v", code, out)
	}
	has(t, "idle GET", out, "state", "model", "alias", "expires_at", "holder", "last_renewed_at", "renew_after", "retry_after")

	// a consumer asks for a model; the lease picks the narrowest window
	code, out = call(t, "POST", api, "lease-secret", `{"model":"gemma-4-12b","ttl_s":900,"holder":"chronicle"}`)
	if code != 200 || out["ok"] != true || out["state"] != "ready" || out["model"] != "shared" || out["alias"] != "gemma-4-12b" {
		t.Fatalf("POST: %d %v", code, out)
	}
	has(t, "ready POST", out, "expires_at", "renew_after")
	if ra, _ := out["renew_after"].(float64); ra != 300 {
		t.Fatalf("renew_after %v, want 300 (a third of 900 s, within the 10 min grace)", out["renew_after"])
	}
	if exp, _ := out["expires_at"].(float64); exp < float64(time.Now().Unix()+800) || exp > float64(time.Now().Unix()+1000) {
		t.Fatalf("expires_at %v is not the ttl's end in epoch seconds", out["expires_at"])
	}

	code, out = call(t, "GET", api, "", "")
	if code != 200 || out["state"] != "ready" || out["model"] != "shared" || out["holder"] != "chronicle" || out["alias"] != "gemma-4-12b" {
		t.Fatalf("held GET: %d %v", code, out)
	}
	has(t, "held GET", out, "expires_at", "last_renewed_at", "renew_after", "retry_after")

	// the same POST renews
	if code, out = call(t, "POST", api, "lease-secret", `{"model":"gemma-4-12b","ttl_s":900,"holder":"chronicle"}`); code != 200 || out["state"] != "ready" || out["model"] != "shared" {
		t.Fatalf("renewal: %d %v", code, out)
	}

	// somebody else: refused with who holds it, until when, and what is allowed
	code, out = call(t, "POST", api, "lease-secret", `{"model":"qwen3.6-35b-a3b","holder":"pi"}`)
	if code != 409 || out["ok"] != false || out["reason"] != "held" || out["holder"] != "chronicle" || out["mode"] != "shared" {
		t.Fatalf("foreign POST: %d %v", code, out)
	}
	has(t, "409", out, "expires_at", "allowed")
	code, out = call(t, "DELETE", api, "lease-secret", `{"holder":"pi"}`)
	if code != 409 || out["ok"] != false || out["reason"] != "held" || out["holder"] != "chronicle" {
		t.Fatalf("foreign DELETE: %d %v", code, out)
	}
	if code, out = call(t, "DELETE", api, "", `{"holder":"chronicle"}`); code != 401 {
		t.Fatalf("DELETE without the key: %d %v", code, out)
	}

	// the holder gives it back
	if code, out = call(t, "DELETE", api, "lease-secret", `{"holder":"chronicle"}`); code != 200 || out["ok"] != true || out["state"] != "released" {
		t.Fatalf("own DELETE: %d %v", code, out)
	}
	if _, out = call(t, "GET", api, "", ""); out["state"] != "none" {
		t.Fatalf("after DELETE: %v", out)
	}
	if code, out = call(t, "DELETE", api, "lease-secret", `{"holder":"chronicle","x":1}`); code != 400 || out["reason"] != "unexpected_field" {
		t.Fatalf("DELETE with an unknown field: %d %v", code, out)
	}
}

// A preset that is still loading answers 202 and how long to wait; the
// consumer polls GET for ready instead of posting again.
func TestAPreparingWindowSaysWhenToAskAgain(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // the weights are still loading
	}))
	t.Cleanup(slow.Close)
	srv := contractServer(t, slow.URL)
	code, out := call(t, "POST", srv.URL+"/api/model-lease", "lease-secret", `{"model":"gemma-4-12b","ttl_s":900,"holder":"chronicle"}`)
	if code != 202 || out["ok"] != true || out["state"] != "preparing" || out["model"] != "shared" || out["retry_after"] != float64(30) || out["expires_at"] != nil {
		t.Fatalf("preparing POST: %d %v", code, out)
	}
	if _, out = call(t, "GET", srv.URL+"/api/model-lease", "", ""); out["state"] != "preparing" || out["holder"] != "chronicle" || out["retry_after"] != float64(30) {
		t.Fatalf("preparing GET: %v", out)
	}
}
