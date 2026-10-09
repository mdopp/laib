package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

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
	only := ""
	if len(args) > 0 {
		only = args[0]
	}
	findings := runProbes(ctx, env, st, only)
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
	// Disk pressure on the data dir and the system root.
	for _, path := range uniq([]string{st.Box.DataDir, "/"}) {
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
