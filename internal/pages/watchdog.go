package pages

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
)

// The outside watchdog (#57): every surface that could say "something is
// wrong" runs on the box, so when the box itself is down nobody is told.
// laib-notify (#443) pings box.yml watchdog.url every five minutes while
// laib answers /ready and writes watchdog.last, which /ready shows; the
// outside (a healthchecks-style endpoint) turns fifteen minutes of silence
// into a push to the admin's phone. The ping means „Laib lebt“ only (#857):
// a red service is the red message's, not a second alarm.

// The stamp lies in laib-notify's own folder, the one its container mounts (ADR 0027).
func watchdogStamp(env *cli.Env) string {
	return filepath.Join(env.Paths.Home, "notify", "watchdog.last")
}

// watchdogState for readiness: "kein Wächter" | "zuletzt gemeldet 12:05" | "noch nie gemeldet".
func watchdogState(env *cli.Env, st *state.State) string {
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
