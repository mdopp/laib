// Package pages is what the box shows (#309): `laib status` and the
// readiness with its three guarantees, `laib serve` with /ready and the
// three pages (Status, Inbox, Journal), the outside watchdog's ping, the
// family card and the Bote's link QR. A box chore: it shows and pings;
// a decision on a page goes through the inbox, never around it.
package pages

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// LastBackup: the last successful backup and how many places hold it.
type LastBackup struct {
	At    string `json:"at,omitempty"` // RFC3339
	Repos int    `json:"repos"`        // places: the configured repo counts one, the off-box repo a second while its copy is under 48 h old
	OK    bool   `json:"ok"`
}

// LastChange: the last applied proposal in plain words (the card's summary).
type LastChange struct {
	Proposal   int    `json:"proposal,omitempty"`
	Summary    string `json:"summary,omitempty"`
	At         string `json:"at,omitempty"`
	Reversible bool   `json:"reversible"`
}

// Readiness is the one state with three values (docs/DESIGN.md).
type Readiness struct {
	State     string   `json:"state"`
	Services  int      `json:"services"`
	Green     int      `json:"green"`
	Yellow    int      `json:"yellow"`
	Red       int      `json:"red"`
	LastHeard string   `json:"lastHeard"`
	Lock      string   `json:"lock,omitempty"`
	Drift     int      `json:"drift"`              // files on the box that differ from the state
	Operator  string   `json:"operator"`           // aus | startet | läuft | stumm | antwortet nicht | abgewiesen | fehlt
	Firewall  string   `json:"firewall,omitempty"` // geladen | fehlt — the rendered ruleset in the kernel (#37)
	Paused    []string `json:"paused,omitempty"`   // services converge leaves alone: "<svc> von <wer> bis <wann>" (#39)
	Pending   []string `json:"pending,omitempty"`  // services whose activation an apply still owes (#39)
	Version   string   `json:"version"`            // of the running laib: the update path checks it (#37)
	Watchdog  string   `json:"watchdog"`           // kein Wächter | noch nie gemeldet | zuletzt gemeldet 12:05 (#57)
	Aufgaben  int      `json:"aufgaben"`           // open Box-Aufgaben (#667), the owner's to-dos on the box
	Faellig   int      `json:"aufgabenFaellig"`    // of them, the ones whose reminder day has come
	// the three guarantees Haus's start page shows the family (haus#2)
	LastBackup LastBackup `json:"lastBackup"`
	LastChange LastChange `json:"lastChange"`
	// what the Operator does now and did today, for the Box pages (#792)
	OperatorWork OperatorWork `json:"operatorWork"`
}

// OperatorWork is the Operator's /ready work and its journal lines of
// today: counts and the newest five in words, "10:31 Neustart bote: rot".
type OperatorWork struct {
	diag.OpWork
	Restarts    int      `json:"restarts"`
	Proposals   int      `json:"proposals"`
	Escalations int      `json:"escalations"`
	Notes       int      `json:"notes"`
	Today       []string `json:"today,omitempty"`
}

type ServiceStatus struct {
	Name         string            `json:"name"`
	Title        string            `json:"title,omitempty"`        // the template's plain name, the card's headline (#785)
	Beschreibung string            `json:"beschreibung,omitempty"` // and its one line of what it is
	State        string            `json:"state"`                  // grün | gelb | rot
	Units        map[string]string `json:"units"`
	Detail       string            `json:"detail,omitempty"`
	// Provides are its roles: laib-lease finds the units a window stops by them (ADR 0027)
	Provides []string `json:"provides,omitempty"`
	// Eigenes: the status file's own fields, for one named service (#688)
	Eigenes map[string]json.RawMessage `json:"eigenes,omitempty"`
	Gekappt []string                   `json:"eigenes_gekappt,omitempty"`
}

// BootUnclean is set by serve after its first comparison when the box did
// not come up matching the state (#9); cleared by the next clean check.
var BootUnclean string

