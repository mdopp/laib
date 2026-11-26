package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
)

// Ablauf L-63 (homes ADR 0004 §3): the Werkstatt's hands, claude and pi,
// see laib_automat and reach the Bote's Automat through the door with
// typed arguments, a multi-line YAML as it is; homes does not see it, and
// the bote and ops hands are refused by laib-haushalt before the Automat.
// A change's code goes to the owner, box.yml notify.person, through
// bote-notify, never into the answer (laib#1284); the confirmed change is in
// the journal as its caller.
func TestTheWerkstattReachesTheAutomatThroughLaib(t *testing.T) {
	env, _ := testBox(t)
	var calls []map[string]any
	automat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k-automat" {
			w.WriteHeader(401)
			return
		}
		var req struct {
			Params map[string]any `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		calls = append(calls, req.Params)
		draft, _ := json.Marshal(map[string]string{"text": "Vorgemerkt, noch nicht geschrieben: Büro Licht aus (buero_licht_aus).", "code": "4711"})
		text := string(draft)
		if req.Params["name"] == "automation_bestaetigen" {
			text = "geschrieben: Büro Licht aus (buero_licht_aus) — automation.buero_licht_aus"
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": false}})
	}))
	t.Cleanup(automat.Close)
	var told []map[string]string
	notify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		json.NewDecoder(r.Body).Decode(&b)
		told = append(told, b)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "kanal": "signal"})
	}))
	t.Cleanup(notify.Close)
	box := filepath.Join(env.Paths.State, "box.yml")
	raw, _ := os.ReadFile(box)
	if strings.Contains(string(raw), "\nnotify:") || strings.Contains(string(raw), "\npeople:") {
		t.Fatalf("testBox's box.yml has notify or people now: write this test's into them:\n%s", raw)
	}
	os.WriteFile(box, append(raw, "notify:\n  person: martin\npeople:\n  martin: [\"signal:+491700000000\"]\n"...), 0o644)
	t.Setenv("BOTE_NOTIFY_URL", notify.URL)
	t.Setenv("BOTE_NOTIFY_TOKEN", "k-notify")
	t.Setenv("BOTE_AUTOMAT_URL", automat.URL)
	t.Setenv("BOTE_AUTOMAT_TOKEN", "k-automat")
	withHaushalt(t, env)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	has := func(hand string) bool {
		return slices.ContainsFunc(mcp.Tools(st, hand), func(tl map[string]any) bool { return tl["name"] == "laib_automat" })
	}
	if !has("claude") || !has("pi") || has("homes") || has("resident") {
		t.Fatalf("laib_automat in tools/list: claude %v, pi %v, homes %v, resident %v", has("claude"), has("pi"), has("homes"), has("resident"))
	}
	for _, tl := range mcp.Tools(st, "claude") {
		if d, _ := tl["description"].(string); tl["name"] == "laib_automat" && (!strings.Contains(d, "never into this answer") || !strings.Contains(d, "homes_laden") || !strings.Contains(d, "tag_scanned")) {
			t.Errorf("laib_automat's description lacks the rule or the phone's triggers: %s", d)
		}
	}
	y := "alias: Büro Licht aus\ntriggers:\n  - trigger: event\n    event_type: homes_laden\n    event_data: {person: mdopp, zustand: an, ladeart: kabellos}\nactions:\n  - action: light.turn_off\n    target: {entity_id: light.buero}\n"
	out, code := mcp.Call(context.Background(), env, st, "pi", "laib_automat", map[string]any{"action": "schreiben", "yaml": y})
	if code != cli.ExitOK || strings.Contains(out, "4711") || !strings.Contains(out, "auf Signal geschickt") || len(calls) != 1 {
		t.Fatalf("schreiben: exit %d: %s (%d calls)", code, out, len(calls))
	}
	if a, _ := calls[0]["arguments"].(map[string]any); calls[0]["name"] != "automation_schreiben" || a["yaml"] != y {
		t.Fatalf("the Automat got %v", calls[0])
	}
	if len(told) != 1 || told[0]["an"] != "martin" || !strings.Contains(told[0]["text"], "4711") {
		t.Fatalf("the code did not go to the owner: %v", told)
	}
	for _, hand := range []string{"bote", "ops"} {
		if out, code := mcp.Call(context.Background(), env, st, hand, "laib_automat", map[string]any{"action": "automationen"}); code == cli.ExitOK || !strings.Contains(out, "Werkstatt") {
			t.Errorf("%s reached the Automat through laib: exit %d: %s", hand, code, out)
		}
	}
	since := time.Now().Add(-time.Minute)
	if out, code := mcp.Call(context.Background(), env, st, "claude", "laib_automat", map[string]any{"action": "bestaetigen", "code": "4711"}); code != cli.ExitOK || !strings.Contains(out, "geschrieben") {
		t.Fatalf("bestaetigen: exit %d: %s", code, out)
	}
	if len(calls) != 2 {
		t.Fatalf("a refused hand reached the Automat: %v", calls)
	}
	es, _ := journal.Read(env.Paths.Journal, since)
	if i := slices.IndexFunc(es, func(e journal.Entry) bool {
		return e.Verb == "automat" && strings.Contains(e.Detail, "geschrieben: Büro Licht aus")
	}); i < 0 || es[i].Actor != "claude" || slices.ContainsFunc(es, func(e journal.Entry) bool { return strings.Contains(e.Detail, "4711") }) {
		t.Fatalf("the confirmed change is not journalled as claude: %+v", es)
	}
}
