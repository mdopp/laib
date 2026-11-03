package main

import (
	"slices"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
)

// #674 (haus#115): the homes hand has aufgabe from DefaultPolicy on, one
// release after the tolerant loader (#667): Homes' Box-Aufgaben page lists
// the box's to-dos, adds one and checks it off or defers it for the admin
// who is logged in (--von). An old box gets the verb as the owner's
// proposal (#575).
func TestHomesKeepsTheBoxAufgabenFromTheDefaults(t *testing.T) {
	env, _ := testBox(t)
	t.Setenv("LAIB_DISK", "/nonexistent")
	if !slices.ContainsFunc(mcp.Tools(mustState(t, env), "homes"), func(tool map[string]any) bool { return tool["name"] == "laib_aufgabe" }) {
		t.Fatal("laib_aufgabe is not in the homes hand's tools/list: the Box-Aufgaben page stays empty")
	}
	got, out, code := callAufgabe(t, env, "homes", map[string]any{"action": "add", "titel": "Router-Passwort ändern", "von": "mama"})
	if code != cli.ExitOK || got.Aufgabe.Von != "homes:mama" {
		t.Fatalf("homes add: exit %d: %s", code, out)
	}
	if got, out, code = callAufgabe(t, env, "homes", map[string]any{"action": "zurueckstellen", "id": "1", "erinnern": "2026-12-01", "von": "mama"}); code != cli.ExitOK || got.Aufgabe.Erinnern != "2026-12-01" {
		t.Fatalf("homes defer: exit %d: %s", code, out)
	}
	if got, out, code = callAufgabe(t, env, "homes", map[string]any{"action": "erledigt", "id": "1", "von": "mama"}); code != cli.ExitOK || got.Aufgabe.Erledigt == nil || got.Aufgabe.Erledigt.Von != "homes:mama" {
		t.Fatalf("homes done: exit %d: %s", code, out)
	}
	if got, out, code = callAufgabe(t, env, "homes", map[string]any{"action": "list", "alle": true}); code != cli.ExitOK || len(got.Aufgaben) != 1 {
		t.Fatalf("homes list: exit %d: %s", code, out)
	}
	if _, out, code := callAufgabe(t, env, "resident", map[string]any{"action": "list"}); code == cli.ExitOK {
		t.Fatalf("a read hand reached the Box-Aufgaben: %s", out)
	}
}