func CollectStatus(ctx context.Context, env *cli.Env, st *state.State) (Readiness, []ServiceStatus) {
	var rows []ServiceStatus
	r := Readiness{State: "bereit", LastHeard: time.Now().UTC().Format(time.RFC3339), Version: cli.Version}
	facts := diag.OperatorFacts(ctx, env, st)
	r.Operator, _ = diag.OperatorState(facts)
	r.OperatorWork = operatorToday(env, facts.Work, time.Now())
	r.LastBackup, r.LastChange = Guarantees(env, st)
	if holder := apply.LockHolder(env.Paths); holder != "" {
		r.State, r.Lock = "eingeschränkt", holder
	}
	r.Firewall = apply.FirewallState(ctx, env.Paths)
	r.Watchdog = watchdogState(env, st)
	if list, err := aufgaben.Load(env.Paths.State); err == nil {
		r.Aufgaben, r.Faellig = aufgaben.Counts(list, time.Now().Format(time.DateOnly))
	}
	if r.Firewall == "fehlt" && r.Lock == "" {
		r.State, r.Lock = "eingeschränkt", "firewall: table inet laib fehlt im Kernel (laib converge --force lädt sie)"
	}
	if sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key); err == nil {
		if p, err := plan.Compute(ctx, st, sec, env.Paths, ""); err == nil {
			r.Drift = len(p.Changes)
		}
	}
	paused := apply.Paused(env.Paths)
	r.Pending = apply.Pending(env.Paths)
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		row := ServiceStatus{Name: name, Title: svc.Contract.Title, Beschreibung: svc.Contract.Beschreibung, State: "grün", Units: map[string]string{}, Provides: svc.Contract.Provides}
		for _, u := range svc.UnitNames() {
			a := sysd.Active(ctx, u)
			row.Units[u] = a
			switch a {
			case "active":
			case "activating", "reloading":
				if row.State == "grün" {
					row.State = "gelb"
				}
			default:
				row.State = "rot"
			}
		}
		for _, u := range svc.Oneshots() {
			if a := sysd.Active(ctx, u); a == "failed" { // a timer's run failed (#195)
				row.Units[u], row.State = a, "rot"
			}
		}
		if rep := diag.ReadStatus(st, svc); rep.Detail != "" {
			row.Detail = rep.Detail
			if a := diag.Age(rep.At, time.Now()); a != "" {
				row.Detail += " · " + a
			}
		} else if svc.Contract.Status != nil && !rep.Found {
			row.Detail = "noch kein Stand" // declared, not written yet: a line, never nothing (#246)
		}
		if pz, ok := paused[name]; ok {
			row.State = "pausiert"
			row.Detail = fmt.Sprintf("von %s bis %s %s", pz.By, pz.Until.Local().Format("02.01. 15:04"), pz.Reason)
			r.Paused = append(r.Paused, fmt.Sprintf("%s von %s bis %s", name, pz.By, pz.Until.Local().Format("02.01. 15:04")))
		}
		rows = append(rows, row)
		r.Services++
		switch row.State {
		case "grün", "pausiert":
			r.Green++
		case "gelb":
			r.Yellow++
		default:
			r.Red++
		}
	}
	if r.Red == 0 && r.Drift == 0 {
		BootUnclean = ""
	}
	if BootUnclean != "" && r.Lock == "" {
		r.State, r.Lock = "eingeschränkt", BootUnclean
	}
	return r, rows
}

