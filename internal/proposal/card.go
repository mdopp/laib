package proposal

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// A Decision card (#58, #616): what a proposal means, in the words a
// non-technical admin can judge — the same card on the Box page, in the
// Bote's relay, in `laib inbox` and in `laib_inbox`'s JSON. Built
// mechanically from the classified change, never by a model. Four parts:
// what changes (Changes), what it means (Impact), who wants it and why
// (Why), how it is undone (Reversible); a weight (Risk), and the technical
// details only behind Details.
type Decision struct {
	Summary    string   `json:"summary"`            // the headline: what changes, in one sentence
	Changes    []Line   `json:"changes"`            // before → after, + and −, in words
	Impact     []string `json:"impact"`             // what it means for the family
	Reversible string   `json:"reversible"`         // whether and how it can be undone
	Why        string   `json:"why"`                // who proposed it and why, quoted
	Risk       string   `json:"risk"`               // gering | mittel | hoch
	RiskWhy    string   `json:"riskWhy"`            // one sentence why
	Decider    string   `json:"decider,omitempty"`  // "Das entscheidest du, kein Agent." when no agent may approve
	Warnings   []string `json:"warnings,omitempty"` // the warnings about this proposal; the box's others are on the status page
	Details    string   `json:"details"`            // technical: the files, the proposer, the tier, the review's reasons
	Primary    string   `json:"primary"`            // the safe default button: "Freigeben" or "Später"
	Risky      bool     `json:"risky"`              // the primary is Später
}

// Line is one line of "Was sich ändert": Op "+" added, "−" removed, "→"
// changed, "=" what stays, "" a lead-in.
type Line struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// Change is one fact of what a proposal changes, recorded by the review
// when the proposal is made (#616): a hand's verb, path grant or ceiling
// (Hand set; Key "ceiling" for the ceiling, "verbs" when a whole verb list
// appears or goes), another rule of policy.yml (Key "autonomy.values" …),
// or a service's value (Service and Key). Op is + - ~ or = (stays, for a
// hand whose verbs change).
type Change struct {
	Op      string `json:"op"`
	Hand    string `json:"hand,omitempty"`
	Verb    string `json:"verb,omitempty"`
	Path    string `json:"path,omitempty"`
	Service string `json:"service,omitempty"`
	Key     string `json:"key,omitempty"`
	Old     string `json:"old,omitempty"`
	New     string `json:"new,omitempty"`
}

// VerbTier is each verb's tier, from the verb table (cmd/laib sets it): it
// decides whether a hand that gets a verb may change something itself or
// only propose. An unknown verb counts as apply — the more careful words.
var VerbTier map[string]string

// handWords: the hands by the names the family knows, and what they are.
var handWords = map[string][2]string{
	"haus": {"Haus", "die Familienseiten"}, "bote": {"der Bote", "Signal"}, "ops": {"der Operator", ""},
	"claude": {"Claude in der Werkstatt", ""}, "pi": {"pi", ""}, "laib": {"Laib", ""},
	"notify": {"die Benachrichtigung", "über den Boten oder per Mail"}, "approver": {"die Freigabe über Signal", ""},
	"werkstatt": {"die Werkstatt", ""}, "resident": {"die Bewohner", ""}, "human": {"ein Mensch", ""},
	"haushalt": {"die Haushaltsbefehle", "Scan und Geräte im Heimnetz"}, "lease": {"die Grafikkarten-Lease", "hält Dienste an, solange ein großes Modell die Karte braucht"},
}

