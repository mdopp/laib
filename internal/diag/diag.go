// Package diag is `laib diag`: the probes (units, health, drift, disk
// pressure, RAID, SMART, temperature, the Operator's heartbeat, the backup
// and recovery gaps) and their findings. A box chore, not the trust kernel
// (ADR 0005 amendment #309): it reads and reports, it never fixes.
package diag

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// Finding is one diag result. Severity: rot | gelb | info. A finding may
// carry the verb that fixes it; it never runs it (ADR 0001).
type Finding struct {
	Severity string `json:"severity"`
	Probe    string `json:"probe"`
	Service  string `json:"service,omitempty"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix,omitempty"`
	// Repair: the one laib verb that repairs it, when there is one (#441):
	// `laib repair <probe> [service]` makes it a proposal a human approves.
	Repair []string `json:"repair,omitempty"`
	// Topic tells findings of one probe apart that have no service (#667:
	// backup offsite), so a Box-Aufgabe's befund can name exactly one.
	Topic string `json:"topic,omitempty"`
	// Unmeasured: the probe could not look (laib-disk silent, DNS not
	// answering). It is never covered by a Box-Aufgabe and closes none.
	Unmeasured bool `json:"unmeasured,omitempty"`
	// Aufgabe: the open Box-Aufgabe that covers it (#667).
	Aufgabe int `json:"aufgabe,omitempty"`
	// Effect and Undo: what the repair does to the household and how it is
	// undone, in German, for its Inbox card (#759: a device restarts).
	Effect string `json:"effect,omitempty"`
	Undo   string `json:"undo,omitempty"`
	// Does: what the repair does, the card's headline (#746), when the
	// status file knows it better than laib (the wake word a flash plays)
	Does string `json:"does,omitempty"`
	// Say is what it means to a person, in one plain sentence (#855): the
	// red mail and a repair's card show it, Detail and Fix stay for the admin.
	Say string `json:"say,omitempty"`
}

// Cmd runs the built-in probes: failed units, drift between state and
// box, health URLs, disk pressure. Red first. Probes are deliberately few
// (docs/DESIGN.md: about ten remain); each new one needs a recurrence.
func Cmd(ctx context.Context, env *cli.Env, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	only, toJournal := "", false
	for _, a := range args {
		if a == "--journal" {
			toJournal = true
			continue
		}
		only = a
	}
	findings := Probes(ctx, env, st, only)
	if findings == nil {
		findings = []Finding{} // JSON [] rather than null for callers
	}
	if only == "" {
		settle(env, st, findings)
		closeGone(env, st, findings)
	}
	if toJournal {
		// #13: after an auto-update. Red findings become red journal lines
		// and reach the webhook; a clean run is one green line.
		red := 0
		for _, f := range findings {
			if f.Severity == "rot" {
				red++
				cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "diag", Service: f.Service, Result: "red", Detail: f.Probe + ": " + f.Detail})
			}
		}
		if red == 0 {
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "diag", Result: "green", Detail: "keine roten Befunde"})
		}
	}
	if env.JSON {
		return cli.Emit(env, findings)
	}
	if len(findings) == 0 {
		fmt.Fprintln(env.W(), "Keine Befunde.")
	}
	for _, f := range findings {
		fmt.Fprintf(env.W(), "%-5s %-8s %-14s %s", f.Severity, f.Probe, f.Service, f.Detail)
		if f.Fix != "" {
			fmt.Fprintf(env.W(), "   → %s", f.Fix)
		}
		if f.Repair != nil {
			fmt.Fprintf(env.W(), "   ↻ laib repair %s %s", f.Probe, f.Service)
		}
		fmt.Fprintln(env.W())
	}
	return cli.ExitOK
}

