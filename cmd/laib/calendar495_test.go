package main

import (
	"context"
	"encoding/base64"
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

const secretICS = "https://calendar.google.com/calendar/ical/lea%40gmail.com/private-0123456789abcdef/basic.ics"

// grepTree says whether any file below dir contains s.
func grepTree(dir, s string) bool {
	found := false
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), s) {
				found = true
			}
		}
		return nil
	})
	return found
}

// #495: Haus hands a Google calendar's secret iCal address to laib once
// (laib_calendar, the address as stdin). It lands in secrets.age as
// KALENDER_ABOS, the kalender service is applied with it, and no answer,
// journal line or argument carries it.
func TestHausSubscribesACalendarAndTheAddressStaysInSecrets(t *testing.T) {
	env, _ := testBox(t)
	svc := filepath.Join(env.Paths.State, "services", "kalender")
	os.MkdirAll(svc, 0o755)
	os.WriteFile(filepath.Join(svc, "laib.yml"), []byte("name: kalender\nsecrets:\n  - name: KALENDER_ABOS\n    external: true\n    optional: true\nhealth:\n  none: a timer\n"), 0o644)
	os.WriteFile(filepath.Join(svc, "values.yml"), []byte("ABOS: !secret KALENDER_ABOS\n"), 0o644)
	os.WriteFile(filepath.Join(svc, "kalender-abgleich.service"), []byte("[Service]\nType=oneshot\nEnvironment=KALENDER_ABOS=${ABOS}\nExecStart=/bin/true\n"), 0o644)
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	call := func(args map[string]any) (string, int) {
		st, err := cli.LoadState(env)
		if err != nil {
			t.Fatal(err)
		}
		return mcp.Call(context.Background(), env, st, "haus", "laib_calendar", args)
	}

	out, code := call(map[string]any{"action": "subscribe", "person": "lea", "name": "familie", "kind": "google", "title": "Familie", "stdin": secretICS + "\n"})
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if strings.Contains(out, "private-") {
		t.Fatalf("the answer carries the secret address: %s", out)
	}
	var got struct {
		Message string `json:"message"`
		Abos    []Abo  `json:"abos"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Abos) != 1 || got.Abos[0].Kind != "google" || got.Abos[0].Since == "" || !strings.Contains(got.Message, "verbunden") {
		t.Fatalf("%v: %s", err, out)
	}
	sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	raw, _ := base64.RawURLEncoding.DecodeString(sec["KALENDER_ABOS"])
	if !strings.Contains(string(raw), "private-0123456789abcdef") {
		t.Fatalf("not in secrets.age: %q", raw)
	}
	if grepTree(env.Paths.Journal, "private-") {
		t.Fatal("the journal carries the secret address")
	}
	if !grepTree(env.Paths.Quadlet, sec["KALENDER_ABOS"]) && !grepTree(env.Paths.Systemd, sec["KALENDER_ABOS"]) {
		t.Fatal("the kalender unit was not applied with the new subscriptions")
	}

	// not Google's secret address, or no address at all: refused, nothing stored
	for _, bad := range []string{"https://example.org/x.ics", "", "http://calendar.google.com/a/basic.ics"} {
		if out, code := call(map[string]any{"action": "subscribe", "person": "lea", "name": "arbeit", "kind": "google", "stdin": bad}); code == cli.ExitOK {
			t.Fatalf("%q accepted: %s", bad, out)
		}
	}
	// a public calendar, webcal as phones show it; the list knows both, without addresses
	if out, code := call(map[string]any{"action": "subscribe", "person": "haus", "name": "feiertage", "kind": "abo", "title": "Feiertage", "stdin": "webcal://openholidaysapi.org/PublicHolidays?countryIsoCode=DE&subdivisionCode=DE-NI"}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	out, _ = call(map[string]any{"action": "list"})
	if strings.Contains(out, "private-") || strings.Contains(out, "openholidaysapi") || strings.Count(out, `"person"`) != 2 {
		t.Fatalf("list: %s", out)
	}
	out, code = call(map[string]any{"action": "unsubscribe", "person": "lea", "name": "familie"})
	sec, _ = secrets.Load(env.Paths.Secrets, env.Paths.Key)
	raw, _ = base64.RawURLEncoding.DecodeString(sec["KALENDER_ABOS"])
	if code != cli.ExitOK || strings.Contains(string(raw), "private-") || !strings.Contains(string(raw), "feiertage") {
		t.Fatalf("unsubscribe: exit %d %s, left %q", code, out, raw)
	}

	// #619: each calendar its own interval, set at subscribe or changed later without the address
	if out, code := call(map[string]any{"action": "subscribe", "person": "haus", "name": "muell", "kind": "abo", "title": "Müllabfuhr", "every": "30d", "stdin": "https://example.org/abfuhr.ics"}); code != cli.ExitOK {
		t.Fatalf("subscribe --every: exit %d: %s", code, out)
	}
	if out, code := call(map[string]any{"action": "every", "person": "haus", "name": "feiertage", "every": "12h"}); code != cli.ExitOK || !strings.Contains(out, "alle 12h") {
		t.Fatalf("every: exit %d: %s", code, out)
	}
	sec, _ = secrets.Load(env.Paths.Secrets, env.Paths.Key)
	raw, _ = base64.RawURLEncoding.DecodeString(sec["KALENDER_ABOS"])
	if !strings.Contains(string(raw), `"name":"muell","kind":"abo","title":"Müllabfuhr","url":"https://example.org/abfuhr.ics"`) || !strings.Contains(string(raw), `"every":"30d"`) || !strings.Contains(string(raw), `"every":"12h"`) || !strings.Contains(string(raw), "openholidaysapi") {
		t.Fatalf("intervals not kept, or the address lost: %q", raw)
	}
	for _, bad := range []map[string]any{
		{"action": "every", "person": "haus", "name": "feiertage", "every": "15s"},
		{"action": "every", "person": "haus", "name": "feiertage"},
		{"action": "every", "person": "haus", "name": "gibtsnicht", "every": "1d"},
	} {
		if out, code := call(bad); code == cli.ExitOK {
			t.Fatalf("%v accepted: %s", bad, out)
		}
	}
}
