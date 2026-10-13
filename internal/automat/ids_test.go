package automat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #155: schalten checked no id; wiederherstellen without a data dir blamed the id.
func TestSchaltenRefusesAnIdTheOthersRefuse(t *testing.T) {
	called := false
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.Write([]byte("[]")) }))
	defer ha.Close()
	h := Server(Config{HAURL: ha.URL, Token: "tok", Data: t.TempDir()}, "test").Handler()
	for _, id := range []string{"flur_aus, light.kueche", "../x", ""} {
		out, isErr := text(call(t, h, "tools/call", map[string]any{"name": "automation_schalten", "arguments": map[string]any{"id": id, "an": true}}))
		if !isErr || called {
			t.Fatalf("id %q: switched (HA called %v): %q", id, called, out)
		}
	}
}

func TestWiederherstellenWithoutADataDirSaysSo(t *testing.T) {
	h := Server(Config{HAURL: "http://127.0.0.1:1", Token: "tok"}, "test").Handler()
	out, isErr := text(call(t, h, "tools/call", map[string]any{"name": "automation_wiederherstellen", "arguments": map[string]any{"id": "flur_aus"}}))
	if !isErr || strings.Contains(out, "nur a-z") {
		t.Fatalf("a valid id was blamed for a missing data dir: %q", out)
	}
}
