package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/state"
)

// The outside watchdog (#57): every surface that could say "something is
// wrong" runs on the box, so when the box itself is down nobody is told.
// serve pings box.yml watchdog.url every five minutes while readiness is
// not red; the outside (a healthchecks-style endpoint) turns fifteen
// minutes of silence into a push to the admin's phone. A red box stays
// silent on purpose.

var watchdogInterval = 5 * time.Minute

func watchdogStamp(env *cliEnv) string { return filepath.Join(env.paths.Home, "watchdog.last") }

// watchdogPing pings once; false when nothing was sent (no URL, red, or an error).
func watchdogPing(ctx context.Context, env *cliEnv, st *state.State) bool {
	url := st.Box.Watchdog.URL
	if url == "" {
		return false
	}
	r, _ := collectStatus(ctx, env, st)
	if r.Red > 0 || r.State == "eingeschränkt" {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false
	}
	os.WriteFile(watchdogStamp(env), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
	return true
}

// watchdogState for readiness: "kein Wächter" | "zuletzt gemeldet 12:05" | "noch nie gemeldet".
func watchdogState(env *cliEnv, st *state.State) string {
	if st.Box.Watchdog.URL == "" {
		return "kein Wächter"
	}
	b, err := os.ReadFile(watchdogStamp(env))
	if err != nil {
		return "noch nie gemeldet"
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return "noch nie gemeldet"
	}
	return "zuletzt gemeldet " + t.Local().Format("02.01. 15:04")
}

// watchdogLoop runs in serve until ctx ends.
func watchdogLoop(ctx context.Context, env *cliEnv) {
	t := time.NewTicker(watchdogInterval)
	defer t.Stop()
	for {
		if st, err := loadState(env); err == nil {
			watchdogPing(ctx, env, st)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// cmdFamilyCard prints the paper card (#57): one HTML page in German for
// the family — what the box does, what to look at, what to do when it
// does not work, whom to call. Printed once with the recovery sheet.
func cmdFamilyCard(ctx context.Context, env *cliEnv, args []string) int {
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	contact := st.Box.Watchdog.Contact
	if contact == "" {
		contact = "den Admin"
	}
	var svcs []string
	for _, name := range st.ServiceNames() {
		if p := st.Services[name].Contract.Provides; len(p) > 0 {
			svcs = append(svcs, fmt.Sprintf("%s (%s)", name, strings.Join(p, ", ")))
		}
	}
	if len(svcs) == 0 {
		svcs = []string{"noch keine Dienste"}
	}
	fmt.Fprintf(env.w(), `<!doctype html><html lang="de"><meta charset="utf-8"><title>Die Box – Karte für die Familie</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:42rem;margin:2rem auto;padding:0 1rem;color:#1d1d1b}h1{font-size:1.5rem}h2{font-size:1.1rem;margin-top:1.4rem}li{margin:.3rem 0}.k{color:#6b665c}@media print{body{margin:0}}</style>
<h1>Die Box im Haus – %s</h1>
<p>Die Box ist der kleine Rechner, auf dem unsere Dinge laufen: %s.</p>
<h2>Die drei Lichter auf der Haus-Seite</h2>
<ul>
<li><b>Alles läuft</b> – wenn das nicht grün ist, kümmert sich jemand; nichts tun.</li>
<li><b>Letzte Sicherung</b> – wann die Dinge zuletzt gesichert wurden.</li>
<li><b>Letzte Änderung</b> – was zuletzt verändert wurde, und dass es rückgängig geht.</li>
</ul>
<h2>Wenn die Box nicht geht</h2>
<ol>
<li>Stecker ziehen, <b>10 Sekunden</b> warten, wieder einstecken.</li>
<li><b>5 Minuten</b> warten – die Box kommt von allein wieder, mit allem, was drauf war.</li>
<li>Geht es dann immer noch nicht: <b>%s</b> anrufen oder schreiben.</li>
</ol>
<p><b>Nie</b> den USB-Stick in die Box stecken – der ist nur zum Neuaufsetzen und gehört in die Schublade.</p>
<p class="k">Gedruckt am %s. Die Box verwaltet sich selbst; nichts auf dieser Karte braucht ein Passwort.</p>
</html>
`, html.EscapeString(st.Box.Domain), html.EscapeString(strings.Join(svcs, ", ")), html.EscapeString(contact), time.Now().Format("02.01.2006"))
	if st.Box.Watchdog.Contact == "" {
		fmt.Fprintln(os.Stderr, "laib: box.yml watchdog.contact is empty — the card says 'den Admin'; set it to a name and a number")
	}
	_ = errors.New
	return exitOK
}
