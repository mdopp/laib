package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/secrets"
)

// #55 (4): the manual Home Assistant wiring becomes a checklist diag can
// read through HA's API — "Wyoming Whisper: fehlt" instead of a README
// nobody re-reads.
func TestDiagNamesEachMissingHAIntegration(t *testing.T) {
	c := newCbox(t)
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ha-token-1" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path != "/api/config/config_entries/entry" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`[{"domain":"wyoming","title":"faster-whisper"},{"domain":"mqtt","title":"mosquitto"},{"domain":"caldav","title":"Radicale"}]`))
	}))
	defer ha.Close()
	sec, _ := secrets.Load(c.env.paths.Secrets, c.env.paths.Key)
	sec["HA_TOKEN"] = "ha-token-1"
	secrets.Save(c.env.paths.Secrets, c.env.paths.Key, sec)
	dir := filepath.Join(c.env.paths.State, "services", "home-assistant")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "laib.yml"), []byte("name: home-assistant\ntemplate: {registry: builtin, name: home-assistant, version: 1}\nroutes: []\nsecrets:\n  - name: HA_TOKEN\n    external: true\n    optional: true\ndata: []\nhealth: {http: "+ha.URL+"/api/}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "values.yml"), []byte("IMAGE: x\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "home-assistant.container"), []byte("[Container]\nImage=${IMAGE}\n"), 0o644)
	st, err := loadState(c.env)
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, f := range runProbes(context.Background(), c.env, st, "") {
		if f.Probe == "ha" {
			missing = append(missing, f.Detail)
		}
	}
	joined := strings.Join(missing, "\n")
	for _, want := range []string{"Wyoming Piper", "Wyoming openWakeWord", "Music Assistant", "MCP Server"} {
		if !strings.Contains(joined, want+": fehlt") {
			t.Fatalf("diag must name %q as missing:\n%s", want, joined)
		}
	}
	for _, have := range []string{"Wyoming Whisper: fehlt", "MQTT: fehlt", "CalDAV: fehlt"} {
		if strings.Contains(joined, have) {
			t.Fatalf("%q is configured and must not be reported:\n%s", have, joined)
		}
	}
}
