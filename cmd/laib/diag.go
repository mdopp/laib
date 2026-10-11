package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/plan"
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

// cmdDiag runs the built-in probes: failed units, drift between state and
// box, health URLs, disk pressure. Red first. Probes are deliberately few
// (docs/DESIGN.md: about ten remain); each new one needs a recurrence.
func cmdDiag(ctx context.Context, env *cliEnv, args []string) int {
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	only, toJournal := "", false
	for _, a := range args {
		if a == "--journal" {
			toJournal = true
			continue
		}
		only = a
	}
	findings := runProbes(ctx, env, st, only)
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
				record(env, st, journal.Entry{Actor: env.actor, Verb: "diag", Service: f.Service, Result: "red", Detail: f.Probe + ": " + f.Detail})
			}
		}
		if red == 0 {
			record(env, st, journal.Entry{Actor: env.actor, Verb: "diag", Result: "green", Detail: "keine roten Befunde"})
		}
	}
	if env.json {
		return emit(env, findings)
	}
	if len(findings) == 0 {
		fmt.Println("Keine Befunde.")
	}
	for _, f := range findings {
		fmt.Printf("%-5s %-8s %-14s %s", f.Severity, f.Probe, f.Service, f.Detail)
		if f.Fix != "" {
			fmt.Printf("   → %s", f.Fix)
		}
		fmt.Println()
	}
	return exitOK
}

func runProbes(ctx context.Context, env *cliEnv, st *state.State, only string) []Finding {
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
		if h := svc.Contract.Health.HTTP; h != "" {
			client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h, nil)
			resp, err := client.Do(req)
			if err != nil {
				out = append(out, Finding{Severity: "rot", Probe: "health", Service: name, Detail: err.Error(), Fix: "laib logs " + name})
			} else {
				resp.Body.Close()
				if resp.StatusCode >= 500 {
					out = append(out, Finding{Severity: "rot", Probe: "health", Service: name, Detail: resp.Status, Fix: "laib logs " + name})
				}
			}
		}
	}
	// Drift: the box differs from the state. Not red: apply fixes it.
	sec, _ := secrets.Load(env.paths.Secrets, env.paths.Key)
	if p, err := plan.Compute(ctx, st, sec, env.paths, only); err == nil {
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
	if _, f := operatorState(operatorFacts(ctx, env, st)); f != nil {
		out = append(out, *f)
	}
	// The outside watchdog (#57): without it, a dead box tells nobody.
	if st.Box.Watchdog.URL == "" {
		out = append(out, Finding{Severity: "gelb", Probe: "watchdog", Detail: "kein Wächter außerhalb der Box (box.yml watchdog.url): wenn die Box ausfällt, erfährt es niemand", Fix: "einen healthchecks-artigen Ping-URL eintragen (eigener Dienst oder ein freier Anbieter); laib serve meldet sich alle fünf Minuten, Stille wird draußen zum Push"})
	}
	// The backup target: none is yellow, one on the data disk itself is a nudge.
	switch repo := st.Box.Backup.Repo; {
	case repo == "":
		out = append(out, Finding{Severity: "gelb", Probe: "backup", Detail: "kein Backup-Ziel (box.yml backup.repo): keine Snapshots, der Operator sichert nicht vor Reparaturen", Fix: "backup.repo setzen, z.B. /mnt/data/backup/restic oder sftp:nas:/backup/laib"})
	case strings.HasPrefix(repo, "/") && strings.HasPrefix(repo, filepath.Dir(st.Box.DataDir)):
		out = append(out, Finding{Severity: "info", Probe: "backup", Detail: "Backup liegt auf der Datenplatte (" + repo + "): schützt vor Fehlern, nicht vor Plattenverlust", Fix: "zweites Repo auf NAS oder USB (backup.repo), oder ein Spiegel (laib disk add)"})
	}
	// The data RAID: one member by design is yellow, a lost member red.
	if _, members := markerMembers(); members > 0 {
		if b, err := os.ReadFile("/proc/mdstat"); err == nil {
			out = append(out, raidFindings(string(b), members)...)
		}
	}
	rank := map[string]int{"rot": 0, "gelb": 1, "info": 2}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Severity] < rank[out[j].Severity] })
	return out
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

var _ = os.Stat
