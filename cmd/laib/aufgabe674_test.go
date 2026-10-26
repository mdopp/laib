package main

import (
	"slices"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
)

// #674 (haus#115): the haus hand has aufgabe from DefaultPolicy on, one
// release after the tolerant loader (#667): Haus' Box-Aufgaben page lists
// the box's to-dos, adds one and checks it off or defers it for the admin
// who is logged in (--von). An old box gets the verb as the owner's
// proposal (#575).
func TestHausKeepsTheBoxAufgabenFromTheDefaults(t *testing.T) {
	env, _ := testBox(t)
	t.Setenv("LAIB_DISK", "/nonexistent")
	if !slices.ContainsFunc(mcp.Tools(mustState(t, env), "haus"), func(tool map[string]any) bool { return tool["name"] == "laib_aufgabe" }) {
		t.Fatal("laib_aufgabe is not in the haus hand's tools/list: the Box-Aufgaben page stays empty")
	}
	got, out, code := callAufgabe(t, env, "haus", map[string]any{"action": "add", "titel": "Router-Passwort ändern", "von": "mama"})
	if code != cli.ExitOK || got.Aufgabe.Von != "haus:mama" {
		t.Fatalf("haus add: exit %d: %s", code, out)
	}
	if got, out, code = callAufgabe(t, env, "haus", map[string]any{"action": "zurueckstellen", "id": "1", "erinnern": "2026-12-01", "von": "mama"}); code != cli.ExitOK || got.Aufgabe.Erinnern != "2026-12-01" {
		t.Fatalf("haus defer: exit %d: %s", code, out)
	}
	if got, out, code = callAufgabe(t, env, "haus", map[string]any{"action": "erledigt", "id": "1", "von": "mama"}); code != cli.ExitOK || got.Aufgabe.Erledigt == nil || got.Aufgabe.Erledigt.Von != "haus:mama" {
		t.Fatalf("haus done: exit %d: %s", code, out)
	}
	if got, out, code = callAufgabe(t, env, "haus", map[string]any{"action": "list", "alle": true}); code != cli.ExitOK || len(got.Aufgaben) != 1 {
		t.Fatalf("haus list: exit %d: %s", code, out)
	}
	if _, out, code := callAufgabe(t, env, "resident", map[string]any{"action": "list"}); code == cli.ExitOK {
		t.Fatalf("a read hand reached the Box-Aufgaben: %s", out)
	}
}
