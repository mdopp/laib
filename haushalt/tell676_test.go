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

// #676 (haus#123): `laib tell <person> "<text>"` — a household page tells
// one person something (a new episode of a podcast they subscribed, later
// the shopping list), through bote-notify, which picks that person's first
// reachable channel from box.yml people. The answer is {sent, message}, in
// German, whatever happened; the journal names the person and the
// channel, never the text. A fake bote-notify with its real answers
// (templates/bote/notify.py): 200 {ok, kanal}, 404 unknown person, 429,
// 502 no channel took it.
func TestTellSendsOneMessageToOnePersonThroughTheBote(t *testing.T) {
	type got struct {
		auth string
		body map[string]any
	}
	var calls []got
	answer := map[string]func(http.ResponseWriter){
		"mama": func(w http.ResponseWriter) { json.NewEncoder(w).Encode(map[string]any{"ok": true, "kanal": "signal"}) },
		"papa": func(w http.ResponseWriter) {
			w.WriteHeader(502)
			json.NewEncoder(w).Encode(map[string]string{"fehler": "x"})
		},
		"lisa": func(w http.ResponseWriter) {
			w.WriteHeader(429)
			json.NewEncoder(w).Encode(map[string]string{"fehler": "x"})
		},
	}
	bote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		calls = append(calls, got{r.Header.Get("Authorization"), b})
		if r.URL.Path != "/mitteilung" {
			w.WriteHeader(404)
			return
		}
		f, ok := answer[b["an"].(string)]
		if !ok {
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]string{"fehler": "unbekannte Person"})
			return
		}
		f(w)
	}))
	t.Cleanup(bote.Close)
	call := func(b Bote, args ...string) (map[string]any, int, string) {
		var out, errb, jr bytes.Buffer
		env := &Env{Call: Call{JSON: true, Actor: "haus"}, Bote: b, Out: &out, Err: &errb, Journal: &jr}
		code := Run(context.Background(), env, append([]string{"tell"}, args...))
		var reply map[string]any
		if out.Len() > 0 {
			if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
				t.Fatalf("not JSON: %s", out.String())
			}
		}
		return reply, code, errb.String() + "|" + jr.String()
	}
	b := Bote{URL: bote.URL, Token: "k-notify"}

	r, code, rest := call(b, "mama", "Neue Folge von Gartenfunk: Tomaten im Herbst")
	if code != exitOK || r["sent"] != true || !strings.Contains(r["message"].(string), "über signal") {
		t.Fatalf("exit %d: %v %s", code, r, rest)
	}
	if len(calls) != 1 || calls[0].auth != "Bearer k-notify" || calls[0].body["an"] != "mama" || calls[0].body["text"] != "Neue Folge von Gartenfunk: Tomaten im Herbst" || len(calls[0].body) != 2 {
		t.Fatalf("bote-notify got %+v", calls)
	}
	if !strings.Contains(rest, `"detail":"an mama über signal"`) || strings.Contains(rest, "Tomaten") {
		t.Errorf("the journal must name the person and the channel, never the text: %s", rest)
	}

	// what did not go out is an answer too, sent false, in words; nothing on stderr (MCP appends it to the JSON)
	for person, want := range map[string]string{"papa": "Kein Kanal", "lisa": "Zu viele", "oma": "box.yml people"} {
		r, code, rest = call(b, person, "Hallo")
		if code == exitOK || r["sent"] != false || !strings.Contains(r["message"].(string), want) || !strings.HasPrefix(rest, "|") {
			t.Errorf("%s: exit %d: %v %s", person, code, r, rest)
		}
	}
	r, code, _ = call(Bote{}, "mama", "Hallo")
	if code == exitOK || r["sent"] != false || !strings.Contains(r["message"].(string), "kein Bote") {
		t.Errorf("without a Bote: exit %d: %v", code, r)
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	r, code, _ = call(Bote{URL: gone.URL, Token: "k"}, "mama", "Hallo")
	if code == exitOK || r["sent"] != false || !strings.Contains(r["message"].(string), "antwortet nicht") {
		t.Errorf("a Bote that does not answer: exit %d: %v", code, r)
	}

	// not a person's name, no text, too long: refused before anything goes out
	n := len(calls)
	for _, args := range [][]string{{"mama"}, {"Mama Müller", "Hallo"}, {"mama", "  "}, {"mama", strings.Repeat("a", 1001)}} {
		if _, code, _ := call(b, args...); code == exitOK {
			t.Errorf("%q: exit 0", args)
		}
	}
	if len(calls) != n {
		t.Errorf("a refused call reached the Bote: %+v", calls[n:])
	}
	// the text may come as several words on the CLI
	if r, code, _ := call(b, "mama", "Neue", "Folge"); code != exitOK || calls[len(calls)-1].body["text"] != "Neue Folge" {
		t.Errorf("exit %d: %v", code, r)
	}
}
