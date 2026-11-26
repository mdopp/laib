package haushalt

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Ablauf L-63 (homes ADR 0004 §3): the Werkstatt sets up Home Assistant's
// automations through Laib, never with a token of its own. laib automat
// hands each action to the Bote's Automat with the Automat's bearer; a
// change answers with the Automat's code and the reminder that the person
// says it, a confirmed one is journalled, and only the Werkstatt's hands
// (and humans) may call it. The fake answers as automat.Server does.
func TestAutomatForwardsToTheBotesAutomatWithItsBearer(t *testing.T) {
	type got struct {
		auth, tool string
		args       map[string]any
	}
	var calls []got
	automat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		calls = append(calls, got{r.Header.Get("Authorization"), req.Params.Name, req.Params.Arguments})
		text, isErr := "homes-0a1b2c3d4e5f · Bürotür", false
		switch req.Params.Name {
		case "automation_schreiben", "automation_schalten":
			text = "Vorgemerkt, noch nicht geschrieben: Büro Licht aus (buero_licht_aus). Zum Ausführen den Code 4711 bestätigen (automation_bestaetigen). Gilt zehn Minuten."
		case "automation_bestaetigen":
			text, isErr = "geschrieben: Büro Licht aus (buero_licht_aus) — automation.buero_licht_aus", req.Params.Arguments["code"] != "4711"
			if isErr {
				text = "kein vorgemerkter Vorgang mit diesem Code (oder älter als zehn Minuten)"
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": isErr}})
	}))
	t.Cleanup(automat.Close)
	bote := Bote{Automat: automat.URL, AutomatToken: "k-automat"}
	run := func(actor, stdin string, b Bote, args ...string) (string, string, int, string) {
		var out, errb, jr bytes.Buffer
		env := &Env{Call: Call{JSON: true, Actor: actor, Stdin: stdin}, Bote: b, Out: &out, Err: &errb, Journal: &jr}
		code := Run(context.Background(), env, append([]string{"automat"}, args...))
		return out.String(), errb.String(), code, jr.String()
	}

	if out, _, code, _ := run("claude", "", bote, "tags"); code != 0 || !strings.Contains(out, "Bürotür") || calls[0].tool != "tags" || calls[0].auth != "Bearer k-automat" {
		t.Fatalf("tags: %q %d %+v", out, code, calls)
	}
	y := "alias: Büro Licht aus\ntriggers:\n  - trigger: event\n    event_type: homes_laden\n    event_data: {person: mdopp, zustand: an, ladeart: kabellos}\nactions:\n  - action: light.turn_off\n    target: {entity_id: light.buero}\n"
	out, _, code, jr := run("pi", y, bote, "schreiben", "--id", "buero_licht_aus")
	if c := calls[len(calls)-1]; code != 0 || c.tool != "automation_schreiben" || c.args["yaml"] != y || c.args["id"] != "buero_licht_aus" {
		t.Fatalf("schreiben with the YAML on stdin: %d %+v", code, c)
	}
	if !strings.Contains(out, "4711") || !strings.Contains(out, "nie von dir aus") || jr != "" {
		t.Fatalf("a prepared change shows the code and says who confirms, and is no journal line yet: %q %q", out, jr)
	}
	run("claude", "", bote, "ausschalten", "--id", "nachtlicht")
	if c := calls[len(calls)-1]; c.tool != "automation_schalten" || c.args["an"] != false || c.args["id"] != "nachtlicht" {
		t.Fatalf("ausschalten: %+v", c)
	}
	if _, errs, code, jr := run("claude", "", bote, "bestaetigen", "--code", "0000"); code != exitRefused || !strings.Contains(errs, "kein vorgemerkter Vorgang") || jr != "" {
		t.Fatalf("a wrong code: %q %d %q", errs, code, jr)
	}
	if out, _, code, jr := run("claude", "", bote, "bestaetigen", "--code", "4711"); code != 0 || !strings.Contains(out, "geschrieben") || !strings.Contains(jr, "automat: geschrieben: Büro Licht aus") {
		t.Fatalf("confirmed: %q %d journal %q", out, code, jr)
	}

	n := len(calls)
	for _, actor := range []string{"bote", "ops", "homes", "haushalt"} {
		if _, errs, code, _ := run(actor, "", bote, "automationen"); code != exitRefused || !strings.Contains(errs, "Werkstatt") {
			t.Errorf("%s may not use laib automat: %q %d", actor, errs, code)
		}
	}
	if _, errs, code, _ := run("human:mdopp", "", Bote{}, "automationen"); code != exitRefused || !strings.Contains(errs, "kein Automat") {
		t.Errorf("without a Bote's Automat: %q %d", errs, code)
	}
	if _, _, code, _ := run("claude", "", bote, "starten"); code != 1 {
		t.Errorf("an unknown action: %d", code)
	}
	if len(calls) != n {
		t.Errorf("a refused call reached the Automat: %+v", calls[n:])
	}
	if _, errs, code, _ := run("claude", "", Bote{Automat: "http://127.0.0.1:1", AutomatToken: "k"}, "tags"); code != 1 || !strings.Contains(errs, "antwortet nicht") {
		t.Errorf("an Automat that does not answer: %q %d", errs, code)
	}
}
