package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// script is a file of n lines a stock image runs; every line has a "=",
// so the unit reader takes each for a key it cannot judge.
func script(n, v int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "COUNT_%d = %d\n", i, v)
	}
	return b.String()
}

// shape102 is #102's shape (#661): a template update of auth that brings
// anmeldungen.py (107 lines with a "=") and anmeldungen.sh (19), a host
// unit with its timer, an executable pre-start and a secret from outside.
func shape102(t *testing.T) (base, changed string) {
	base, changed = copyTree(t, golden), copyTree(t, golden)
	write(t, changed, "services/auth/anmeldungen.py", script(107, 2), 0o644)
	write(t, changed, "services/auth/anmeldungen.sh", script(19, 2), 0o644)
	write(t, changed, "services/auth/auth-anmeldungen.service", "[Service]\nType=oneshot\nExecStart=/usr/bin/true\n", 0o644)
	write(t, changed, "services/auth/auth-anmeldungen.timer", "[Timer]\nOnCalendar=*:0/5\n\n[Install]\nWantedBy=timers.target\n", 0o644)
	write(t, changed, "services/auth/pre-start", "#!/bin/sh\ntrue\n", 0o755)
	edit(t, changed, "services/auth/laib.yml", "  - name: OPERATOR_PASSWORD", "  - {name: BOTE_NOTIFY_TOKEN, external: true}\n  - name: OPERATOR_PASSWORD")
	return base, changed
}

// want102 are #102's reasons after #661: six instead of 132.
var want102 = []string{
	"erwartet ein Geheimnis von außen (BOTE_NOTIFY_TOKEN in auth)",
	"services/auth/auth-anmeldungen.service läuft direkt auf der Box, nicht im Container (Host-Unit)",
	"services/auth/auth-anmeldungen.timer läuft direkt auf der Box, nicht im Container (Host-Unit)",
	"services/auth/pre-start ist ausführbar",
	"ändert 107 Zeilen in auth/anmeldungen.py (ein Skript, das Laib nicht einschätzen kann)",
	"ändert 19 Zeilen in auth/anmeldungen.sh (ein Skript, das Laib nicht einschätzen kann)",
}

// #661: what the review cannot judge in a file is one reason for the file,
// with the count — not one reason per changed line. The lines stay
// available (Lines) for the card's technical details.
func TestReviewGroupsWhatItCannotJudgeByFile(t *testing.T) {
	base, changed := shape102(t)
	r := Dirs(base, changed)
	want := want102
	if !slices.Equal(r.Human, want) {
		t.Fatalf("reasons (%d):\n%s\nwant\n%s", len(r.Human), strings.Join(r.Human, "\n"), strings.Join(want, "\n"))
	}
	if len(r.Lines) != 126 || !slices.Contains(r.Lines, "ändert COUNT_0=2 in auth/anmeldungen.py, was Laib nicht einschätzen kann") {
		t.Errorf("the single lines must stay available, got %d: %.200q", len(r.Lines), r.Lines)
	}
	// one line it cannot judge is still told as that line
	base, changed = copyTree(t, golden), copyTree(t, golden)
	write(t, changed, "services/auth/anmeldungen.py", "LIMIT = 5\n", 0o644)
	if r := Dirs(base, changed); !slices.Equal(r.Human, []string{"ändert LIMIT=5 in auth/anmeldungen.py, was Laib nicht einschätzen kann"}) {
		t.Errorf("a single line: %q", r.Human)
	}
}

// #661: the reasons that matter on their own come first, one line each,
// ordered by weight — a secret from outside, a host unit, a public route
// or auth: none, a host folder, policy.yml — then the others, then the
// files Laib cannot judge.
func TestReviewPutsTheWeightyReasonsFirst(t *testing.T) {
	base, changed := shape102(t)
	edit(t, changed, "policy.yml", "pi: {ceiling: propose}", "pi: {ceiling: apply}")
	edit(t, changed, "services/whoami/whoami.container", "AutoUpdate=registry\n", "AutoUpdate=registry\nVolume=/srv:/srv\nPodmanArgs=--log-level=debug\n")
	edit(t, changed, "services/whoami/laib.yml", "exposure: internal", "exposure: public")
	r := Dirs(base, changed)
	order := []string{"Geheimnis von außen", "auth-anmeldungen.service", "auth-anmeldungen.timer", "aus dem Internet erreichbar", "Zugriff auf den Ordner /srv", "(policy.yml)", "pre-start ist ausführbar", "Sonderrechte", "107 Zeilen in auth/anmeldungen.py", "19 Zeilen in auth/anmeldungen.sh"}
	if len(r.Human) != len(order) {
		t.Fatalf("want %d reasons, got %d:\n%s", len(order), len(r.Human), strings.Join(r.Human, "\n"))
	}
	for i, w := range order {
		if !strings.Contains(r.Human[i], w) {
			t.Errorf("reason %d: want %q, got:\n%s", i, w, strings.Join(r.Human, "\n"))
			break
		}
	}
}
