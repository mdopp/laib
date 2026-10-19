// Package diag is `laib diag`: the probes (units, health, drift, disk
// pressure, RAID, SMART, temperature, the Operator's heartbeat, the backup
// and recovery gaps) and their findings. A box chore, not the trust kernel
// (ADR 0005 amendment #309): it reads and reports, it never fixes.
package diag

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/disk"
	"github.com/mdopp/laib/internal/journal"
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
				out = append(out, Finding{Severity: "rot", Probe: "unit", Service: name, Detail: u + " ist " + a, Fix: "laib logs " + name})
			}
		}
		for _, u := range svc.Oneshots() {
			if sysd.Active(ctx, u) == "failed" { // a timer's run failed (#195)
				out = append(out, Finding{Severity: "rot", Probe: "unit", Service: name, Detail: u + " ist failed", Fix: "laib logs " + name})
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
				out = append(out, Finding{Severity: "rot", Probe: "health", Service: name, Detail: err.Error(), Fix: "laib logs " + name})
			} else {
				resp.Body.Close()
				if !svc.Contract.Health.Answers(resp.StatusCode) {
					out = append(out, Finding{Severity: "rot", Probe: "health", Service: name, Detail: resp.Status, Fix: "laib logs " + name})
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
			out = append(out, Finding{Severity: "gelb", Probe: "drift", Detail: fmt.Sprintf("%d Dateien weichen vom State ab: %s", len(files), strings.Join(files, ", ")), Fix: "laib plan && laib apply"})
		}
		for _, u := range p.Unmanaged {
			out = append(out, Finding{Severity: "info", Probe: "drift", Detail: "nicht verwaltet: " + u, Fix: "laib adopt " + u + " | laib ignore " + u})
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
	// The hardware (#59): disks by SMART, the box's temperature.
	if only == "" {
		out = append(out, hardwareFindings(ctx)...)
	}
	// The box's own config (#281): install-box.sh writes it on every box, stick or migrated.
	for _, f := range []string{cmp.Or(os.Getenv("LAIB_ETC"), "/etc") + "/systemd/journald.conf.d/50-laib.conf", filepath.Join(filepath.Dir(env.Paths.Quadlet), "containers.conf.d", "50-laib.conf")} {
		if _, err := os.Stat(f); err != nil && only == "" {
			out = append(out, Finding{Severity: "rot", Probe: "box", Detail: f + " fehlt: das Journal hat nicht Laibs Grenzen (vier Wochen, 4G) oder Container-Ausgaben keinen festen Log-Treiber", Fix: "install-box.sh --box-config (docs/MIGRATION.md Schritt 1)"})
		}
	}
	// The outside watchdog (#57): without it, a dead box tells nobody.
	if st.Box.Watchdog.URL == "" {
		out = append(out, Finding{Severity: "gelb", Probe: "watchdog", Detail: "kein Wächter außerhalb der Box (box.yml watchdog.url): wenn die Box ausfällt, erfährt es niemand", Fix: "einen healthchecks-artigen Ping-URL eintragen (eigener Dienst oder ein freier Anbieter); laib serve meldet sich alle fünf Minuten, Stille wird draußen zum Push"})
	}
	// The backup target: none is yellow, one on the data disk itself is a nudge
	// for a week and red after it (#40) — a dead disk then takes the backup with it.
	all, _ := journal.Read(env.Paths.Journal, time.Time{})
	var installed time.Time
	var lastCheck time.Time
	for _, e := range all {
		if e.Verb == "init" && installed.IsZero() {
			installed = e.T
		}
		if e.Verb == "backup" && e.Reason == "check" && e.Result == "green" && e.T.After(lastCheck) {
			lastCheck = e.T
		}
	}
	switch repo := st.Box.Backup.Repo; {
	case repo == "":
		out = append(out, Finding{Severity: "gelb", Probe: "backup", Detail: "kein Backup-Ziel (box.yml backup.repo): keine Snapshots, der Operator sichert nicht vor Reparaturen", Fix: "backup.repo setzen, z.B. /mnt/data/backup/restic oder sftp:nas:/backup/laib"})
	case strings.HasPrefix(repo, "/") && strings.HasPrefix(repo, filepath.Dir(st.Box.DataDir)):
		sev := "info"
		if !installed.IsZero() && time.Since(installed) > 7*24*time.Hour {
			sev = "rot"
		}
		out = append(out, Finding{Severity: sev, Probe: "backup", Detail: "Backup liegt nur auf der Datenplatte (" + repo + "): schützt vor Fehlern, nicht vor Plattenverlust", Fix: "Repo auf NAS oder USB (box.yml backup.repo), oder ein Spiegel (laib disk add)"})
	}
	if st.Box.Backup.Repo != "" {
		if _, err := os.Stat(filepath.Join(env.Paths.Home, "recovery-sheet.confirmed")); err != nil {
			out = append(out, Finding{Severity: "gelb", Probe: "recovery", Detail: "das Wiederherstellungsblatt (age-Schlüssel, restic-Passwort) ist nicht als gesichert bestätigt: ohne es ist das Backup nach Plattenverlust unlesbar", Fix: "laib recovery-sheet  → ausdrucken, dann laib recovery-sheet --confirm"})
		}
		if lastCheck.IsZero() || time.Since(lastCheck) > 35*24*time.Hour {
			out = append(out, Finding{Severity: "gelb", Probe: "restore-test", Detail: "kein grüner Wiederherstellungstest in den letzten 35 Tagen (laib backup check: restic check + Probe-Restore)", Fix: "laib backup check; der Timer laib-backup-check läuft monatlich"})
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
	// The data RAID: one member by design is yellow, a lost member red.
	if _, members := disk.MarkerMembers(); members > 0 {
		if b, err := os.ReadFile("/proc/mdstat"); err == nil {
			out = append(out, raidFindings(string(b), members)...)
		}
	}
	rank := map[string]int{"rot": 0, "gelb": 1, "info": 2}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Severity] < rank[out[j].Severity] })
	return out
}

// challengeFinding judges what the public DNS answers for _acme-challenge.<domain>.
func challengeFinding(domain, alias, cname string, err error) *Finding {
	name := "_acme-challenge." + domain
	fix := "beim DNS-Anbieter der Domain: " + name + " CNAME " + alias + " (docs/INSTALL.md, Wildcard-Zertifikat)"
	if err != nil {
		return &Finding{Severity: "gelb", Probe: "acme", Detail: name + " nicht auflösbar (" + err.Error() + "): ob das Zertifikat sich erneuern kann, ist offen", Fix: fix}
	}
	if !strings.EqualFold(strings.TrimSuffix(cname, "."), strings.TrimSuffix(alias, ".")) {
		return &Finding{Severity: "rot", Probe: "acme", Detail: name + " zeigt auf " + cname + ", nicht auf " + alias + ": Caddy schreibt die Challenge dorthin, Let's Encrypt sucht sie hier, das Zertifikat erneuert sich nicht", Fix: fix}
	}
	return nil
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
