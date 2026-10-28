package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
)

// #883 (owner, 2026-10-28): the Bote's laib wish, served by laib-haushalt
// behind the door, becomes a Box-Aufgabe for the admins with the person
// who asked — never an issue in box.yml intake.repo. laib-haushalt adds it
// over MCP with its own hand; the kernel writes aufgaben.yml and journals.
func TestABotesWishLandsOnTheAdminsBoxAufgaben(t *testing.T) {
	c := newCbox(t)
	g := newFakeGitHub(t)
	c.intake(t, "mdopp/x", g)
	c.env.Actor = "bote"
	if rc := c.wish(t, "--von", "lena", "Ich hätte gern einen Dienst, der Rezepte sammelt"); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	if len(g.calls) != 0 {
		t.Fatalf("the Bote's wish became an issue: %v", g.calls)
	}
	list, err := aufgaben.Load(c.env.Paths.State)
	if err != nil || len(list) != 1 {
		t.Fatalf("one Box-Aufgabe: %v %v", list, err)
	}
	a := list[0]
	if !strings.HasPrefix(a.Titel, "Wunsch aus dem Boten: Ich hätte gern einen Dienst") || a.Fuer != aufgaben.Admins || a.Von != "bote:lena" || !strings.Contains(a.Warum, "Rezepte sammelt") {
		t.Fatalf("the admins' task, from lena through the Bote: %+v", a)
	}
	if !c.journalHas(t, "Box-Aufgabe #1") {
		t.Fatal("the journal names the task")
	}
}

// #883: laib_wish carries who asked (von) as --von; laib-haushalt's hand
// may add a Box-Aufgabe in the name of the caller the door named, never
// tick one off or defer it.
func TestTheWishCarriesWhoAskedAndHaushaltOnlyAddsTasks(t *testing.T) {
	env, _ := testBox(t)
	t.Setenv("LAIB_DISK", "/nonexistent")
	st := mustState(t, env)
	argv, _, err := mcp.Argv(st, "wish", map[string]any{"text": "ein Kalender", "von": "lena"})
	if err != nil || !slices.Equal(argv, []string{"--von", "lena", "ein Kalender"}) {
		t.Fatalf("argv %q %v", argv, err)
	}
	if argv, _, _ := mcp.Argv(st, "wish", map[string]any{"text": "ein Kalender"}); !slices.Equal(argv, []string{"ein Kalender"}) {
		t.Fatalf("without von: %q", argv)
	}
	_, l, out, code := callTasks(t, env, "haushalt", map[string]any{"args": `add "Wunsch aus dem Boten: Kalender" --warum "lena über den Boten" --fuer admins --von bote:lena`})
	if code != cli.ExitOK || len(l) != 1 || l[0].Fuer != "admins" {
		t.Fatalf("haushalt adds: exit %d %s", code, out)
	}
	if list, _ := aufgaben.Load(env.Paths.State); len(list) != 1 || list[0].Von != "bote:lena" {
		t.Fatalf("von is the caller laib-haushalt names: %+v", list)
	}
	for _, act := range []string{"erledigt 1", "zurueckstellen 1 2030-01-01"} {
		if _, _, out, code := callTasks(t, env, "haushalt", map[string]any{"args": act}); code == cli.ExitOK {
			t.Errorf("haushalt %s: %s", act, out)
		}
	}
}
