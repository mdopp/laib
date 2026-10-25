package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/policy"
)

// #676 (haus#123): Haus tells one person something — the subscriber of a
// podcast its new episode — with laib_tell {person, text}, served by
// laib-haushalt behind the verb door (ADR 0026), which hands it to
// bote-notify. Haus offers the message once laib_tell is in its
// tools/list, so the haus hand has the verb from DefaultPolicy on (an old
// box gets it as the owner's proposal, #575); a read hand never reaches
// the program. The kernel journals the call as the caller, without the text.
func TestHausTellsOnePersonThroughTheDoor(t *testing.T) {
	env, _ := testBox(t)
	var sent []map[string]string
	bote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		json.NewDecoder(r.Body).Decode(&b)
		if r.Header.Get("Authorization") != "Bearer k-notify" {
			w.WriteHeader(401)
			return
		}
		sent = append(sent, b)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "kanal": "signal"})
	}))
	t.Cleanup(bote.Close)
	t.Setenv("BOTE_NOTIFY_URL", bote.URL)
	t.Setenv("BOTE_NOTIFY_TOKEN", "k-notify")
	withHaushalt(t, env)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.KnownVerbs["tell"] || !mcp.Allowed["tell"] {
		t.Fatal("tell is not a verb an agent may reach and policy.yml may name")
	}
	if !slices.ContainsFunc(mcp.Tools(st, "haus"), func(tool map[string]any) bool { return tool["name"] == "laib_tell" }) {
		t.Fatal("laib_tell is not in the haus hand's tools/list: Haus never offers the message")
	}
	if _, code := mcp.Call(context.Background(), env, st, "resident", "laib_tell", map[string]any{"person": "mama", "text": "x"}); code != cli.ExitRefused || len(sent) != 0 {
		t.Fatalf("a read hand told someone: exit %d, %v", code, sent)
	}
	since := time.Now().Add(-time.Minute)
	out, code := mcp.Call(context.Background(), env, st, "haus", "laib_tell", map[string]any{"person": "mama", "text": "Neue Folge von Gartenfunk: Tomaten im Herbst"})
	var r struct {
		Sent    bool   `json:"sent"`
		Message string `json:"message"`
	}
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &r) != nil || !r.Sent || r.Message == "" {
		t.Fatalf("exit %d: %s", code, out)
	}
	if len(sent) != 1 || sent[0]["an"] != "mama" || sent[0]["text"] != "Neue Folge von Gartenfunk: Tomaten im Herbst" {
		t.Fatalf("bote-notify got %v", sent)
	}
	es, _ := journal.Read(env.Paths.Journal, since)
	i := slices.IndexFunc(es, func(e journal.Entry) bool { return e.Verb == "tell" })
	if i < 0 || es[i].Actor != "haus" || !strings.Contains(es[i].Detail, "mama") || strings.Contains(es[i].Detail, "Tomaten") {
		t.Fatalf("not journaled as haus, without the text: %+v", es)
	}
}
