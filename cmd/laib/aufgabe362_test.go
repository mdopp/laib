package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
)

// homes#362: Homes' one to-do list carries the owner's keys something on
// the box waits for — names and the template's words, never a value — and
// a task may lead to a key's form, closing itself once the key is set.
// Ablauf L-18 @52e0
func TestHomesSeesTheKeysTheBoxWaitsForAndATaskClosesOnTheKey(t *testing.T) {
	env, _ := testBox(t)
	t.Setenv("LAIB_DISK", "/nonexistent")
	svc := filepath.Join(env.Paths.State, "services", "whoami")
	owner := "    external: true\n    optional: true\n    owner: {label: \"Whoami: Post\", warum: \"Damit whoami Post liest.\", hilfe: \"Beim Anbieter.\"}\n"
	os.WriteFile(filepath.Join(svc, "laib.yml"), []byte("name: whoami\nsecrets:\n  - name: WHOAMI_TOKEN\n  - name: WHO_MAIL\n"+owner+
		"  - name: WHO_DISCOGS\n"+owner+"requires:\n  MAIL_HOST: [MAIL_PASSWORD]\n"), 0o644)
	os.WriteFile(filepath.Join(svc, "values.yml"), []byte("IMAGE: docker.io/traefik/whoami:v1.10\nTOKEN: !secret WHOAMI_TOKEN\nMAIL_HOST: mail.example\nMAIL_PASSWORD: !secret WHO_MAIL\nDISCOGS: !secret WHO_DISCOGS\n"), 0o644)
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	sec["WHO_DISCOGS"] = "geheim-discogs"
	delete(sec, "WHO_MAIL")
	secrets.Save(env.Paths.Secrets, env.Paths.Key, sec)

	type key struct{ Name, Dienst, Label, Warum, Hilfe, Link string }
	list := func() (keys []key, tasks []map[string]any, raw string) {
		t.Helper()
		out, code := mcp.Call(context.Background(), env, mustState(t, env), "homes", "laib_aufgabe", map[string]any{"action": "list", "von": "anna"})
		var got struct {
			Schluessel []key
			Aufgaben   []map[string]any
		}
		if code != cli.ExitOK || json.Unmarshal([]byte(out), &got) != nil {
			t.Fatalf("list: %d %s", code, out)
		}
		return got.Schluessel, got.Aufgaben, compact(out)
	}
	keys, _, raw := list()
	if len(keys) != 1 || keys[0] != (key{"WHO_MAIL", "whoami", "Whoami: Post", "Damit whoami Post liest.", "Beim Anbieter.", "https://laib.box.test/schluessel?name=WHO_MAIL"}) {
		t.Errorf("only the required, missing key, in the template's words: %+v", keys)
	}
	if strings.Contains(raw, "geheim") || strings.Contains(raw, "WHO_DISCOGS") {
		t.Errorf("a value, or an optional key nobody waits for, reached Homes: %s", raw)
	}

	// a task leads to the key's form and closes itself on the key
	out, code := mcp.Call(context.Background(), env, mustState(t, env), "claude", "laib_aufgabe", map[string]any{"action": "add", "titel": "Post-Passwort eintragen", "weg": "schluessel:WHO_MAIL"})
	if code != cli.ExitOK || !strings.Contains(compact(out), `"fertig_wenn":"schluessel WHO_MAIL"`) || !strings.Contains(out, "sobald der Schlüssel eingetragen ist") {
		t.Fatalf("add: %d %s", code, out)
	}
	if _, code := mcp.Call(context.Background(), env, mustState(t, env), "claude", "laib_aufgabe", map[string]any{"action": "add", "titel": "x", "weg": "schluessel:WHOAMI_TOKEN"}); code == cli.ExitOK {
		t.Error("a key the box makes itself is no owner's way")
	}
	_, tasks, raw := list()
	if len(tasks) != 1 || !strings.Contains(raw, `"weg":{"schluessel":"WHO_MAIL","link":"https://laib.box.test/schluessel?name=WHO_MAIL"}`) || !strings.Contains(raw, `"aufgabe":1`) {
		t.Errorf("the task's way is the key's form, the key names its task: %s", raw)
	}
	diag := func() {
		if out, code := mcp.Call(context.Background(), env, mustState(t, env), "ops", "laib_diag", map[string]any{}); code == cli.ExitUsage {
			t.Fatalf("diag: %s", out)
		}
	}
	diag()
	if _, tasks, _ = list(); tasks[0]["erledigt"] != nil {
		t.Fatal("closed while the key is missing")
	}
	sec["WHO_MAIL"] = "geheim-post"
	secrets.Save(env.Paths.Secrets, env.Paths.Key, sec)
	diag()
	if keys, tasks, _ = list(); len(keys) != 0 || len(tasks) != 0 {
		t.Errorf("the key is set: nothing waits, the task closed itself: %+v %+v", keys, tasks)
	}
}

func compact(s string) string {
	var b bytes.Buffer
	json.Compact(&b, []byte(s))
	return b.String()
}
