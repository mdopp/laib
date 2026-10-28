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
	// OK: a green backup and no red backup finding (failed, or older than
	// 36 h); a stale off-box copy lowers Repos instead (#856)
	OK bool `json:"ok"`
	// OnlyOnDataDisk: the one place is the data disk it protects
	OnlyOnDataDisk bool   `json:"onlyOnDataDisk,omitempty"`
	Problem        string `json:"problem,omitempty"` // why it is not OK, diag's red finding
	Text           string `json:"text"`              // the fact in one neutral sentence, as Laib's pages say it
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
	Aufgaben  int      `json:"aufgaben"`           // open Box-Aufgaben (#667) that are nobody's own (#878): the admins' and everyone's
	Faellig   int      `json:"aufgabenFaellig"`    // of them, the ones whose reminder day has come
	// Problems: what is red beyond one service (disk, backup, firewall,
	// and the last whole diag's slow probes), one line each (#856)
	Problems []string `json:"problems,omitempty"`
	// Says: each problem in one plain sentence, for the pages (#855); the
	// same findings, index by index
	Says []string `json:"-"`
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
	Red          []string          `json:"red,omitempty"` // why it is red beyond its units: "health: 502 Bad Gateway" (#856)
	// Provides are its roles: laib-lease finds the units a window stops by them (ADR 0027)
	Provides []string `json:"provides,omitempty"`
	// Eigenes: the status file's own fields, for one named service (#688)
	Eigenes map[string]json.RawMessage `json:"eigenes,omitempty"`
	Gekappt []string                   `json:"eigenes_gekappt,omitempty"`
}

