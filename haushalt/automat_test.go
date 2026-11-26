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

// fakeAutomat answers as automat.Server with Config.Laib does: a change is a
// draft {text, code}, the code apart; bestaetigen takes only that code,
// verwerfen drops it.
func fakeAutomat(t *testing.T) (*httptest.Server, *[]map[string]any, map[string]bool) {
	var calls []map[string]any
	pending := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k-automat" {
			w.WriteHeader(401)
			return
		}
		var req struct {
			Params map[string]any `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		calls = append(calls, req.Params)
		args, _ := req.Params["arguments"].(map[string]any)
		text, isErr := "homes-0a1b2c3d4e5f · Bürotür", false
		switch req.Params["name"] {
		case "automation_schreiben", "automation_schalten":
			pending["4711"] = true
			b, _ := json.Marshal(map[string]string{"text": "Vorgemerkt, noch nicht geschrieben: Büro Licht aus (buero_licht_aus).", "code": "4711"})
			text = string(b)
		case "automation_verwerfen":
			delete(pending, args["code"].(string))
			text = "verworfen"
		case "automation_bestaetigen":
			text = "geschrieben: Büro Licht aus (buero_licht_aus) — automation.buero_licht_aus"
			if c, _ := args["code"].(string); !pending[c] {
				text, isErr = "kein vorgemerkter Vorgang mit diesem Code (oder älter als zehn Minuten)", true
			}
			delete(pending, args["code"].(string))
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": isErr}})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, pending
}

// fakeNotify is bote-notify: 200 {kanal} for mdopp, refuses with status otherwise.
func fakeNotify(t *testing.T, status int) (*httptest.Server, *[]map[string]string) {
	var sent []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		json.NewDecoder(r.Body).Decode(&b)
		sent = append(sent, b)
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"ok": status == 200, "kanal": "signal"})
	}))
	t.Cleanup(srv.Close)
	return srv, &sent
}

func runAutomat(actor, person, stdin string, b Bote, args ...string) (string, string, int, string) {
	var out, errb, jr bytes.Buffer
	env := &Env{Call: Call{JSON: true, Actor: actor, Stdin: stdin, Person: person}, Bote: b, Out: &out, Err: &errb, Journal: &jr}
	code := Run(context.Background(), env, append([]string{"automat"}, args...))
	return out.String(), errb.String(), code, jr.String()
}

// Ablauf L-63 (homes ADR 0004 §3, laib#1284): the Werkstatt sets up Home
// Assistant's automations through Laib, never with a token of its own.
// laib automat hands each action to the Bote's Automat with its bearer; a
// change's code goes to the owner (box.yml notify.person) through
// bote-notify and never into the answer, which says where it went; the
// owner says it, bestaetigen runs it and the journal names it. Only the
// Werkstatt's hands (and humans) may call it.
func TestAutomatSendsTheCodeToTheOwnerNeverToTheAgent(t *testing.T) {
	automat, calls, pending := fakeAutomat(t)
	notify, sent := fakeNotify(t, 200)
	bote := Bote{URL: notify.URL, Token: "k-notify", Automat: automat.URL, AutomatToken: "k-automat"}

	if out, _, code, _ := runAutomat("claude", "mdopp", "", bote, "tags"); code != 0 || !strings.Contains(out, "Bürotür") || (*calls)[0]["name"] != "tags" || len(*sent) != 0 {
		t.Fatalf("tags: %q %d %+v", out, code, *calls)
	}
	y := "alias: Büro Licht aus\ntriggers:\n  - trigger: event\n    event_type: homes_laden\n    event_data: {person: mdopp, zustand: an, ladeart: kabellos}\nactions:\n  - action: light.turn_off\n"
	out, errs, code, jr := runAutomat("pi", "mdopp", y, bote, "schreiben", "--id", "buero_licht_aus")
	c := (*calls)[len(*calls)-1]
	if a, _ := c["arguments"].(map[string]any); code != 0 || c["name"] != "automation_schreiben" || a["yaml"] != y || a["id"] != "buero_licht_aus" {
		t.Fatalf("schreiben with the YAML on stdin: %d %q %+v", code, errs, c)
	}
	if strings.Contains(out+errs, "4711") || !strings.Contains(out, "Ich habe dir den Code auf Signal geschickt. Sag ihn mir, dann speichere ich.") || !strings.Contains(out, "Büro Licht aus") {
		t.Fatalf("the answer holds the code or does not say where it went: %q", out)
	}
	if len(*sent) != 1 || (*sent)[0]["an"] != "mdopp" || !strings.Contains((*sent)[0]["text"], "4711") || strings.Contains(jr, "4711") {
		t.Fatalf("the code did not go to the owner alone: %+v, journal %q", *sent, jr)
	}
	if _, errs, code, jr := runAutomat("claude", "mdopp", "", bote, "bestaetigen", "--code", "0000"); code != exitRefused || !strings.Contains(errs, "kein vorgemerkter Vorgang") || jr != "" {
		t.Fatalf("a wrong code: %q %d %q", errs, code, jr)
	}
	if out, _, code, jr := runAutomat("claude", "mdopp", "", bote, "bestaetigen", "--code", "4711"); code != 0 || !strings.Contains(out, "geschrieben") || !strings.Contains(jr, "automat: geschrieben: Büro Licht aus") {
		t.Fatalf("confirmed: %q %d journal %q", out, code, jr)
	}
	runAutomat("claude", "mdopp", "", bote, "ausschalten", "--id", "nachtlicht")
	if c := (*calls)[len(*calls)-1]; c["name"] != "automation_schalten" || c["arguments"].(map[string]any)["an"] != false {
		t.Fatalf("ausschalten: %+v", c)
	}
	delete(pending, "4711")

	// the code cannot go: nothing stays drafted, the answer says why and holds no code
	down, _ := fakeNotify(t, 502)
	for _, b := range []Bote{{URL: down.URL, Token: "k", Automat: automat.URL, AutomatToken: "k-automat"}, {URL: "http://127.0.0.1:1", Token: "k", Automat: automat.URL, AutomatToken: "k-automat"}} {
		out, errs, code, _ := runAutomat("claude", "mdopp", y, b, "schreiben")
		if code != exitRefused || strings.Contains(out+errs, "4711") || !strings.Contains(errs, "Nichts vorgemerkt") || len(pending) != 0 {
			t.Fatalf("a failed tell: %q %q %d, still pending %v", out, errs, code, pending)
		}
	}
	n := len(*calls)
	if _, errs, code, _ := runAutomat("claude", "", y, bote, "schreiben"); code != exitRefused || !strings.Contains(errs, "notify.person") || len(*calls) != n {
		t.Fatalf("without an owner to send to, nothing reaches the Automat: %q %d", errs, code)
	}
	for _, actor := range []string{"bote", "ops", "homes", "haushalt"} {
		if _, errs, code, _ := runAutomat(actor, "mdopp", "", bote, "automationen"); code != exitRefused || !strings.Contains(errs, "Werkstatt") {
			t.Errorf("%s may not use laib automat: %q %d", actor, errs, code)
		}
	}
	if _, errs, code, _ := runAutomat("human:mdopp", "mdopp", "", Bote{}, "automationen"); code != exitRefused || !strings.Contains(errs, "kein Automat") {
		t.Errorf("without a Bote's Automat: %q %d", errs, code)
	}
	if _, _, code, _ := runAutomat("claude", "mdopp", "", bote, "starten"); code != 1 {
		t.Errorf("an unknown action: %d", code)
	}
	if len(*calls) != n {
		t.Errorf("a refused call reached the Automat: %+v", (*calls)[n:])
	}
	if _, errs, code, _ := runAutomat("claude", "mdopp", "", Bote{Automat: "http://127.0.0.1:1", AutomatToken: "k"}, "tags"); code != 1 || !strings.Contains(errs, "antwortet nicht") {
		t.Errorf("an Automat that does not answer: %q %d", errs, code)
	}
}

// An Automat that answers a change in the Bote's form, with the code in its
// text, is never passed on to the agent (laib#1284).
func TestAnOldFormDraftNeverReachesTheAgent(t *testing.T) {
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"content": []map[string]string{{"text": "Vorgemerkt … Zum Ausführen den Code 4711 bestätigen."}}}})
	}))
	t.Cleanup(old.Close)
	notify, sent := fakeNotify(t, 200)
	out, errs, code, _ := runAutomat("claude", "mdopp", "", Bote{URL: notify.URL, Token: "k", Automat: old.URL, AutomatToken: "k"}, "loeschen", "--id", "x")
	if code != exitRefused || strings.Contains(out+errs, "4711") || len(*sent) != 0 {
		t.Fatalf("an old-form draft: %q %q %d %v", out, errs, code, *sent)
	}
}
