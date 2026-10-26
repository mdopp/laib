package lease

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #395: the router runs with --models-dir beside --models-preset, so its
// /v1/models lists every GGUF file next to the presets. pi takes the first
// entry, sends a raw file id, and the proxy refuses it. The proxy's
// catalogue lists exactly the presets, the household's first; a raw id is
// a 404 that names the presets.
func TestTheCatalogueIsThePresetsOnly(t *testing.T) {
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[{"id":"Qwen3.6-35B-A3B-UD-IQ3_XXS"},{"id":"gemma-4-12b","status":{"value":"loaded"}},{"id":"mtp-gemma-4-E4B-it-Q8_0"},{"id":"gemma-4-e4b"},{"id":"nomic-embed-text-v1.5.Q8_0"},{"id":"qwen3.6-35b-a3b"}]}`))
	}))
	t.Cleanup(router.Close)
	cfg := config42(t, router.URL)
	cfg.Household = "qwen3.6-35b-a3b"
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&listing)
	var ids []string
	for _, m := range listing.Data {
		ids = append(ids, m["id"].(string))
	}
	if got := strings.Join(ids, ","); got != "qwen3.6-35b-a3b,gemma-4-e4b,gemma-4-12b" {
		t.Fatalf("/v1/models lists %s, want the presets, the household's first", got)
	}
	if st, _ := listing.Data[2]["status"].(map[string]any); st["value"] != "loaded" {
		t.Errorf("the router's own fields of a preset are lost: %v", listing.Data[2])
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(`{"model":"Qwen3.6-35B-A3B-UD-IQ3_XXS","messages":[]}`))
	req.Header.Set("Authorization", "Bearer lease-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Error struct{ Message, Code string } `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusNotFound || out.Error.Code != "model_not_found" {
		t.Fatalf("a raw file id answers %d %+v, want 404 model_not_found", resp.StatusCode, out)
	}
	for _, p := range []string{"qwen3.6-35b-a3b", "gemma-4-e4b", "gemma-4-12b"} {
		if !strings.Contains(out.Error.Message, p) {
			t.Errorf("the refusal does not name %s: %s", p, out.Error.Message)
		}
	}
}