func Probes(ctx context.Context, env *cli.Env, st *state.State, only string) []Finding {
	var out []Finding
	for _, name := range st.ServiceNames() {
		if only != "" && name != only {
			continue
		}
		svc := st.Services[name]
		for _, u := range svc.UnitNames() {
			if a := sysd.Active(ctx, u); a != "active" {
				out = append(out, Finding{Severity: "rot", Probe: "unit", Service: name, Detail: u + " ist " + a, Fix: "laib logs " + name, Repair: []string{"restart", name}})
			}
		}
		for _, u := range svc.Oneshots() {
			if sysd.Active(ctx, u) == "failed" { // a timer's run failed (#195)
				out = append(out, Finding{Severity: "rot", Probe: "unit", Service: name, Detail: u + " ist failed", Fix: "laib logs " + name, Repair: []string{"restart", name}})
			}
		}
		// what the service says about itself (#309); a stale one is re-checked
		// in the background, the next look shows the result
		rep := ReadStatus(st, svc)
		if Stale(svc, rep, time.Now()) {
			Refresh(ctx, svc)
		}
		for _, f := range rep.Findings {
			if a := Age(rep.At, time.Now()); a != "" {
				f.Detail += " (" + a + ")"
			}
			out = append(out, f)
		}
		if h := svc.Contract.Health.HTTP; h != "" {
			client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h, nil)
			resp, err := client.Do(req)
			if err != nil {
				out = append(out, Finding{Severity: "rot", Probe: "health", Service: name, Detail: err.Error(), Fix: "laib logs " + name, Repair: []string{"restart", name}})
			} else {
				resp.Body.Close()
				if !svc.Contract.Health.Answers(resp.StatusCode) {
					out = append(out, Finding{Severity: "rot", Probe: "health", Service: name, Detail: resp.Status, Fix: "laib logs " + name, Repair: []string{"restart", name}})
				}
			}
		}
	}
	// Drift: the box differs from the state. Not red: apply fixes it.
	sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if p, err := plan.Compute(ctx, st, sec, env.Paths, only); err == nil {
		if len(p.Changes) > 0 {
			var files []string
			for _, c := range p.Changes {
				files = append(files, c.Path)
			}
			out = append(out, Finding{Severity: "gelb", Probe: "drift", Detail: fmt.Sprintf("%d Dateien weichen vom State ab: %s", len(files), strings.Join(files, ", ")), Fix: "laib plan && laib apply", Repair: []string{"apply"}})
		}
		for _, u := range p.Unmanaged {
			out = append(out, Finding{Severity: "info", Probe: "drift", Detail: "nicht verwaltet: " + u, Fix: "laib ignore " + u + " | als Vorlage: laib propose --add"})
		}
	}
	for _, f := range append(deviceFindings(env, st), keyFindings(st, sec)...) { // device-held secrets (#665)
		if only == "" || f.Service == only { // laib repair secret <svc> asks for one service
			out = append(out, f)
		}
	}
	// Disk pressure on the data dir and the system's writable root (/var: on
	// CoreOS / is the immutable, "full" composefs and would always read red).
	for _, path := range uniq([]string{st.Box.DataDir, "/var"}) {
		var fs syscall.Statfs_t
		if err := syscall.Statfs(path, &fs); err != nil {
			continue
		}
		free := float64(fs.Bavail) / float64(fs.Blocks) * 100
		switch {
		case free < 5:
			out = append(out, Finding{Severity: "rot", Probe: "disk", Detail: fmt.Sprintf("%s hat nur %.1f%% frei", path, free)})
		case free < 15:
			out = append(out, Finding{Severity: "gelb", Probe: "disk", Detail: fmt.Sprintf("%s hat %.1f%% frei", path, free)})
		}
	}
	// The Operator: enabled but silent or down is the failure nobody notices.
	if _, f := OperatorState(OperatorFacts(ctx, env, st)); f != nil {
		out = append(out, *f)
	}
	if st.Box.Operator.Enabled && only == "" { // #543: a skill the policy keeps from its job
		out = append(out, skillFindings(mcp.SkillsFor(env, st, "ops"))...)
	}
	// The data RAID always; the hardware (#59: SMART, the box's temperature)
	// for the whole box. Both are laib-disk's (#443).
	out = append(out, diskFindings(ctx, only == "")...)
	// The box's own config (#281): install-box.sh writes it on every box, stick or migrated.
	for _, f := range []string{cmp.Or(os.Getenv("LAIB_ETC"), "/etc") + "/systemd/journald.conf.d/50-laib.conf", filepath.Join(filepath.Dir(env.Paths.Quadlet), "containers.conf.d", "50-laib.conf")} {
		if _, err := os.Stat(f); err != nil && only == "" {
			out = append(out, Finding{Severity: "rot", Probe: "box", Detail: f + " fehlt: das Journal hat nicht Laibs Grenzen (vier Wochen, 4G) oder Container-Ausgaben keinen festen Log-Treiber", Fix: "install-box.sh --box-config"})
		}
	}
	// #368, #486: a staged OS update waits for laib's reboot lock; a pause, a
	// uCore box without a pin, or Zincati asking a graph that has no update
	if o, ok := OS(ctx); ok && only == "" {
		_, err := os.Stat(filepath.Join(st.Dir, "host", "image"))
		z, _ := sysd.Run(ctx, "systemctl", "is-active", "zincati.service")
		out = append(out, osFindings(o, err == nil, strings.TrimSpace(z) == "active", OSPause(), WindowText(st))...)
	}
	out = append(out, imageFindings(ctx, env.Paths.Home, only, time.Now())...) // #528
	if only == "" {
		out = append(out, selfUpdateFindings(env.Paths.Home, cmp.Or(os.Getenv("LAIB_BIN_DIR"), "/var/opt/laib"), time.Now())...) // #547
	}
	// #368: without the boot check a broken OS update stays broken until someone has SSH.
	if _, err := os.Stat(cmp.Or(os.Getenv("LAIB_ETC"), "/etc") + "/systemd/system/laib-boot-check.service"); err != nil && only == "" {
		out = append(out, Finding{Severity: "rot", Probe: "boot-check", Detail: "laib-boot-check.service fehlt: ein OS-Update, das die GPU oder Laib bricht, rollt nicht von selbst zurück", Fix: "install-box.sh --version <diese Version> (die Installation legt die Prüfung an)"})
	}
	// #445: Laib filters with its own nftables table; firewalld beside it lets only SSH in.
	if fw, _ := sysd.Run(ctx, "systemctl", "is-active", "firewalld.service"); strings.TrimSpace(fw) == "active" && only == "" {
		out = append(out, Finding{Severity: "rot", Probe: "firewall", Detail: "firewalld läuft und blockiert alles außer SSH aus dem LAN (Laibs Firewall ist die nftables-Tabelle inet laib)", Fix: "sudo systemctl mask --now firewalld.service (oder install-box.sh --box-config)"})
	}
	if only == "" {
		var fs syscall.Statfs_t
		if syscall.Statfs("/boot", &fs) == nil {
			if f := bootFinding(fs.Bavail*uint64(fs.Bsize), fs.Blocks*uint64(fs.Bsize)); f != nil {
				out = append(out, *f)
			}
		}
	}
	// The outside watchdog (#57): without it, a dead box tells nobody.
	if st.Box.Watchdog.URL == "" {
		out = append(out, Finding{Severity: "gelb", Probe: "watchdog", Detail: "kein Wächter außerhalb der Box (box.yml watchdog.url): wenn die Box ausfällt, erfährt es niemand", Fix: "einen healthchecks-artigen Ping-URL eintragen (eigener Dienst oder ein freier Anbieter); laib serve meldet sich alle fünf Minuten, Stille wird draußen zum Push"})
	}
	// The backup target: none is yellow, one on the data disk itself is a nudge
	// for a week and yellow after it (#40) — a risk, not damage (owner 2026-10-25:
	// „Rot bleibt rot“, so a deferred Box-Aufgabe can cover it; a failed off-box copy stays red).
	bf := backupFacts(env)
	installed, lastCheck, lastGreen, lastOffsite, lastRun := bf.installed, bf.RestoreTest, bf.LastGreen, bf.Offsite, bf.Nightly.Result
	switch repo := st.Box.Backup.Repo; {
	case st.Box.Backup.Offsite != nil && repo != "":
		if time.Since(lastOffsite) > 48*time.Hour { // #391: the copy off the data disk is the one a dead disk leaves
			out = append(out, Finding{Severity: "rot", Probe: "backup", Detail: "keine frische Kopie außer Haus (" + st.Box.Backup.Offsite.Repo + ") seit mehr als 48 Stunden: ein Plattenausfall nähme alles mit", Fix: "laib journal --verb backup (reason offsite); NAS erreichbar? laib backup", Topic: "offsite"})
		}
	case repo == "":
		out = append(out, Finding{Severity: "gelb", Probe: "backup", Detail: "kein Backup-Ziel (box.yml backup.repo): keine Snapshots, der Operator sichert nicht vor Reparaturen", Fix: "backup.repo setzen, z.B. /mnt/data/backup/restic oder sftp:nas:/backup/laib"})
	case strings.HasPrefix(repo, "/") && strings.HasPrefix(repo, filepath.Dir(st.Box.DataDir)):
		out = append(out, Finding{Severity: localOnlySeverity(installed), Probe: "backup", Detail: "Backup liegt nur auf der Datenplatte (" + repo + "): schützt vor Fehlern, nicht vor Plattenverlust", Fix: "Repo auf NAS oder USB (box.yml backup.repo), oder ein Spiegel (laib disk add)", Topic: "offsite"})
	}
	// a failed or stale nightly backup (#393: it is what the owner gets a mail for)
	if st.Box.Backup.Repo != "" && (lastRun == "red" || !installed.IsZero() && time.Since(installed) > 36*time.Hour && time.Since(lastGreen) > 36*time.Hour) {
		what := map[bool]string{true: "ist noch nie gelaufen"}[lastGreen.IsZero()]
		out = append(out, Finding{Severity: "rot", Probe: "backup", Detail: "die letzte Sicherung " + cmp.Or(what, "ist fehlgeschlagen oder älter als 36 Stunden"), Fix: "laib journal (verb backup); laib backup", Repair: []string{"backup"}})
	}
	if st.Box.Backup.Repo != "" {
		if _, err := os.Stat(filepath.Join(env.Paths.Home, "recovery-sheet.confirmed")); err != nil {
			out = append(out, Finding{Severity: "gelb", Probe: "recovery", Detail: "das Wiederherstellungsblatt (age-Schlüssel, restic-Passwort) ist nicht als gesichert bestätigt: ohne es ist das Backup nach Plattenverlust unlesbar", Fix: "laib recovery-sheet  → ausdrucken, dann laib recovery-sheet --confirm"})
		}
		if lastCheck.IsZero() || time.Since(lastCheck) > 35*24*time.Hour {
			out = append(out, Finding{Severity: "gelb", Probe: "restore-test", Detail: "kein grüner Wiederherstellungstest in den letzten 35 Tagen (laib backup check: restic check + Probe-Restore)", Fix: "laib backup check; der Timer laib-backup-check läuft monatlich", Repair: []string{"backup", "check"}})
		}
	}
	// The delegated ACME challenge (#378): a wrong CNAME fails every renewal, silently.
	if a := st.Box.DNS.ChallengeAlias; a != "" && only == "" {
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, render.PublicResolvers[0]+":53")
		}}
		cname, err := r.LookupCNAME(ctx, "_acme-challenge."+st.Box.Domain)
		if f := challengeFinding(st.Box.Domain, a, cname, err); f != nil {
			out = append(out, *f)
		}
	}
	if tasks, err := aufgaben.Load(st.Dir); err == nil { // #667: what the owner deferred
		out = cover(out, tasks, time.Now().Format(time.DateOnly))
	}
	for i := range out {
		out[i].Say = Say(out[i])
	}
	rank := map[string]int{"rot": 0, "gelb": 1, "info": 2}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Severity] < rank[out[j].Severity] })
	return out
}

