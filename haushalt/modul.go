package haushalt

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// homesModule are Homes' modules (homes ADR 0001 §7): a wish names one of
// them (#1038). A module Homes adds later is added here too; until then
// laib refuses a wish for it, and Homes says so.
var homesModule = []string{"geraete", "energie", "sprechen", "kalender", "kontakte", "konto", "einkauf", "notizen",
	"dokumente", "musik", "podcasts", "gesehen", "sicherung", "standort", "netzlaufwerk", "gesundheit", "werkstatt", "nutzung"}

var (
	modulName   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	backendName = regexp.MustCompile(`^([a-z][a-z0-9-]{0,62})?$`)
)

// wunschTitel starts the title of Homes' wish for a module; its weg is
// /module/<name>, the Homes page the admin switches it on (#1038).
const wunschTitel = "Wunsch aus Homes: "

// aufgabe is a Box-Aufgabe as `laib aufgabe list --alle` answers it.
type aufgabe struct {
	ID    int    `json:"id"`
	Titel string `json:"titel"`
	Seit  string `json:"seit"`
	Von   string `json:"von"`
	Weg   *struct {
		Seite string `json:"seite"`
	} `json:"weg"`
	Erledigt *struct {
		Am  string `json:"am"`
		Von string `json:"von"`
	} `json:"erledigt"`
}

// vorschlag is a proposal as `laib inbox --all` answers it.
type vorschlag struct {
	ID           int       `json:"id"`
	Status       string    `json:"status"`  // open, approved, rejected, stale
	Outcome      string    `json:"outcome"` // rolled-back, later, …
	Created      time.Time `json:"created"`
	Adds         []string  `json:"adds"`
	RejectReason string    `json:"rejectReason"`
	Changes      []change  `json:"changes"`
}

// change is one value a proposal changes, before → after.
type change struct {
	Op      string `json:"op"`
	Service string `json:"service"`
	Key     string `json:"key"`
	Old     string `json:"old"`
	New     string `json:"new"`
}

// Stand is where one module's latest wish stands (laib wish stand, #1038).
type Stand struct {
	Modul     string `json:"modul"`
	Backend   string `json:"backend"`
	Stand     string `json:"stand"` // angefordert | wartet | abgelehnt | eingerichtet | umgesetzt
	Aufgabe   int    `json:"aufgabe"`
	Seit      string `json:"seit"`                // the wish's day, JJJJ-MM-TT
	Von       string `json:"von"`                 // who asked: homes:<login>, or homes
	Vorschlag int    `json:"vorschlag,omitempty"` // wartet, abgelehnt, eingerichtet: the proposal's number
	Grund     string `json:"grund,omitempty"`     // abgelehnt: the owner's reason for the no
}

// modulWish: `laib wish --modul <name> [--von <login>] "<Titel>"` from
// Homes (#1038, homes ADR 0001 §10): a Box-Aufgabe for the admins, as the
// Bote's wish (#883), „Wunsch aus Homes: <Titel>“, its weg the module's
// Homes page. While one is open, a second files nothing and answers it.
func modulWish(ctx context.Context, env *Env, args []string) int {
	if hand, _, _ := strings.Cut(env.Actor, ":"); hand != "homes" {
		return env.refused("laib wish --modul ist Homes' Wunsch für eines seiner Module; " + env.Actor + " wünscht ohne --modul")
	}
	var modul, von string
	var words []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--modul" && i+1 < len(args):
			modul, i = args[i+1], i+1
		case args[i] == "--von" && i+1 < len(args):
			von, i = args[i+1], i+1
		default:
			words = append(words, args[i])
		}
	}
	titel := headline(strings.Join(strings.Fields(strings.Join(words, " ")), " "))
	if titel == "" || von != "" && !scanPerson.MatchString(von) {
		return env.fail(errors.New(`usage: laib wish --modul <name> [--von <login>] "<Titel>"`))
	}
	if !slices.Contains(homesModule, modul) {
		return env.refused(fmt.Sprintf("%q ist kein Modul von Homes (%s)", modul, strings.Join(homesModule, ", ")))
	}
	list, err := env.aufgaben(ctx)
	if err != nil {
		return env.fail(err)
	}
	if a := latestWish(list, modul); a != nil && a.Erledigt == nil {
		return env.answer(map[string]any{"modul": modul, "aufgabe": a.ID, "seit": a.Seit, "von": a.Von, "neu": false,
			"message": fmt.Sprintf("Der Wunsch liegt schon beim Admin, als Box-Aufgabe #%d seit %s.", a.ID, a.Seit)})
	}
	by, who := "homes", "Jemand"
	if von != "" {
		by, who = "homes:"+von, von
	}
	warum := who + " hat sich in Homes „" + titel + "“ gewünscht, " + time.Now().Format("02.01.2006 15:04") +
		". Abhaken heißt für Homes: wird nicht eingerichtet. Ist das Modul an, hakt Homes die Aufgabe selbst ab."
	id, err := env.Laib.Aufgabe(ctx, env.Ceiling, "add", wunschTitel+titel, "--warum", warum, "--fuer", "admins", "--weg", "/module/"+modul, "--von", by)
	if err != nil {
		env.journalAs("red", "homes", wunschTitel+titel, err.Error())
		return env.fail(err)
	}
	env.journal("homes", wunschTitel+titel, fmt.Sprintf("Box-Aufgabe #%d für die Admins, Modul %s", id, modul))
	return env.answer(map[string]any{"modul": modul, "aufgabe": id, "seit": time.Now().Format(time.DateOnly), "von": by, "neu": true,
		"message": fmt.Sprintf("Der Wunsch liegt beim Admin als Box-Aufgabe #%d.", id)})
}

