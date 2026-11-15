package main

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// ownerLabel is the PR label that lets the principles change: they are
// the owner's.
const ownerLabel = "besitzer-entscheidung"

// gate is the judgment gate (owner, 2026-11-15): facts are the script's,
// judgment is the agent's who makes the change. A PR whose facts change in
// a way that matters for security carries a change to docs/sicherheit.yml
// (docs/c4.yml for a service or its layer), or acknowledges the fact under
// aenderungen: with why nothing changes. The principles change only with
// the owner's label.
func gate(head, base *tree, labels []string, w io.Writer) int {
	judged := judgmentChanged(head, base)
	acked := map[string]bool{}
	for _, a := range head.sec.Aenderungen {
		acked[strings.TrimSpace(a.Fakt)] = strings.TrimSpace(a.Grund) != ""
	}
	var open []change
	for _, c := range changes(head, base) {
		c.resolved = acked[c.f.key] || judged || (c.f.kind == "dienst" && !bytes.Equal(head.c4raw, base.c4raw))
		if !c.resolved {
			open = append(open, c)
		}
	}
	bad := 0
	if len(open) > 0 {
		fmt.Fprintln(w, "c4: diese Änderungen betreffen die Sicherheit der Box, docs/sicherheit.yml ändert sich aber nicht mit:")
		for _, c := range open {
			fmt.Fprintln(w, "  -", c)
		}
		fmt.Fprintln(w, "Bitte ordne sie in docs/sicherheit.yml ein (Netz, Daten, Schichtregeln, „Was passiert, wenn …“, offene Punkte) und führe scripts/c4.sh aus.")
		fmt.Fprintln(w, "Ändert sich am Konzept nichts, bestätige jede unter aenderungen: mit fakt: \"<der Name in `…` oben>\" und grund: \"<warum>\".")
		bad = 1
	}
	if base.secraw != nil && !reflect.DeepEqual(head.sec.Grundsaetze, base.sec.Grundsaetze) && !slices.Contains(labels, ownerLabel) {
		fmt.Fprintf(w, "c4: die Grundsätze in docs/sicherheit.yml haben sich geändert. Sie sind des Besitzers: der PR braucht das Label %s.\n", ownerLabel)
		bad = 1
	}
	if bad == 0 {
		fmt.Fprintln(w, "c4: Urteils-Tor grün")
	}
	return bad
}

// judgmentChanged: sicherheit.yml changed in anything but its change log.
func judgmentChanged(head, base *tree) bool {
	if base.secraw == nil {
		return head.secraw != nil
	}
	h, b := head.sec, base.sec
	h.Aenderungen, b.Aenderungen = nil, nil
	return !reflect.DeepEqual(h, b)
}

var issueRef = regexp.MustCompile(`^(?:([a-z0-9-]+)#)?([0-9]+)$`)

// standRow is one marked point of sicherheit.yml with its name.
type standRow struct {
	name string
	s    stand
}

func stands(s secsrc) []standRow {
	var out []standRow
	for _, x := range s.Identitaeten {
		out = append(out, standRow{"Identität „" + x.Wer + "“", x.Stand})
	}
	for _, x := range s.Netz {
		out = append(out, standRow{"Netz „" + x.Weg + "“", x.Stand})
	}
	for _, x := range s.Daten {
		out = append(out, standRow{"Daten „" + x.Daten + "“", x.Stand})
	}
	for _, x := range s.Offen {
		out = append(out, standRow{"offener Punkt „" + x.Titel + "“", x.Stand})
	}
	return out
}

// ghState answers an issue's state ("OPEN", "CLOSED"); a seam for tests.
var ghState = func(repo, n string) (string, error) {
	out, err := exec.Command("gh", "issue", "view", n, "-R", repo, "--json", "state", "--jq", ".state").Output()
	return strings.TrimSpace(string(out)), err
}

var ghFound = func() bool { _, err := exec.LookPath("gh"); return err == nil }

// issues is the daily check: a point not „läuft“ whose named issue is
// closed is stale. Its output is the body of the issue CI opens.
func issues(t *tree, w io.Writer) int {
	if !ghFound() {
		fmt.Fprintln(w, "c4: gh fehlt, die Prüfung der Issues entfällt hier (in CI läuft sie täglich)")
		return 0
	}
	owner := strings.Split(t.module, "/")[1]
	var stale []string
	for _, r := range stands(t.sec) {
		if r.s.Status == "läuft" {
			continue
		}
		for _, ref := range r.s.Issues {
			repo, n := issueRepo(t, ref)
			st, err := ghState(owner+"/"+repo, n)
			if err != nil {
				fmt.Fprintf(w, "c4: %s#%s nicht lesbar: %v\n", repo, n, err)
				return 2
			}
			if st == "CLOSED" {
				stale = append(stale, fmt.Sprintf("- %s steht auf **%s**, nennt aber [%s#%s](https://github.com/%s/%s/issues/%s), das geschlossen ist.", r.name, r.s.Status, repo, n, owner, repo, n))
			}
		}
	}
	if len(stale) == 0 {
		fmt.Fprintln(w, "c4: jeder offene Punkt nennt ein offenes Issue")
		return 0
	}
	fmt.Fprintln(w, "Das Sicherheitskonzept (`docs/SICHERHEIT.md`, Quelle `docs/sicherheit.yml`) ist nicht mehr auf dem Stand:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, strings.Join(stale, "\n"))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Bitte den Stand in `docs/sicherheit.yml` nachtragen (läuft, oder ein neues Issue nennen) und `scripts/c4.sh` ausführen. Dieses Issue öffnet und aktualisiert der tägliche Lauf von `.github/workflows/architektur.yml` (`scripts/c4.sh --issues`).")
	return 1
}

func issueRepo(t *tree, ref string) (repo, n string) {
	m := issueRef.FindStringSubmatch(ref)
	if m == nil {
		return "", ref
	}
	repo = m[1]
	if repo == "" {
		repo = strings.Split(t.module, "/")[2]
	}
	return repo, m[2]
}
