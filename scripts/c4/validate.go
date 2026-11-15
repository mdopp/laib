package main

import (
	"fmt"
	"slices"
)

var statuses = []string{"läuft", "geplant", "Lücke", "offen"}

var rings = []string{"host", "helfer", "vorlage", "laptop", "blatt", "werkzeug"}

// validate holds the hand-written files to the code, both ways: every
// template has its layer, every kernel package and every program its
// place, no entry names something that is gone, and a point that is not
// „läuft“ names its issue.
func validate(t *tree) []string {
	var errs []string
	if t.c4raw == nil || t.secraw == nil {
		return []string{"docs/c4.yml und docs/sicherheit.yml müssen beide da sein"}
	}
	have := map[string]bool{}
	for _, x := range t.tpls {
		have[x.name] = true
		if l := t.layer(x.name); l < 0 || l > 2 {
			errs = append(errs, fmt.Sprintf("docs/c4.yml: die Vorlage %s hat keine Schicht 0, 1 oder 2 unter schichten: (ADR 0035)", x.name))
		}
	}
	for _, n := range sortedKeys(t.c4.Schichten) {
		if !have[n] {
			errs = append(errs, fmt.Sprintf("docs/c4.yml: schichten: nennt %s, die Vorlage gibt es nicht mehr", n))
		}
	}
	errs = append(errs, validateKern(t)...)
	errs = append(errs, validatePrograms(t, have)...)
	for _, r := range stands(t.sec) {
		errs = append(errs, validateStand(r)...)
	}
	return errs
}

func validateKern(t *tree) []string {
	var errs []string
	have := map[string]bool{}
	for _, p := range t.pkgs {
		have[p.path] = true
		k, ok := t.c4.Kern[p.path]
		if !ok || k.Titel == "" {
			errs = append(errs, fmt.Sprintf("docs/c4.yml: das Kern-Paket %s hat keinen Eintrag unter kern: (titel, text)", p.path))
		}
		if ok && k.Zieht != nil && (k.Zieht.Nach == "" || k.Zieht.Issue == "") {
			errs = append(errs, fmt.Sprintf("docs/c4.yml: kern: %s zieht: braucht nach: und issue:", p.path))
		}
	}
	for _, n := range sortedKeys(t.c4.Kern) {
		if !have[n] {
			errs = append(errs, fmt.Sprintf("docs/c4.yml: kern: nennt %s, das Paket gibt es nicht mehr", n))
		}
	}
	return errs
}

func validatePrograms(t *tree, tpls map[string]bool) []string {
	var errs []string
	have := map[string]bool{}
	for _, b := range t.budgets {
		if b.name == "kernel" {
			continue
		}
		have[b.name] = true
		p, ok := t.c4.Programme[b.name]
		switch {
		case !ok:
			errs = append(errs, fmt.Sprintf("docs/c4.yml: das Programm %s (scripts/loc.sh) hat keinen Platz unter programme:", b.name))
		case !slices.Contains(rings, p.Ring):
			errs = append(errs, fmt.Sprintf("docs/c4.yml: programme: %s hat ring %q, erlaubt sind %v", b.name, p.Ring, rings))
		case p.Ring == "vorlage" && !tpls[p.Vorlage]:
			errs = append(errs, fmt.Sprintf("docs/c4.yml: programme: %s nennt die Vorlage %q, die es nicht gibt", b.name, p.Vorlage))
		}
	}
	for _, n := range sortedKeys(t.c4.Programme) {
		if !have[n] {
			errs = append(errs, fmt.Sprintf("docs/c4.yml: programme: nennt %s, scripts/loc.sh kennt es nicht", n))
		}
	}
	if _, ok := t.budget("kernel"); !ok {
		errs = append(errs, "scripts/loc.sh: keine Zeile budget kernel")
	}
	return errs
}

func validateStand(r standRow) []string {
	var errs []string
	if !slices.Contains(statuses, r.s.Status) {
		errs = append(errs, fmt.Sprintf("docs/sicherheit.yml: %s hat den Stand %q, erlaubt sind %v", r.name, r.s.Status, statuses))
	}
	if r.s.Status != "läuft" && len(r.s.Issues) == 0 {
		errs = append(errs, fmt.Sprintf("docs/sicherheit.yml: %s steht auf %s und nennt kein Issue (issues:)", r.name, r.s.Status))
	}
	for _, ref := range r.s.Issues {
		if !issueRef.MatchString(ref) {
			errs = append(errs, fmt.Sprintf("docs/sicherheit.yml: %s nennt %q, ein Issue ist 1245 oder archiv#68", r.name, ref))
		}
	}
	return errs
}