func Status(ctx context.Context, env *cli.Env, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	fresh := slices.Contains(args, "--fresh")
	args = slices.DeleteFunc(args, func(a string) bool { return a == "--fresh" })
	// laib_status in serve answers from a readiness under StatusTTL old (#593);
	// a named service (after a restart), --fresh and the CLI compute
	r, rows, at := kept(ctx, env, st, fresh || len(args) > 0 || !env.MCP)
	if len(args) > 0 {
		var one []ServiceStatus
		for _, row := range rows {
			if row.Name == args[0] {
				rep := diag.ReadStatus(st, st.Services[row.Name])
				row.Eigenes, row.Gekappt = rep.Eigenes, rep.Gekappt
				one = append(one, row)
			}
		}
		rows = one
	}
	if env.JSON {
		return cli.Emit(env, map[string]any{"ready": r, "services": rows, "at": at.UTC().Format(time.RFC3339)})
	}
	fmt.Fprintf(env.W(), "Laib · %s · %d Dienste · %d grün · %d gelb · %d rot · Drift %d · Operator %s\n", r.State, r.Services, r.Green, r.Yellow, r.Red, r.Drift, r.Operator)
	if r.Lock != "" {
		fmt.Fprintf(env.W(), "  gesperrt durch: %s\n", r.Lock)
	}
	if r.Aufgaben > 0 {
		fmt.Fprintf(env.W(), "  Box-Aufgaben: %d offen, %d fällig (laib aufgabe list)\n", r.Aufgaben, r.Faellig)
	}
	for _, row := range rows {
		fmt.Fprintf(env.W(), "  %-5s %-20s", row.State, row.Name)
		for u, a := range row.Units {
			if a != "active" {
				fmt.Fprintf(env.W(), " %s=%s", u, a)
			}
		}
		if row.Detail != "" { // what the template says about itself, as the Box page shows it (#359)
			fmt.Fprintf(env.W(), " %s", row.Detail)
		}
		fmt.Fprintln(env.W())
	}
	if e, ok := journal.Last(env.Paths.Journal); ok {
		fmt.Fprintf(env.W(), "Letzte Änderung  %s  %s  %s %s  %s\n", e.T.Local().Format("02.01. 15:04"), e.Actor, e.Verb, e.Service, e.Reason)
	}
	return cli.ExitOK
}

// StatusTTL is how long laib_status answers from the kept readiness (#593).
var StatusTTL = 30 * time.Second

var statusCache struct {
	sync.Mutex
	key  string
	at   time.Time
	r    Readiness
	rows []ServiceStatus
}

// kept returns the kept readiness while it is younger than StatusTTL and
// nothing changed the box since: every apply, decision, restart, start and
// stop writes a journal line, so the newest day file's size and time are
// part of the key, and so is the apply lock's holder (an apply under way).
func kept(ctx context.Context, env *cli.Env, st *state.State, fresh bool) (Readiness, []ServiceStatus, time.Time) {
	key := env.Paths.Home + "|" + apply.LockHolder(env.Paths)
	if files, _ := filepath.Glob(filepath.Join(env.Paths.Journal, "*.jsonl")); len(files) > 0 {
		if fi, err := os.Stat(slices.Max(files)); err == nil {
			key += fmt.Sprintf("|%s|%d|%d", fi.Name(), fi.Size(), fi.ModTime().UnixNano())
		}
	}
	c := &statusCache
	c.Lock()
	defer c.Unlock()
	if fresh || c.key != key || time.Since(c.at) >= StatusTTL {
		c.r, c.rows = CollectStatus(ctx, env, st)
		c.key, c.at = key, time.Now()
		if ctx.Err() != nil { // a call cut off mid-way is no readiness to keep
			c.key = ""
		}
	}
	return c.r, slices.Clone(c.rows), c.at
}

