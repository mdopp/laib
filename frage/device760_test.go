package frage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #760: „Schalte Sofa-Licht aus“ was heard right, missed HA's own intents (the
// light is „Sofalicht“) and reached Frage, which searched the web and
// shrugged. A sentence shaped like a device command is answered honestly:
// no search, no switching (ADR 0015: devices are HA's intents), the nearest
// of the names HA renders into the agent's prompt („Geräte: …“).
func TestADeviceCommandIsAnsweredHonestlyWithoutASearch(t *testing.T) {
	searched := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { searched = true; w.Write([]byte(`{"results":[]}`)) }))
	defer s.Close()
	srv := httptest.NewServer(Handler(Config{SearxURL: s.URL, ModelURL: "http://127.0.0.1:1/v1"}))
	defer srv.Close()
	system := "Antworte kurz, auf Deutsch, zum Vorlesen.\nGeräte: Sofalicht; Deckenlampe; Kaffeemaschine; Rollo Küche\nCurrent time is 18:42:24."
	ask := func(sys, q string) string {
		t.Helper()
		msgs := []map[string]string{{"role": "user", "content": q}}
		if sys != "" {
			msgs = append([]map[string]string{{"role": "system", "content": sys}}, msgs...)
		}
		body, _ := json.Marshal(map[string]any{"model": "frage", "stream": false, "messages": msgs})
		resp, err := http.Post(srv.URL+"/api/chat", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		var one struct {
			Message struct{ Content string } `json:"message"`
		}
		json.NewDecoder(resp.Body).Decode(&one)
		return one.Message.Content
	}
	for q, want := range map[string]string{
		"Schalte Sofa-Licht aus.":         "Meinst du „Sofalicht“?",
		"Schalte Sofa Licht an":           "Meinst du „Sofalicht“?",
		"Mach das Sofa-Dicht aus":         "Meinst du „Sofalicht“?", // Whisper's first try
		"Mach die Deckenlampen an":        "Meinst du „Deckenlampe“?",
		"Stell das Rollo in der Küche zu": "Meinst du „Rollo Küche“?",
		"Schalte den Toaster ein":         "„Toaster“ habe ich nicht gefunden: welches Gerät meinst du?",
		"Licht aus":                       "„Licht“ habe ich nicht gefunden: welches Gerät meinst du?",
		"Schalte aus":                     "Welches Gerät meinst du?",
	} {
		if got := ask(system, q); !strings.Contains(got, want) {
			t.Errorf("%q: %q, want %q", q, got, want)
		}
	}
	if got := ask("", "Schalte Sofa-Licht aus"); !strings.Contains(got, "„Sofa-Licht“ habe ich nicht gefunden: welches Gerät meinst du?") {
		t.Errorf("without the names (the owner's own prompt): %q", got)
	}
	if searched {
		t.Error("a device command went to the web search")
	}
	for _, q := range []string{"Was ist ein Kolibri?", "Ist das Licht an?", "Wer hat das Licht erfunden"} {
		ask(system, q)
		if !searched {
			t.Errorf("%q is a question and was not searched", q)
		}
		searched = false
	}
}