// wishStand: `laib wish stand <name>=<backend> …` (#1038): for each module
// with a wish, where its latest stands, from the Box-Aufgaben and the Inbox,
// which laib-haushalt reads back as its own hand. A module without one is
// left out.
func wishStand(ctx context.Context, env *Env, args []string) int {
	if len(args) == 0 {
		return env.fail(errors.New("usage: laib wish stand <modul>=<backend> … (z. B. standort=reitti)"))
	}
	for _, a := range args {
		name, backend, _ := strings.Cut(a, "=")
		if !modulName.MatchString(name) || !backendName.MatchString(backend) {
			return env.fail(fmt.Errorf("%q: <modul>=<backend>, klein, z. B. standort=reitti", a))
		}
	}
	list, err := env.aufgaben(ctx)
	if err != nil {
		return env.fail(err)
	}
	out, inbox := []Stand{}, []vorschlag(nil)
	for _, a := range args {
		name, backend, _ := strings.Cut(a, "=")
		w := latestWish(list, name)
		if w == nil {
			continue
		}
		if w.Erledigt == nil && inbox == nil {
			if err := env.Laib.Read(ctx, env.Ceiling, "laib_inbox", &inbox, "--all", "--limit", "0"); err != nil {
				return env.fail(fmt.Errorf("die Inbox: %w", err))
			}
			if inbox == nil {
				inbox = []vorschlag{}
			}
			slices.SortFunc(inbox, func(a, b vorschlag) int { return b.ID - a.ID })
		}
		s := Stand{Modul: name, Backend: backend, Aufgabe: w.ID, Seit: w.Seit, Von: w.Von}
		s.Stand, s.Vorschlag, s.Grund = standOf(w, inbox, name, backend)
		out = append(out, s)
	}
	return env.emit(out)
}

// standOf is a wish's state (#1038, homes ADR 0001 §10). Ticked off: by
// Homes itself umgesetzt, by anyone else abgelehnt. Open: the proposals for
// the module since the wish's day, newest first — one open is wartet, else
// the latest decided one says eingerichtet or abgelehnt; „später“ and a
// change a run rolled back are no answer, and without one it is angefordert.
func standOf(w *aufgabe, inbox []vorschlag, modul, backend string) (string, int, string) {
	if e := w.Erledigt; e != nil {
		if e.Von == "homes" {
			return "umgesetzt", 0, ""
		}
		return "abgelehnt", 0, ""
	}
	var last *vorschlag
	for i := range inbox {
		p := &inbox[i]
		if !p.fuer(modul, backend) || p.Created.Local().Format(time.DateOnly) < w.Seit {
			continue
		}
		switch {
		case p.Status == "open":
			return "wartet", p.ID, ""
		case last != nil:
		case p.Status == "approved" && p.Outcome != "rolled-back", p.Status == "rejected" && p.Outcome != "later":
			last = p
		}
	}
	switch {
	case last == nil:
		return "angefordert", 0, ""
	case last.Status == "approved":
		return "eingerichtet", last.ID, ""
	}
	return "abgelehnt", last.ID, last.RejectReason
}

