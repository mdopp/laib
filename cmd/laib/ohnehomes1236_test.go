package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/render"
)

// addTask puts a task on the box's list as a person would, and returns its id.
func addTask(t *testing.T, env *cliEnv, a aufgaben.Aufgabe) int {
	t.Helper()
	err := aufgaben.Update(context.Background(), env.Paths.State, "human:test", func(list []aufgaben.Aufgabe) ([]aufgaben.Aufgabe, string, error) {
		list, a = aufgaben.Add(list, a, "human:test", time.Now())
		return list, "aufgabe add", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

// #1236, ADR 0035: a box without Homes is maintained from Laib's own
// Status page: the admins' tasks with a way to do them there, the last
// whole diag's findings, the load and the morning report.
// Ablauf L-59
func TestLaibStandsWithoutHomes(t *testing.T) {
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	if render.HomesURL(st) != "" {
		t.Fatal("the test box has Homes")
	}
	addTask(t, env, aufgaben.Aufgabe{Titel: "Zweite Platte einbauen", Warum: "Dann ist die Datenplatte gespiegelt.", Weg: &aufgaben.Weg{Laib: true}})
	addTask(t, env, aufgaben.Aufgabe{Titel: "Stimme einrichten", Weg: &aufgaben.Weg{Seite: "/stimme"}})
	addTask(t, env, aufgaben.Aufgabe{Titel: "Annas Passwort", Fuer: "person:anna"})
	if err := aufgaben.CheckWeg(aufgaben.Weg{Laib: true}); err != nil {
		t.Errorf("Laib's own way is no way: %v", err)
	}
	os.WriteFile(filepath.Join(env.Paths.Home, "diag.json"), []byte(`{"at":"2026-11-15T03:00:00Z","findings":[{"severity":"gelb","probe":"raid","detail":"das RAID hat nur eine Platte"}]}`), 0o644)
	os.MkdirAll(filepath.Join(env.Paths.Home, "reports"), 0o755)
	os.WriteFile(filepath.Join(env.Paths.Home, "reports", "2026-11-15.md"), []byte("Nachts alles ruhig, die Sicherung lief um 03:10.\n"), 0o644)
	disk := filepath.Join(t.TempDir(), "laib-disk")
	os.WriteFile(disk, []byte("#!/bin/sh\necho '{\"cpu\":{\"cores\":4,\"load\":[0.5,0.4,0.3],\"pct\":12},\"mem\":{\"total\":16000000000,\"used\":4000000000},\"disks\":[{\"role\":\"data\",\"pct\":91,\"free\":90000000000,\"size\":1000000000000}]}'\n"), 0o755)
	t.Setenv("LAIB_DISK", disk)
	mux := pages.Routes(env, func(h http.HandlerFunc) http.HandlerFunc { return h })
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	page := w.Body.String()
	plainEnough(t, "/", page)
	for _, want := range []string{
		"<h2 id=aufgaben>Aufgaben</h2>", "id=aufgabe-1 ", "Zweite Platte einbauen", "Dann ist die Datenplatte gespiegelt.", "action=/aufgabe/1/erledigt", "action=/aufgabe/1/zurueckstellen", "Stimme einrichten",
		"<h2>Befunde</h2>", "Letzte ganze Durchsicht", `class="card gelb"`,
		"<h2>Auslastung</h2>", "Prozessor 12 %", "Arbeitsspeicher 4,0 GB von 16,0 GB", `<p class=gelb>Datenplatte: 91 % belegt, 90,0 GB von 1000,0 GB frei</p>`,
		"<h2>Morgenbericht</h2>", "Vom 15.11.2026", "Nachts alles ruhig",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the Status page lacks %q", want)
		}
	}
	for _, gone := range []string{"Annas Passwort", "Auch in Homes"} { // a person's own stays theirs; no Homes, no link to it
		if strings.Contains(page, gone) {
			t.Errorf("the Status page shows %q", gone)
		}
	}
	r := httptest.NewRequest("POST", "/aufgabe/1/erledigt", nil)
	r.Header.Set("Remote-User", "martin")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if list, _ := aufgaben.Load(env.Paths.State); list[0].Erledigt == nil || list[0].Erledigt.Von != "human:martin" {
		t.Fatalf("Erledigt on Laib's page did not check the task off as the person: %+v\n%s", list[0], w.Body.String())
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(w.Body.String(), "Zweite Platte einbauen") {
		t.Error("a done task is still on the Status page")
	}
}
