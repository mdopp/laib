package pages

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

// Laib stands without the layers above (#1236, ADR 0035): what an admin
// needs to keep the box running is on Laib's own Status page, also when
// Homes shows the same — the admins' tasks with a way to do them here, the
// last whole diag's findings, the load and the morning report. The load is
// measured beside CollectStatus: laib-disk samples the CPU for a second.
func betrieb(ctx context.Context, env *cli.Env, st *state.State) func() string {
	load := make(chan string, 1)
	go func() { load <- loadPart(ctx, st) }()
	return func() string { return aufgabenPart(env, st) + befundePart(env) + <-load + reportPart(env) }
}

// aufgabenPart: the open tasks of the admins and everyone's, by id for a
// link (/#aufgabe-N), each with its other ways and its two forms; a
// person's own stay that person's (#878).
func aufgabenPart(env *cli.Env, st *state.State) string {
	list, err := aufgaben.Load(env.Paths.State)
	b := strings.Builder{}
	b.WriteString("<h2 id=aufgaben>Aufgaben</h2>")
	if err != nil {
		return b.String() + failure("Die Box-Aufgaben lassen sich gerade nicht lesen. Lade die Seite in einer Minute noch einmal.", err.Error())
	}
	now, homes, n := time.Now(), strings.TrimSuffix(render.HomesURL(st), "/"), 0
	today, later := now.Format(time.DateOnly), now.AddDate(0, 0, 7).Format(time.DateOnly)
	for _, a := range list {
		if !a.Open() || !aufgaben.Sees(a, false, "") {
			continue
		}
		n++
		meta, class, ways := fmt.Sprintf("#%d · seit %s", a.ID, a.Seit), "", ""
		if a.Due(today) {
			meta, class = meta+" · fällig", " gelb"
		} else if a.Erinnern != "" {
			meta += " · erinnert ab " + a.Erinnern
		}
		if aufgaben.ClosesItself(a.FertigWenn) {
			meta += " · Laib hakt sie selbst ab, sobald es gemessen erledigt ist"
		}
		if w := a.Weg; w != nil && w.Schluessel != "" {
			ways += `<a href="` + html.EscapeString(render.KeyLink(st, w.Schluessel)) + `">Schlüssel eintragen ›</a>`
		} else if w != nil && w.Seite != "" && homes != "" {
			ways += `<a href="` + html.EscapeString(homes+w.Seite) + `">Auch in Homes ›</a>`
		} else if w != nil && w.Gespraech {
			ways += "<span class=muted>Der Bote führt auch im Gespräch hindurch.</span>"
		}
		fmt.Fprintf(&b, `<div class="card%s" id=aufgabe-%d data-f><b>%s</b><span class=note>%s</span><small class=muted>%s</small>%s`, class, a.ID, html.EscapeString(a.Titel), html.EscapeString(a.Warum), html.EscapeString(meta), ways)
		fmt.Fprintf(&b, `<form method=post action=/aufgabe/%d/erledigt><button>Erledigt</button></form><form method=post action=/aufgabe/%d/zurueckstellen><input type=date name=bis value=%s aria-label="Erinnern ab"> <button>Zurückstellen</button></form></div>`, a.ID, a.ID, later)
	}
	if n == 0 {
		b.WriteString("<p class=muted>Keine offenen Aufgaben.</p>")
	}
	return b.String()
}

// Aufgabe is a task's two taps on the Status page (#1236): erledigt, or
// zurückstellen until a day. Bookkeeping as `laib aufgabe` does it: one
// commit, one journal line, as the person Authelia names.
func Aufgabe(env *cli.Env) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.PathValue("id"))
		tat, by, bis := r.PathValue("tat"), webActor(r), r.FormValue("bis")
		back := "<p><a href=/#aufgaben>Zurück zur Status-Seite</a></p>"
		st, err := cli.LoadState(env)
		if err == nil && tat != "erledigt" && tat != "zurueckstellen" {
			err = fmt.Errorf("%q kann man mit einer Box-Aufgabe nicht tun", tat)
		}
		if err == nil {
			err = aufgaben.Update(r.Context(), env.Paths.State, by, func(list []aufgaben.Aufgabe) ([]aufgaben.Aufgabe, string, error) {
				if i := slices.IndexFunc(list, func(a aufgaben.Aufgabe) bool { return a.ID == id }); i >= 0 && !aufgaben.Sees(list[i], false, "") {
					return list, "", fmt.Errorf("Box-Aufgabe #%d gibt es nicht", id) // a person's own: as if it were not there
				}
				if tat == "erledigt" {
					return list, fmt.Sprintf("aufgabe erledigt #%d", id), aufgaben.Done(list, id, by, "", time.Now())
				}
				return list, fmt.Sprintf("aufgabe zurueckstellen #%d bis %s", id, bis), aufgaben.Defer(list, id, bis)
			})
		}
		if err != nil {
			page(w, "Status", failure(fmt.Sprintf("Box-Aufgabe #%d ließ sich nicht %s; warum, steht unter Details. Lade die Status-Seite neu und versuch es noch einmal.", id, map[bool]string{true: "abhaken", false: "zurückstellen"}[tat == "erledigt"]), err.Error())+back)
			return
		}
		detail, say := fmt.Sprintf("erledigt #%d", id), fmt.Sprintf("Box-Aufgabe #%d ist erledigt.", id)
		if tat == "zurueckstellen" {
			detail, say = fmt.Sprintf("zurueckstellen #%d bis %s", id, bis), fmt.Sprintf("Box-Aufgabe #%d ist zurückgestellt, Laib erinnert ab %s wieder.", id, bis)
		}
		cli.Record(env, st, journal.Entry{Actor: by, Verb: "aufgabe", Result: "info", Detail: detail + " auf der Box-Seite"})
		page(w, "Status", "<p>"+html.EscapeString(say)+"</p>"+back)
	}
}