// verbWords: what a verb lets a hand do, as the end of "darf …".
var verbWords = map[string]string{
	"status": "den Zustand der Dienste sehen", "inbox": "die Vorschläge sehen", "journal": "das Tagebuch der Box lesen",
	"diag": "die Befunde der Box lesen", "restart": "Dienste neu starten", "start": "Dienste starten", "stop": "Dienste anhalten",
	"person_invite": "Personen einladen", "person_pending": "sehen, wer eine Einladung noch nicht angenommen hat",
	"person_signal": "die Signal-Nummer einer Person eintragen lassen", "calendar": "Kalender verbinden",
	"zuhause": "die Adresse des Haushalts ändern lassen", "scan": "Scans starten", "note": "Notizen ins Tagebuch schreiben", "aufgabe": "Box-Aufgaben führen und abhaken",
	"tell":   "einer Person eine Nachricht schicken",
	"backup": "eine Sicherung machen", "backups": "die Sicherungen sehen", "updates": "nach Updates sehen",
	"approve": "Vorschläge freigeben", "reject": "Vorschläge ablehnen", "session": "einen Zugang für eine Werkstatt-Sitzung holen",
	"plan": "sehen, was ein Ausrollen ändern würde", "apply": "Änderungen ausrollen", "inspect": "in Dienste hineinsehen",
	"read": "Dateien von Diensten lesen", "look": "in Dienste hineinsehen", "logs": "die Protokolle der Dienste lesen",
	"write": "Dateien von Diensten schreiben", "exec": "Befehle in Diensten ausführen", "escalate": "um Hilfe rufen",
	"operator": "den Operator ein- und ausschalten", "rollback": "Änderungen zurücknehmen", "restore": "Daten aus der Sicherung zurückholen",
	"converge": "die Box wieder in Ordnung bringen", "context": "einen Überblick über die Box holen", "template": "Vorlagen verwalten",
	"add": "Dienste installieren", "remove": "Dienste entfernen", "propose": "Änderungen vorschlagen", "adopt": "fremde Dienste übernehmen",
	"ignore": "fremde Dienste übergehen", "token": "Zugänge ausstellen", "secret": "Geheimnisse setzen", "disk": "Festplatten verwalten",
	"init": "die Box neu einrichten", "serve": "die Seiten der Box betreiben", "version": "die Version sehen",
	"pause": "Dienste eine Weile anhalten", "resume": "angehaltene Dienste wieder starten",
	"skill": "die Fähigkeiten des Operators sehen", "net": "das Netz der Box sehen", "state": "die Einstellungen der Box lesen",
	"repair": "Reparaturen vorschlagen", "wish": "Wünsche an die Werkstatt schicken",
}

// pathWords: a path grant, as the end of "darf …"; a path grant always ends in a proposal.
var pathWords = map[string]string{"zuhause": "die Adresse des Haushalts ändern lassen", "people.*.signal": "die Signal-Nummer einer Person eintragen lassen"}

// ceilingWords: what a hand's ceiling lets it do.
var ceilingWords = map[string]string{"read": "nur lesen", "propose": "vorschlagen", "apply": "selbst ändern", "destroy": "auch löschen"}

// ruleWords: the rules of policy.yml beside the hands.
var ruleWords = map[string]string{
	"autonomy.values": "Einstellungen von Diensten", "autonomy.template-update": "neue Vorlagen", "autonomy.new-service": "neue Dienste",
	"autonomy.patch": "Änderungen an Dateien von Diensten", "autonomy.converge": "das Aufräumen alle zehn Minuten",
	"auto": "entscheidet Laib selbst (bei einem Fehler nimmt es die Änderung zurück)", "proposal": "fragt dich", "on": "an", "off": "aus",
}

func word(m map[string]string, k string) string {
	if w, ok := m[k]; ok {
		return w
	}
	return k // unknown stays as a word, never empty
}

// HandName is a hand as the family knows it; long adds what it is.
func HandName(hand string, long bool) string {
	w, ok := handWords[hand]
	if !ok {
		return hand
	}
	if long && w[1] != "" {
		return w[0] + " (" + w[1] + ")"
	}
	return w[0]
}

func upper(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

var numbers = []string{"kein Ding", "ein Ding", "zwei Dinge", "drei Dinge", "vier Dinge", "fünf Dinge", "sechs Dinge", "sieben Dinge", "acht Dinge", "neun Dinge", "zehn Dinge", "elf Dinge", "zwölf Dinge"}

func things(n int) string {
	if n < len(numbers) {
		return numbers[n]
	}
	return fmt.Sprintf("%d Dinge", n)
}

// grant is one verb or path a hand gains, loses or keeps, in words, with its tier.
type grant struct{ text, tier string }

func grantOf(c Change) grant {
	if c.Path != "" {
		if w, ok := pathWords[c.Path]; ok {
			return grant{w, "propose"}
		}
		if _, key, ok := strings.Cut(c.Path, ":"); ok {
			return grant{"die Einstellung " + key + " eines Dienstes ändern lassen", "propose"}
		}
		return grant{"den Wert " + c.Path + " ändern lassen", "propose"}
	}
	tier := VerbTier[c.Verb]
	if tier == "" {
		tier = "apply"
	}
	return grant{word(verbWords, c.Verb), tier}
}

func texts(gs []grant) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.text)
	}
	return out
}

// handChange is what one hand gains, loses and keeps.
type handChange struct {
	added, removed, kept []grant
	ceiling, verbs       *Change
}

