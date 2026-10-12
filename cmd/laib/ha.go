package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/state"
)

// Home Assistant's integrations are set up by hand once (templates/home-
// assistant/README.md). With a long-lived token in the secret HA_TOKEN,
// diag reads HA's config entries and names what is still missing (#55):
// the checklist the Box page can show, "Wyoming Whisper: fehlt".

type haEntry struct {
	Domain string `json:"domain"`
	Title  string `json:"title"`
}

// haExpected: what ADR 0015 wires into HA — a domain and a word of the
// entry's title (Wyoming has three entries of one domain).
var haExpected = []struct{ Name, Domain, Title string }{
	{"Wyoming Whisper", "wyoming", "whisper"},
	{"Wyoming Piper", "wyoming", "piper"},
	{"Wyoming openWakeWord", "wyoming", "wake"},
	{"CalDAV", "caldav", ""},
	{"Music Assistant", "music_assistant", ""},
	{"MQTT", "mqtt", ""},
	{"MCP Server", "mcp_server", ""},
}

// haEntries is the seam: HA's config entries through its REST API.
var haEntries = func(ctx context.Context, base, token string) ([]haEntry, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(base, "/")+"/api/config/config_entries/entry", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HA answered %s", resp.Status)
	}
	var out []haEntry
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func haFindings(ctx context.Context, st *state.State, sec map[string]string) []Finding {
	svc, ok := st.Services["home-assistant"]
	token := sec["HA_TOKEN"]
	if !ok || token == "" {
		return nil
	}
	base := "http://127.0.0.1:8123"
	if h := svc.Contract.Health.HTTP; h != "" {
		if i := strings.Index(h, "/api"); i > 0 {
			base = h[:i]
		}
	}
	entries, err := haEntries(ctx, base, token)
	if err != nil {
		return []Finding{{Severity: "gelb", Probe: "ha", Service: "home-assistant", Detail: "Home Assistant antwortet nicht auf die Integrationsliste: " + err.Error(), Fix: "HA_TOKEN prüfen (langlebiger Token eines Admin-Kontos)"}}
	}
	var out []Finding
	for _, want := range haExpected {
		found := false
		for _, e := range entries {
			if e.Domain == want.Domain && (want.Title == "" || strings.Contains(strings.ToLower(e.Title), want.Title)) {
				found = true
			}
		}
		if !found {
			out = append(out, Finding{Severity: "gelb", Probe: "ha", Service: "home-assistant", Detail: want.Name + ": fehlt", Fix: "in Home Assistant unter Einstellungen → Integrationen einrichten (templates/home-assistant/README.md)"})
		}
	}
	return out
}