// DiskBin is the program laib-disk (#443), beside laib.
func DiskBin() string { return cmp.Or(os.Getenv("LAIB_DISK"), "/var/opt/laib/laib-disk") }

// diskFindings: what laib-disk probe says, the program that holds the RAID,
// SMART and thermal probes outside the kernel (#443).
func diskFindings(ctx context.Context, hardware bool) []Finding {
	args := []string{"probe"}
	if hardware {
		args = append(args, "--hardware")
	}
	b, err := exec.CommandContext(ctx, DiskBin(), args...).Output()
	var out []Finding
	if err != nil || json.Unmarshal(b, &out) != nil {
		return []Finding{{Severity: "gelb", Probe: "raid", Detail: fmt.Sprintf("laib-disk antwortet nicht (%v): RAID, SMART und Temperatur sind unbekannt", err), Fix: "install-box.sh bringt laib-disk mit (" + DiskBin() + ")", Unmeasured: true}}
	}
	return out
}

// BackupFacts is what the journal says the backups did (#441): the last
// nightly run, the last green one, the last off-box copy and restore test.
type BackupFacts struct {
	Nightly                         journal.Entry `json:"nightly"`
	LastGreen, Offsite, RestoreTest time.Time
	Findings                        []Finding `json:"findings"`
	installed                       time.Time
}

