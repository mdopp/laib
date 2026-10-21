package proposal

import (
	"fmt"
	"sort"
	"strings"
)

// A Decision card (#58): what a proposal means, in the words a non-technical
// admin can judge — the same card on the Box page, in the Bote's relay and
// in `laib inbox`. Built mechanically from the classified change, never by
// a model.
type Decision struct {
	Summary    string   `json:"summary"`    // what changes, in one sentence
	Impact     []string `json:"impact"`     // what it means for the family
	Reversible string   `json:"reversible"` // whether and how it can be undone
	Why        string   `json:"why"`        // who proposed it and why, quoted
	Primary    string   `json:"primary"`    // the safe default button: "Freigeben" or "Später"
	Risky      bool     `json:"risky"`      // the primary is Später
}

// Card builds the decision card of a proposal.
func Card(p *Proposal, domain string) Decision {
	var d Decision
	svcs := servicesOf(p)
	names := make([]string, 0, len(svcs))
	for _, s := range svcs {
		names = append(names, title(s))
	}
	who := strings.Join(names, ", ")
	switch {
	case len(p.Adds) > 0:
		adds := make([]string, 0, len(p.Adds))
		for _, a := range p.Adds {
			adds = append(adds, title(a))
		}
		d.Summary = strings.Join(adds, ", ") + " wird installiert"
		if len(p.Adds) > 1 {
			d.Summary = strings.Join(adds, ", ") + " werden installiert"
		}
		for _, f := range p.Facts {
			d.Summary += " — " + strings.TrimPrefix(f, p.Adds[0]+" ist ")
			break
		}
	case p.Kind == "values" && who != "":
		d.Summary = "Einstellungen von " + who + " ändern sich"
	case p.Kind == "template-update" && who != "":
		d.Summary = who + " bekommt die neue Vorlage"
	case who != "":
		d.Summary = who + " ändert sich"
		if len(p.Facts) > 0 {
			d.Summary += " — " + p.Facts[0]
		}
	default:
		d.Summary = "Einstellungen der Box ändern sich"
	}
	// the family: the review's human-only reasons are exactly the visible consequences
	for _, r := range p.Reasons {
		d.Impact = append(d.Impact, "Achtung: "+r)
	}
	for _, m := range p.Mints {
		d.Impact = append(d.Impact, fmt.Sprintf("Achtung: die Hand %s bekommt einen eigenen Laib-Schlüssel", m))
	}
	for _, f := range p.Facts {
		d.Impact = append(d.Impact, f)
	}
	if len(d.Impact) == 0 {
		d.Impact = []string{"Für die Familie ändert sich nichts Sichtbares."}
	}
	switch {
	case len(p.Adds) > 0:
		d.Reversible = "Rückgängig mit einem Tipp („Zurücknehmen“): der Dienst wird gestoppt und entfernt, seine Daten bleiben — Löschen ist ein eigener Schritt."
	default:
		d.Reversible = "Rückgängig mit einem Tipp („Zurücknehmen“): die Dateien von vorher kommen zurück, Daten bleiben."
	}
	if p.Undo != "" { // what the proposer says undoes it (#544), next to laib's own way back
		d.Reversible += " Laut Vorschlag: " + p.Undo
	}
	d.Why = fmt.Sprintf("%s schlägt vor: „%s“", p.Actor, p.Reason)
	if p.Evidence != "" {
		d.Why += " — gesehen: " + p.Evidence
	}
	d.Risky = p.Class == "human" || len(p.Adds) > 0 || len(p.Mints) > 0
	d.Primary = "Freigeben"
	if d.Risky {
		d.Primary = "Später"
	}
	return d
}

// servicesOf lists the services a proposal's files belong to.
func servicesOf(p *Proposal) []string {
	seen := map[string]bool{}
	for _, f := range p.Files {
		if strings.HasPrefix(f, "services/") {
			parts := strings.SplitN(strings.TrimPrefix(f, "services/"), "/", 2)
			if parts[0] != "" {
				seen[parts[0]] = true
			}
		}
	}
	var out []string
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// title: a service name as people say it (paperless → Paperless, home-assistant → Home Assistant).
func title(name string) string {
	words := strings.Split(strings.ReplaceAll(name, "-", " "), " ")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