// Card builds the decision card of a proposal. box are the box's own
// warnings (state.Warnings): on a card only when they name a service the
// proposal touches; the others belong to the status page (#616).
func Card(p *Proposal, domain string, box ...string) Decision {
	d := Decision{Changes: []Line{}}
	svcs := servicesOf(p)
	names := make([]string, 0, len(svcs))
	for _, s := range svcs {
		names = append(names, title(s))
	}
	who := strings.Join(names, ", ")

	hands := map[string]*handChange{}
	var handOrder []string
	var rules, values []Change
	for _, c := range p.Changes {
		switch {
		case c.Hand != "":
			h := hands[c.Hand]
			if h == nil {
				h = &handChange{}
				hands[c.Hand] = h
				handOrder = append(handOrder, c.Hand)
			}
			switch {
			case c.Key == "ceiling":
				h.ceiling = &c
			case c.Key == "verbs":
				h.verbs = &c
			case c.Op == "+":
				h.added = append(h.added, grantOf(c))
			case c.Op == "-":
				h.removed = append(h.removed, grantOf(c))
			case c.Op == "=":
				h.kept = append(h.kept, grantOf(c))
			}
		case c.Service != "":
			values = append(values, c)
		default:
			rules = append(rules, c)
		}
	}
	policyChange := slices.Contains(p.Files, "policy.yml")

	// --- the headline, from the change
	switch {
	case len(p.News) > 0: // what a template update brings the household, in its template's words (#687)
		d.Summary = p.News[0]
	case p.Boxless() && p.Evidence != "": // a repair: the finding it answers (#759)
		d.Summary = "Reparieren: " + p.Evidence
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
	case len(handOrder) == 1:
		d.Summary = handHeadline(handOrder[0], hands[handOrder[0]])
	case len(handOrder) > 1:
		ns := make([]string, 0, len(handOrder))
		for _, h := range handOrder {
			ns = append(ns, HandName(h, false))
		}
		d.Summary = "Neue Regeln, wer was darf: " + strings.Join(ns, ", ")
	case len(rules) > 0:
		d.Summary = "Was Laib allein entscheiden darf, ändert sich"
	case policyChange:
		d.Summary = "Die Regeln, wer auf der Box was darf, ändern sich"
	case len(values) == 1:
		v := values[0]
		d.Summary = title(v.Service) + ": " + v.Key + " "
		switch {
		case v.Op == "-":
			d.Summary += "fällt weg"
		case v.Old == "":
			d.Summary += "wird " + valueWord(v.New)
		default:
			d.Summary += "wird " + valueWord(v.Old) + " → " + valueWord(v.New)
		}
	case len(values) > 1 && len(svcs) == 1:
		d.Summary = fmt.Sprintf("%s: %d Einstellungen ändern sich", who, len(values))
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

	// --- what changes, and what it means
	for _, name := range handOrder {
		d.Changes = append(d.Changes, handLines(name, hands[name])...)
		d.Impact = append(d.Impact, handImpact(name, hands[name])...)
	}
	for _, r := range rules {
		d.Changes = append(d.Changes, Line{"→", upper(word(ruleWords, r.Key)) + ": " + word(ruleWords, orUnset(r.Old)) + " → " + word(ruleWords, orUnset(r.New))})
	}
	if len(p.News) > 1 {
		for _, n := range p.News {
			d.Changes = append(d.Changes, Line{"+", n})
		}
	}
	var technical []string
	for _, v := range values {
		if len(p.News) > 0 && v.Op == "-" { // #696: a value whose secret the template retired
			technical = append(technical, title(v.Service)+": "+v.Key+" entfällt, die Vorlage braucht es nicht mehr (bisher "+valueWord(v.Old)+")")
		} else if len(p.News) > 0 { // the template's values: its news says what they bring
			technical = append(technical, title(v.Service)+": "+v.Key+": "+valueWord(v.Old)+" → "+valueWord(v.New))
		} else if v.Op == "-" {
			d.Changes = append(d.Changes, Line{"−", title(v.Service) + ": " + v.Key + " (bisher " + valueWord(v.Old) + ")"})
		} else {
			d.Changes = append(d.Changes, Line{"→", title(v.Service) + ": " + v.Key + ": " + valueWord(v.Old) + " → " + valueWord(v.New)})
		}
	}
	for _, s := range svcs {
		if slices.ContainsFunc(values, func(v Change) bool { return v.Service == s }) {
			d.Impact = append(d.Impact, title(s)+" startet dafür kurz neu.")
		}
	}
	// the review's human-only reasons are the visible consequences; the
	// technical ones as one plain sentence (#687), each in Details, the one
	// that only says the rules file changed told above in words
	var because []string
	for _, r := range p.Reasons {
		i := slices.IndexFunc(plainWords, func(w [2]string) bool {
			return slices.ContainsFunc(strings.Split(w[0], "|"), func(m string) bool { return strings.Contains(r, m) })
		})
		switch {
		case i >= 0 || policyChange && strings.Contains(r, "(policy.yml)"):
			technical = append(technical, r)
			if i >= 0 && !slices.Contains(because, plainWords[i][1]) {
				because = append(because, plainWords[i][1])
			}
		default:
			d.Impact = append(d.Impact, "Achtung: "+r)
		}
	}
	if len(because) > 0 {
		last, svc := len(because)-1, cmp.Or(who, "der Dienst")
		text := strings.Join(because[:last], ", ")
		if last > 0 {
			text += " und "
		}
		d.Impact = append([]string{"Laib fragt dich, weil " + strings.ReplaceAll(text+because[last], "%s", svc) + "."}, d.Impact...)
	}
	for _, m := range p.Mints {
		d.Impact = append(d.Impact, fmt.Sprintf("Achtung: %s bekommt einen eigenen Laib-Schlüssel", HandName(m, true)))
	}
	d.Impact = append(d.Impact, p.Facts...)
	if len(d.Impact) == 0 && len(p.News) > 0 { // the headline is what changes (#698)
		d.Impact = []string{"Für die Familie ändert sich nur, was oben steht."}
	} else if len(d.Impact) == 0 {
		d.Impact = []string{"Für die Familie ändert sich nichts Sichtbares."}
	}
	if n := len(d.Impact) - impactMax; n > 0 { // short on every phone (#661); the rest is technical
		technical = append(technical, d.Impact[impactMax:]...)
		d.Impact = append(d.Impact[:impactMax:impactMax], fmt.Sprintf("und %d weitere (unter Technische Details)", n))
	}

	// --- how it is undone
	switch {
	case p.Tier == "destroy": // #759: a flashed device, a deleted folder
		d.Reversible = "Nicht mit einem Tipp rückgängig."
	case len(p.Adds) > 0:
		d.Reversible = "Rückgängig mit einem Tipp („Zurücknehmen“): der Dienst wird gestoppt und entfernt, seine Daten bleiben — Löschen ist ein eigener Schritt."
	case len(handOrder) == 1:
		d.Reversible = "Rückgängig mit einem Tipp („Zurücknehmen“): " + HandName(handOrder[0], false) + " darf danach wieder nur das Bisherige. Daten bleiben."
	case len(values) > 0 && p.Kind != "template-update":
		d.Reversible = "Rückgängig mit einem Tipp („Zurücknehmen“): der Wert von vorher kommt zurück, Daten bleiben."
	default:
		d.Reversible = "Rückgängig mit einem Tipp („Zurücknehmen“): die Dateien von vorher kommen zurück, Daten bleiben."
	}
	if p.Undo != "" { // what the proposer says undoes it (#544), next to laib's own way back
		d.Reversible += " Laut Vorschlag: " + p.Undo
	}

	// --- who and why
	d.Why = fmt.Sprintf("%s schlägt vor: „%s“", upper(actorName(p.Actor)), p.Reason)
	if p.Kind == "template-update" && who != "" { // the reason is a command (#687): what the template brings instead
		d.Why = fmt.Sprintf("%s schlägt die neue Vorlage von %s vor", upper(actorName(p.Actor)), who)
		if len(p.News) > 0 {
			d.Why += ": " + strings.Join(p.News, ". ")
		}
		d.Why += "."
	}
	if p.Evidence != "" {
		d.Why += " — gesehen: " + p.Evidence
	}

	// --- the weight, and the safe default that follows from it
	d.Risky = p.Class == "human" || len(p.Adds) > 0 || len(p.Mints) > 0
	exposed := slices.ContainsFunc(p.Reasons, func(r string) bool {
		return strings.Contains(r, "aus dem Internet erreichbar") || strings.Contains(r, "ohne Anmeldung") || strings.Contains(r, "schwächt die Anmeldung")
	})
	switch {
	case len(p.Adds) > 0:
		d.Risk, d.RiskWhy = "hoch", "Ein neuer Dienst kommt auf die Box."
	case len(p.Mints) > 0:
		d.Risk, d.RiskWhy = "hoch", "Ein Programm bekommt einen eigenen Schlüssel zur Box."
	case exposed:
		d.Risk, d.RiskWhy = "hoch", "Etwas wird von außen oder ohne Anmeldung erreichbar."
	case d.Risky && policyChange:
		d.Risk, d.RiskWhy = "mittel", "Es ändert, wer auf der Box was darf."
	case d.Risky && slices.Contains(p.Files, "box.yml"):
		d.Risk, d.RiskWhy = "mittel", "Es ändert Einstellungen der ganzen Box."
	case d.Risky:
		d.Risk, d.RiskWhy = "mittel", "Laib kann die Folgen nicht allein beurteilen."
	default:
		d.Risk, d.RiskWhy = "gering", "Eine Einstellung, mit einem Tipp zurückzunehmen."
	}
	d.Primary = "Freigeben"
	if d.Risky {
		d.Primary = "Später"
	}
	if p.Class == "human" {
		d.Decider = "Das entscheidest du, kein Agent."
	}

	// --- the warnings about this proposal only
	touched := append(slices.Clone(svcs), p.Adds...)
	for _, w := range p.Warnings {
		if slices.Contains(box, w) && !slices.ContainsFunc(touched, func(s string) bool { return mentions(w, s) }) {
			continue
		}
		d.Warnings = append(d.Warnings, w)
	}

	// --- the technical details
	d.Details = fmt.Sprintf("Änderung an %s, vorgeschlagen von %s, Stufe %s", strings.Join(p.Files, ", "), p.Actor, p.Tier)
	for _, c := range p.Changes {
		if c.Hand != "" && c.Op != "=" {
			d.Details += fmt.Sprintf("; %s %s%s%s%s", c.Hand, c.Op, c.Verb, c.Path, c.Key)
		}
	}
	if len(technical) > 0 {
		d.Details += "; " + strings.Join(technical, "; ")
	}
	if n := len(p.Lines) - linesMax; n > 0 { // the diff has them all
		d.Details += "; im Einzelnen: " + strings.Join(p.Lines[:linesMax], "; ") + fmt.Sprintf("; und %d weitere (im Diff)", n)
	} else if len(p.Lines) > 0 {
		d.Details += "; im Einzelnen: " + strings.Join(p.Lines, "; ")
	}
	return d
}

// linesMax: the single lines Details lists (#687); the rest is in the diff.
const linesMax = 12

// plainWords (#687): the review's technical reasons by their marks (| between
// them), as the end of „Laib fragt dich, weil …“; %s is the service.
var plainWords = [][2]string{
	{"(Host-Unit)|direkt auf der Box aus|ist ausführbar|ein Skript, das", "ein Teil direkt auf der Box läuft statt abgeschirmt im Container"},
	{"Geheimnis von außen|das ihm nicht gehört", "%s einen Schlüssel zu einem anderen Dienst bekommt"},
	{"Zugriff auf den Ordner|Zugriff auf einen Ordner", "%s an Ordner der Box kommt, die nicht seine sind"},
	{"Sonderrechte|im Netz der Box|nimmt eine Einschränkung weg", "%s mehr Rechte auf der Box bekommt als üblich"},
	{"Laib nicht einschätzen kann|die Laib nicht kennt|ohne dass Laib den Grund erkennt", "Laib einige Zeilen nicht selbst prüfen kann"},
}

// impactMax: the lines of "what it means" a card shows before „und N weitere“ (#661).
const impactMax = 6

func orUnset(s string) string {
	if s == "" {
		return "(nicht gesetzt)"
	}
	return s
}

// valueWord: a value as the card shows it; a secret by its name only.
func valueWord(v string) string {
	if name, ok := strings.CutPrefix(v, "!secret "); ok {
		return "ein Geheimnis (" + name + ")"
	}
	if v == "" {
		return "(leer)"
	}
	return v
}

// actorName: the proposer by name — a person by their login, a hand as the family knows it.
func actorName(actor string) string {
	if h, login, ok := strings.Cut(actor, ":"); ok && h == "human" {
		return login
	}
	return HandName(Hand(actor), false)
}

// mentions says whether text names service as a whole word.
func mentions(text, service string) bool {
	return slices.Contains(strings.FieldsFunc(text, func(r rune) bool {
		return !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}), service)
}

func handHeadline(hand string, h *handChange) string {
	n := upper(HandName(hand, false))
	switch {
	case h.ceiling != nil && h.ceiling.Op == "+":
		return n + " kommt dazu und darf " + word(ceilingWords, h.ceiling.New)
	case h.ceiling != nil && h.ceiling.Op == "-":
		return n + " fällt weg"
	case h.ceiling != nil:
		return n + " darf künftig " + word(ceilingWords, h.ceiling.New) + " (bisher " + word(ceilingWords, h.ceiling.Old) + ")"
	case h.verbs != nil:
		return "Was " + HandName(hand, false) + " darf, wird neu festgelegt"
	case len(h.added) == 1 && len(h.removed) == 0:
		return n + " darf künftig " + h.added[0].text
	case len(h.added) == 0 && len(h.removed) == 1:
		return n + " darf künftig nicht mehr " + h.removed[0].text
	case len(h.removed) == 0:
		return n + " darf künftig " + things(len(h.added)) + " mehr " + doing(h.added)
	case len(h.added) == 0:
		return n + " darf künftig " + things(len(h.removed)) + " nicht mehr"
	}
	return n + " darf künftig " + things(len(h.added)) + " mehr und " + things(len(h.removed)) + " nicht mehr"
}

// doing: what the added rights let a hand do, as one verb.
func doing(gs []grant) string {
	tiers := map[string]bool{}
	for _, g := range gs {
		tiers[g.tier] = true
	}
	switch {
	case len(tiers) == 1 && tiers["read"]:
		return "sehen"
	case !tiers["apply"] && !tiers["destroy"]:
		return "vorschlagen"
	}
	return "tun"
}

func handLines(hand string, h *handChange) []Line {
	n := upper(HandName(hand, true))
	var out []Line
	if h.ceiling != nil {
		switch h.ceiling.Op {
		case "+":
			out = append(out, Line{"+", n + " kommt dazu und darf " + word(ceilingWords, h.ceiling.New)})
		case "-":
			out = append(out, Line{"−", n + " fällt weg"})
		default:
			out = append(out, Line{"→", n + ": " + word(ceilingWords, h.ceiling.Old) + " → " + word(ceilingWords, h.ceiling.New)})
		}
	}
	if h.verbs != nil {
		was, will := "alles bis zu ihrer Grenze", "alles bis zu ihrer Grenze"
		if h.verbs.Old != "" {
			was = strings.Join(verbList(h.verbs.Old), ", ")
		}
		if h.verbs.New != "" {
			will = strings.Join(verbList(h.verbs.New), ", ")
		}
		out = append(out, Line{"→", n + " darf: " + was + " → " + will})
	}
	if len(h.added) > 0 {
		out = append(out, Line{"", n + " darf zusätzlich"})
		for _, g := range h.added {
			out = append(out, Line{"+", g.text})
		}
	}
	if len(h.removed) > 0 {
		out = append(out, Line{"", n + " darf nicht mehr"})
		for _, g := range h.removed {
			out = append(out, Line{"−", g.text})
		}
	}
	if len(h.kept) > 0 {
		out = append(out, Line{"=", "Was " + HandName(hand, false) + " schon darf, bleibt: " + strings.Join(texts(h.kept), ", ") + "."})
	}
	return out
}

func verbList(s string) []string {
	var out []string
	for _, v := range strings.Fields(s) {
		out = append(out, word(verbWords, v))
	}
	return out
}

// handImpact: for every grant the security question — may the hand change
// something itself, or only propose (#616), from its verbs' tiers.
func handImpact(hand string, h *handChange) []string {
	n := upper(HandName(hand, false))
	var out, self []string
	propose := false
	for _, g := range h.added {
		switch g.tier {
		case "read":
		case "propose":
			propose = true
		default:
			self = append(self, g.text)
		}
	}
	if len(self) > 0 {
		out = append(out, n+" darf das selbst tun, ohne vorher zu fragen: "+strings.Join(self, ", ")+".")
	}
	if propose {
		out = append(out, n+" ändert dadurch nichts selbst: jede solche Änderung kommt wieder als eigener Vorschlag hierher.")
	}
	if len(h.added) > 0 && len(self) == 0 && !propose {
		out = append(out, n+" sieht dadurch mehr, ändert aber nichts.")
	}
	if len(h.removed) > 0 {
		out = append(out, n+" kann danach nicht mehr: "+strings.Join(texts(h.removed), ", ")+".")
	}
	if h.ceiling != nil && h.ceiling.Op != "-" {
		out = append(out, n+" darf danach "+word(ceilingWords, h.ceiling.New)+".")
	}
	return out
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

// Title is title for the pages (the key page, #696).
func Title(name string) string { return title(name) }

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