// Guarantees reads the last successful backup from the journal and the
// last approved proposal from the inbox (haus#2).
func Guarantees(env *cli.Env, st *state.State) (LastBackup, LastChange) {
	var lb LastBackup
	var offsite time.Time
	if es, err := journal.Read(env.Paths.Journal, time.Time{}); err == nil {
		for _, e := range es {
			if e.Verb == "backup" && e.Result == "green" && e.Reason == "" {
				lb = LastBackup{At: e.T.UTC().Format(time.RFC3339), OK: true}
			}
			if e.Verb == "backup" && e.Result == "green" && e.Reason == "offsite" {
				offsite = e.T
			}
		}
	}
	if st.Box.Backup.Repo != "" {
		lb.Repos = 1
		if st.Box.Backup.Offsite != nil && time.Since(offsite) < 48*time.Hour { // the second place counts while its copy is fresh (#391)
			lb.Repos = 2
		}
	}
	var lc LastChange
	if ps, err := proposal.List(cli.InboxDir(env), false); err == nil {
		var last *proposal.Proposal
		for _, p := range ps {
			if p.Status == "approved" && (last == nil || p.Decided.After(last.Decided)) {
				last = p
			}
		}
		if last != nil {
			c := proposal.Card(last, st.Box.Domain) // reversible: the card's own answer, and not once it was taken back (#852)
			lc = LastChange{Proposal: last.ID, Summary: c.Summary, At: last.Decided.UTC().Format(time.RFC3339), Reversible: c.Undoable && last.Outcome != "rolled-back"}
		}
	}
	return lb, lc
}

// operatorToday adds what the ops hand did since midnight to its work.
func operatorToday(env *cli.Env, now diag.OpWork, t time.Time) OperatorWork {
	w := OperatorWork{OpWork: now}
	es, _ := journal.Read(env.Paths.Journal, time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()))
	for _, e := range es {
		count := map[string]*int{"restart": &w.Restarts, "propose": &w.Proposals, "repair": &w.Proposals, "escalate": &w.Escalations, "note": &w.Notes}[e.Verb]
		if e.Actor != "ops" || count == nil {
			continue
		}
		*count++
		word := map[string]string{"restart": "Neustart", "propose": "Vorschlag", "repair": "Vorschlag", "escalate": "Eskalation", "note": "Notiz"}[e.Verb]
		line := []rune(fmt.Sprintf("%s %s %s: %s", e.T.Local().Format("15:04"), word, e.Service, cmp.Or(e.Detail, e.Reason, map[string]string{"green": "grün", "red": "rot"}[e.Result])))
		w.Today = append([]string{string(line[:min(len(line), 160)])}, w.Today...)
	}
	w.Today = w.Today[:min(len(w.Today), 5)]
	return w
}

// operatorSection is the Box page's Operator part (#792): what it works on,
// why it waits, what it did today, small and in German.
func operatorSection(w OperatorWork) string {
	var b strings.Builder
	b.WriteString("<h2>Operator</h2>")
	if w.Incident == "" {
		b.WriteString("<p class=muted>Gerade kein Vorfall.</p>")
	} else {
		fmt.Fprintf(&b, "<p>Arbeitet an: %s · seit %s", html.EscapeString(w.Incident), w.Since.Local().Format("15:04"))
		if w.Step != "" {
			fmt.Fprintf(&b, " · zuletzt: %s", html.EscapeString(w.Step))
		}
		b.WriteString("</p>")
	}
	if w.Waiting != "" || w.Queued > 0 {
		fmt.Fprintf(&b, "<p class=gelb>Wartet: %s", html.EscapeString(cmp.Or(w.Waiting, "auf den nächsten Versuch")))
		if w.Queued > 0 {
			fmt.Fprintf(&b, " · %s in der Warteschlange", plural(w.Queued, "Vorfall", "Vorfälle"))
		}
		b.WriteString("</p>")
	}
	did := "noch nichts getan"
	if len(w.Today) > 0 {
		did = plural(w.Restarts, "Neustart", "Neustarts") + " · " + plural(w.Proposals, "Vorschlag", "Vorschläge") + " · " + plural(w.Escalations, "Eskalation", "Eskalationen") + " · " + plural(w.Notes, "Notiz", "Notizen")
	}
	fmt.Fprintf(&b, `<p>Heute: %s · <a href="/inbox">Inbox</a> · <a href="/journal">Journal</a></p>`, did)
	for _, l := range w.Today {
		fmt.Fprintf(&b, "<p class=muted>%s</p>", html.EscapeString(l))
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprint(n, " ", many)
}