// fuer: does the proposal bring the module? It adds its backend as a
// service, or it takes the module out of Homes' MODULE_AUS (#1036).
func (p *vorschlag) fuer(modul, backend string) bool {
	if backend != "" && slices.Contains(p.Adds, backend) {
		return true
	}
	return slices.ContainsFunc(p.Changes, func(c change) bool {
		return c.Service == "homes" && c.Key == "MODULE_AUS" && inList(c.Old, modul) && (c.Op == "-" || !inList(c.New, modul))
	})
}

// inList: is name one of a comma list's entries?
func inList(list, name string) bool {
	return slices.ContainsFunc(strings.Split(list, ","), func(s string) bool { return strings.TrimSpace(s) == name })
}

// latestWish is the module's latest wish from Homes, open or not; nil
// without one.
func latestWish(list []aufgabe, modul string) *aufgabe {
	var w *aufgabe
	for i := range list {
		a := &list[i]
		if a.Weg != nil && a.Weg.Seite == "/module/"+modul && strings.HasPrefix(a.Titel, wunschTitel) && (w == nil || a.ID > w.ID) {
			w = a
		}
	}
	return w
}

// answer is the JSON answer, or only its message on a terminal.
func (e *Env) answer(v map[string]any) int {
	if e.JSON {
		return e.emit(v)
	}
	fmt.Fprintln(e.Out, v["message"])
	return exitOK
}

// aufgaben reads the Box-Aufgaben back, the done ones too.
func (e *Env) aufgaben(ctx context.Context) ([]aufgabe, error) {
	var out struct {
		Aufgaben []aufgabe `json:"aufgaben"`
	}
	if err := e.Laib.Read(ctx, e.Ceiling, "laib_aufgabe", &out, "list", "--alle"); err != nil {
		return nil, fmt.Errorf("die Box-Aufgaben: %w", err)
	}
	return out.Aufgaben, nil
}

// moduleAus is where the owner's switch for Homes' modules lives (homes
// ADR 0001 §5, #1036): the haushalt hand's one path grant in a values.yml.
const moduleAus = "services/homes/values.yml:MODULE_AUS"

// cmdModule: `laib module aus [<modul>,…]` (#1036): the Homes modules to
// leave off although their backend runs, as a proposal for
// services/homes/values.yml MODULE_AUS; empty switches every module on whose
// backend runs. Filed as the haushalt hand under the verb's ceiling, so the
// dial never approves it: it waits in the Inbox. A name Homes does not know
// is its to ignore (Homes and Laib release apart); a malformed one is refused.
func cmdModule(ctx context.Context, env *Env, args []string) int {
	usage := errors.New(`usage: laib module aus [<modul>,…] [--grund "<Satz>"] (z. B. podcasts,energie; leer: alle, deren Dienst läuft)`)
	if len(args) == 0 || args[0] != "aus" {
		return env.fail(usage)
	}
	var names, rest []string
	grund := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--grund" && i+1 < len(args) {
			grund, i = strings.Join(strings.Fields(args[i+1]), " "), i+1
			continue
		}
		rest = append(rest, args[i])
	}
	for _, n := range strings.Split(strings.Join(rest, ","), ",") {
		if n = strings.TrimSpace(n); n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
		if n != "" && !modulName.MatchString(n) {
			return env.fail(fmt.Errorf("%q ist kein Modulname: klein, a-z 0-9 -, mit Komma getrennt", n))
		}
	}
	if len([]rune(grund)) > 200 {
		return env.fail(usage)
	}
	// the caller's sentence for the family (Homes names the modules by their titles), else laib's own
	list, reason := strings.Join(names, ","), cmp.Or(grund, "Alle Bereiche von Homes einschalten, deren Dienst auf der Box läuft")
	if list != "" && grund == "" {
		reason = "In Homes ausschalten: " + strings.Join(names, ", ")
	}
	return env.propose(ctx, "homes", reason, "Vorschlag #%d: nach der Freigabe in der Inbox startet Homes mit der neuen Auswahl.", "--path", moduleAus+"='"+list+"'")
}