func backupFacts(env *cli.Env) BackupFacts {
	var f BackupFacts
	all, _ := journal.Read(env.Paths.Journal, time.Time{})
	for _, e := range all {
		switch {
		case e.Verb == "init" && f.installed.IsZero():
			f.installed = e.T
		case e.Verb != "backup" || e.Result != "green" && e.Reason != "":
		case e.Reason == "":
			f.Nightly = e
			if e.Result == "green" {
				f.LastGreen = e.T
			}
		case e.Reason == "offsite":
			f.Offsite = e.T
		case e.Reason == "check" && e.T.After(f.RestoreTest):
			f.RestoreTest = e.T
		}
	}
	return f
}

// Backups is `laib backups` (read, #441): the backups as the chat hands see
// them — what the journal records and diag's backup findings with their fix.
func Backups(ctx context.Context, env *cli.Env, _ []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	f := backupFacts(env)
	for _, x := range Probes(ctx, env, st, "") {
		if x.Probe == "backup" || x.Probe == "restore-test" || x.Probe == "recovery" {
			f.Findings = append(f.Findings, x)
		}
	}
	if env.JSON {
		return cli.Emit(env, f)
	}
	when := func(t time.Time) string {
		if t.IsZero() {
			return "nie"
		}
		return t.Local().Format("02.01. 15:04") + fmt.Sprintf(" (vor %.0f h)", time.Since(t).Hours())
	}
	fmt.Fprintf(env.W(), "letzte Sicherung: %s %s %s\nletzte grüne:     %s\naußer Haus:       %s\nProbe-Restore:    %s\n", cmp.Or(f.Nightly.Result, "nie"), when(f.Nightly.T), f.Nightly.Detail, when(f.LastGreen), when(f.Offsite), when(f.RestoreTest))
	for _, x := range f.Findings {
		fmt.Fprintf(env.W(), "%-5s %s → %s\n", x.Severity, x.Detail, x.Fix)
	}
	return cli.ExitOK
}