// befundePart: the last whole diag run's red and yellow findings in words,
// diag's line under Details; the live ones are above and on the cards.
func befundePart(env *cli.Env) string {
	at, fs := diag.LastRun(env.Paths.Home)
	if at.IsZero() {
		return "<h2>Befunde</h2><p class=muted>Die Box hat sich noch nicht ganz durchgesehen; das macht sie jede Nacht.</p>"
	}
	b := strings.Builder{}
	fmt.Fprintf(&b, "<h2>Befunde</h2><p class=muted>Letzte ganze Durchsicht %s.</p>", at.Local().Format("02.01. 15:04"))
	for _, f := range fs {
		if f.Severity == "rot" || f.Severity == "gelb" {
			fmt.Fprintf(&b, `<div class="card %s" data-f><span class=note>%s</span>%s</div>`, f.Severity, html.EscapeString(diag.Say(f)+map[bool]string{true: " Sieh in der Inbox nach, ob eine Reparatur wartet."}[f.Severity == "rot"]), details(strings.TrimSpace(f.Service+" "+f.Probe+": "+f.Detail+" "+f.Fix)))
		}
	}
	return b.String()
}

// loadPart: what the box carries now, laib-disk's measure (laib auslastung).
func loadPart(ctx context.Context, st *state.State) string {
	raw, err := diag.Load(ctx, st)
	var l struct {
		CPU struct {
			Cores int
			Load  [3]float64
			Pct   float64
		}
		Mem   struct{ Total, Used uint64 }
		Disks []struct {
			Role       string
			Pct        float64
			Free, Size uint64
		}
		GPU *struct{ Pct int }
	}
	if err == nil {
		err = json.Unmarshal(raw, &l)
	}
	if err != nil {
		return "<h2>Auslastung</h2><p class=muted>Die Auslastung lässt sich gerade nicht messen.</p>" + details(err.Error())
	}
	gb := func(n uint64) string { return strings.Replace(fmt.Sprintf("%.1f GB", float64(n)/1e9), ".", ",", 1) }
	b := strings.Builder{}
	fmt.Fprintf(&b, "<h2>Auslastung</h2><p>Prozessor %.0f %% · Last %.2f bei %d Kernen · Arbeitsspeicher %s von %s", l.CPU.Pct, l.CPU.Load[0], l.CPU.Cores, gb(l.Mem.Used), gb(l.Mem.Total))
	if l.GPU != nil {
		fmt.Fprintf(&b, " · Grafikkarte %d %%", l.GPU.Pct)
	}
	b.WriteString("</p>")
	for _, d := range l.Disks {
		fmt.Fprintf(&b, "<p class=%s>%s: %.0f %% belegt, %s von %s frei</p>", map[bool]string{true: "gelb", false: "muted"}[d.Pct >= 85], map[string]string{"system": "Systemplatte", "data": "Datenplatte", "boot": "Startbereich"}[d.Role], d.Pct, gb(d.Free), gb(d.Size))
	}
	return b.String()
}

// reportPart: the Operator's latest morning report (laib note --kind
// report), also without Homes (#798 moved it there, #1236 back here too).
func reportPart(env *cli.Env) string {
	files, _ := filepath.Glob(filepath.Join(env.Paths.Home, "reports", "*.md"))
	if len(files) == 0 {
		return ""
	}
	last := slices.Max(files)
	text, err := os.ReadFile(last)
	if err != nil {
		return ""
	}
	day := strings.TrimSuffix(filepath.Base(last), ".md")
	if t, err := time.Parse(time.DateOnly, day); err == nil {
		day = t.Format("02.01.2006")
	}
	s := strings.TrimSpace(string(text))
	if proposal.Technical(s) {
		return "<h2>Morgenbericht</h2><p class=muted>Vom " + day + ": er nennt technische Angaben, sie stehen unter Details.</p>" + details(s)
	}
	return "<h2>Morgenbericht</h2><p class=muted>Vom " + day + "</p><p>" + html.EscapeString(s) + "</p>"
}