// CollectStatus reads the box and gives the one verdict (#856) that /ready,
// the Status page, laib status and Haus show: judge decides, from facts.
func CollectStatus(ctx context.Context, env *cli.Env, st *state.State) (Readiness, []ServiceStatus) {
	now := time.Now()
	r := Readiness{LastHeard: now.UTC().Format(time.RFC3339), Version: cli.Version}
	op := diag.OperatorFacts(ctx, env, st)
	f := facts{now: now, lock: apply.LockHolder(env.Paths)}
	var opFinding *diag.Finding
	r.Operator, opFinding = diag.OperatorState(op)
	r.OperatorWork = operatorToday(env, op.Work, now)
	f.backup, f.change, f.found = Guarantees(env, st)
	r.Firewall = apply.FirewallState(ctx, env.Paths)
	r.Watchdog = watchdogState(env, st)
	if list, err := aufgaben.Load(env.Paths.State); err == nil {
		list = slices.DeleteFunc(list, func(a aufgaben.Aufgabe) bool { return !aufgaben.Sees(a, false, "") }) // a person's own count for them, in laib_aufgabe
		r.Aufgaben, r.Faellig = aufgaben.Counts(list, now.Format(time.DateOnly))
	}
	if sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key); err == nil {
		if p, err := plan.Compute(ctx, st, sec, env.Paths, ""); err == nil {
			r.Drift = len(p.Changes)
		}
	}
	for _, x := range []*diag.Finding{opFinding, diag.NFTables(r.Firewall)} {
		if x != nil {
			f.found = append(f.found, *x)
		}
	}
	f.found = append(f.found, diag.DiskPressure(st.Box.DataDir)...)
	f.found = append(f.found, diag.Kept(env.Paths.Home)...)
	names := st.ServiceNames()
	health := make([]*diag.Finding, len(names)) // in parallel: one slow service does not hold the page
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() { health[i] = diag.Health(ctx, st.Services[name]) })
	}
	paused := apply.Paused(env.Paths)
	r.Pending = apply.Pending(env.Paths)
	for _, name := range names {
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
		rep := diag.ReadStatus(st, svc)
		f.found = append(f.found, diag.SelfFindings(rep, now)...)
		if rep.Detail != "" {
			row.Detail = rep.Detail
			if a := diag.Age(rep.At, now); a != "" {
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
		f.rows = append(f.rows, row)
	}
	wg.Wait()
	for _, h := range health {
		if h != nil {
			f.found = append(f.found, *h)
		}
	}
	return judge(r, f)
}

// facts are what CollectStatus read, for judge.
type facts struct {
	now    time.Time
	lock   string // the apply lock's holder
	rows   []ServiceStatus
	found  []diag.Finding // diag's findings: live, and the last whole run's slow ones
	backup LastBackup     // At, Repos and OnlyOnDataDisk as read
	change LastChange
}

// judge is the one verdict (#856). Readiness is what docs/DESIGN.md says:
// eingeschränkt while apply is locked, else bereit. A red finding of one
// service makes it red with its reason (a unit's is its units' own line);
// any other is a problem line; the backup holds only while no backup
// finding is red. A paused service stays paused.
func judge(r Readiness, f facts) (Readiness, []ServiceStatus) {
	r.State, r.Lock = "bereit", f.lock
	if f.lock != "" {
		r.State = "eingeschränkt"
	}
	rows, at := f.rows, map[string]int{}
	for i, row := range rows {
		at[row.Name] = i
	}
	lb := f.backup
	for _, x := range f.found {
		if x.Severity != "rot" || x.Probe == "unit" && x.Service != "" {
			continue
		}
		if i, ok := at[x.Service]; ok {
			if rows[i].State != "pausiert" {
				rows[i].State, rows[i].Red = "rot", append(rows[i].Red, x.Probe+": "+x.Detail)
			}
			continue
		}
		r.Problems, r.Says = append(r.Problems, strings.TrimPrefix(x.Service+": "+x.Detail, ": ")), append(r.Says, diag.Say(x))
		if x.Probe == "backup" && x.Topic != "offsite" && lb.Problem == "" {
			lb.Problem = x.Detail
		}
	}
	lb.OK = lb.At != "" && lb.Problem == ""
	lb.Text = backupText(lb)
	r.LastBackup, r.LastChange = lb, f.change
	r.Services, r.Green, r.Yellow, r.Red = len(rows), 0, 0, 0
	for _, row := range rows {
		switch row.State {
		case "grün", "pausiert":
			r.Green++
		case "gelb":
			r.Yellow++
		default:
			r.Red++
		}
	}
	return r, rows
}

// backupText is the backup in one neutral sentence: when, how many places,
// and why it does not hold. The family's words are Haus's (haus#173).
func backupText(lb LastBackup) string {
	at, err := time.Parse(time.RFC3339, lb.At)
	if err != nil {
		return "Noch keine Sicherung."
	}
	s := "Letzte Sicherung " + at.Local().Format("02.01. 15:04")
	switch {
	case lb.Repos >= 2:
		s += ", an zwei Orten"
	case lb.OnlyOnDataDisk:
		s += ", an einem Ort: auf der Datenplatte, die sie sichert"
	case lb.Repos == 1:
		s += ", an einem Ort"
	}
	if p := []rune(lb.Problem); len(p) > 0 {
		s += ". " + strings.ToUpper(string(p[0])) + string(p[1:])
	}
	return s + "."
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
	for _, p := range r.Problems {
		fmt.Fprintf(env.W(), "  rot: %s\n", p)
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
		if len(row.Red) > 0 {
			fmt.Fprintf(env.W(), " %s", strings.Join(row.Red, "; "))
		}
		if row.Detail != "" { // what the template says about itself, as the Box page shows it (#359)
			fmt.Fprintf(env.W(), " %s", row.Detail)
		}
		fmt.Fprintln(env.W())
	}
	fmt.Fprintf(env.W(), "Sicherung  %s\n", r.LastBackup.Text)
	if c := lastChange(r.LastChange); c != "" {
		fmt.Fprintf(env.W(), "Letzte Änderung  %s\n", c)
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

// Guarantees reads the last successful backup and diag's backup findings,
// and the last approved proposal from the inbox (haus#2).
func Guarantees(env *cli.Env, st *state.State) (LastBackup, LastChange, []diag.Finding) {
	var lb LastBackup
	bf, found := diag.Backup(env, st)
	if !bf.LastGreen.IsZero() {
		lb.At = bf.LastGreen.UTC().Format(time.RFC3339)
	}
	if st.Box.Backup.Repo != "" {
		lb.Repos = 1
		if st.Box.Backup.Offsite != nil && time.Since(bf.Offsite) < 48*time.Hour { // the second place counts while its copy is fresh (#391)
			lb.Repos = 2
		}
		lb.OnlyOnDataDisk = lb.Repos == 1 && diag.OnlyOnDataDisk(st)
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
	return lb, lc, found
}

// lastChange is the last applied change in one line: when, what, which proposal.
func lastChange(c LastChange) string {
	at, err := time.Parse(time.RFC3339, c.At)
	if err != nil || c.Summary == "" {
		return ""
	}
	return fmt.Sprintf("%s · %s (#%d)", at.Local().Format("02.01. 15:04"), c.Summary, c.Proposal)
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