// challengeFinding judges what the public DNS answers for _acme-challenge.<domain>.
func challengeFinding(domain, alias, cname string, err error) *Finding {
	name := "_acme-challenge." + domain
	fix := "beim DNS-Anbieter der Domain: " + name + " CNAME " + alias + " (docs/INSTALL.md, Wildcard-Zertifikat)"
	if err != nil {
		return &Finding{Severity: "gelb", Probe: "acme", Detail: name + " nicht auflösbar (" + err.Error() + "): ob das Zertifikat sich erneuern kann, ist offen", Fix: fix, Unmeasured: true}
	}
	if !strings.EqualFold(strings.TrimSuffix(cname, "."), strings.TrimSuffix(alias, ".")) {
		return &Finding{Severity: "rot", Probe: "acme", Detail: name + " zeigt auf " + cname + ", nicht auf " + alias + ": Caddy schreibt die Challenge dorthin, Let's Encrypt sucht sie hier, das Zertifikat erneuert sich nicht", Fix: fix}
	}
	return nil
}

// bootFinding: one kernel set (kernel and initramfs) is ~150 MiB on the
// GPU box (#368); with less free, the next OS update does not fit.
func bootFinding(free, size uint64) *Finding {
	const need = 150 << 20
	if size == 0 || free >= need {
		return nil
	}
	return &Finding{Severity: "gelb", Probe: "boot", Detail: fmt.Sprintf("/boot hat %d MiB frei, ein Kernel-Satz braucht ~150 MiB: das nächste OS-Update passt nicht", free>>20), Fix: "erst wenn der jetzige Stand bewiesen ist (/var/lib/laib/boot-proven): sudo rpm-ostree cleanup -r räumt den Rückfall-Stand weg"}
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// localOnlySeverity: a backup on the data disk alone is info for the first
// week after install, then gelb — never red, it is a risk, not a failure.
func localOnlySeverity(installed time.Time) string {
	if !installed.IsZero() && time.Since(installed) > 7*24*time.Hour {
		return "gelb"
	}
	return "info"
}
