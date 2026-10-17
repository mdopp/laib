// Package converge is the mechanical half of repair (#309, docs/DESIGN.md
// "Repair"): the box back to the state within a budget, never the state to
// the box. Trust kernel.
package converge

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/run"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/sysd"
)

// Converge is the mechanical half of repair (docs/DESIGN.md "Repair"):
// bring the box back to the state, never the state to the box. It re-applies
// drifted files and restarts units that are not active, within a budget;
// when the budget is spent or a conflict blocks it, it stops and tells the
// human through the journal and the webhook. It never touches the state
// repo, never generates a secret anew, and never decides anything.
func Converge(ctx context.Context, env *cli.Env, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if !policy.ConvergeOn(st.Policy) && !slices.Contains(args, "--force") {
		fmt.Fprintln(env.W(), "converge ist aus (policy.yml autonomy.converge: on schaltet die Schleife ein; --force für einen Lauf)")
		return cli.ExitOK
	}
	// one lock (#39): a tick beside a restore, a remove or a human's apply is
	// skipped, never run alongside
	if holder := apply.LockHolder(env.Paths); holder != "" {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "converge", Result: "info", Detail: "übersprungen: gesperrt durch " + holder})
		fmt.Fprintln(env.W(), "übersprungen: gesperrt durch", holder)
		return cli.ExitOK
	}
	unlock, code := run.Lock(env, st, "converge")
	if code != cli.ExitOK {
		return code
	}
	defer unlock()
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return env.Fail(err)
	}
	p, err := plan.Compute(ctx, st, sec, env.Paths, "")
	if err != nil {
		return env.Fail(err)
	}
	if len(p.Conflicts) > 0 {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "converge", Result: "red", Detail: "KONFLIKT: " + strings.Join(p.Conflicts, "; ")})
		return cli.ExitRed
	}
	// paused services (#39): a human's stop, a restore, the lease — not ours to restart
	paused := apply.Paused(env.Paths)
	var pausedNames []string
	for n, pz := range paused {
		pausedNames = append(pausedNames, fmt.Sprintf("%s (von %s bis %s)", n, pz.By, pz.Until.Local().Format("02.01. 15:04")))
	}
	sort.Strings(pausedNames)
	var inactive []string
	for _, name := range st.ServiceNames() {
		if _, ok := paused[name]; ok {
			continue
		}
		for _, u := range st.Services[name].UnitNames() {
			if a := sysd.Active(ctx, u); a != "active" && a != "activating" {
				inactive = append(inactive, name)
				break
			}
		}
	}
	fw, fwErr := apply.EnsureFirewall(ctx, env.Paths)
	if fwErr != nil {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "converge", Result: "red", Detail: "firewall: " + fwErr.Error()})
		return cli.ExitRed
	}
	if fw {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "converge", Result: "info", Detail: "firewall: rendered ruleset loaded into the kernel"})
	}
	pending := apply.Pending(env.Paths)
	if len(pausedNames) > 0 {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "converge", Result: "info", Detail: "pausiert, nicht angefasst: " + strings.Join(pausedNames, "; ")})
	}
	if p.Empty() && len(inactive) == 0 && len(pending) == 0 {
		if env.JSON {
			return cli.Emit(env, map[string]any{"converged": true, "paused": pausedNames})
		}
		fmt.Fprintln(env.W(), "konvergent: Box entspricht dem State, alle Units aktiv")
		return cli.ExitOK
	}
	// Budget: three converge applies per hour; a service that needs more is
	// flapping, and flapping is a bug for a human or the ops hand, not a loop.
	since := time.Now().Add(-time.Hour)
	recent, _, err := journal.ReadCounted(env.Paths.Journal, since)
	if err != nil {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "converge", Result: "red", Detail: "Journal nicht lesbar, kein Converge: " + err.Error()})
		return cli.ExitRed
	}
	runs := 0
	for _, e := range recent {
		if e.Verb == "converge" && e.Result != "info" {
			runs++
		}
	}
	if runs >= policy.ConvergeBudget {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "converge", Result: "red", Detail: fmt.Sprintf("Budget erschöpft (%d/h): %d Dateien Drift, nicht aktiv: %s. Ein Mensch oder die Ops-Hand muss ran.", policy.ConvergeBudget, len(p.Changes), strings.Join(inactive, ", "))})
		return cli.ExitRed
	}
	reason := fmt.Sprintf("converge: %d Dateien Drift, nicht aktiv: %s", len(p.Changes), strings.Join(inactive, ", "))
	if len(pending) > 0 {
		reason += "; Aktivierung ausstehend: " + strings.Join(pending, ", ")
	}
	// Units that are down but whose files match get a restart through apply
	// by marking their service as changed.
	for _, name := range inactive {
		if !slices.Contains(p.Services, name) {
			p.Services = append(p.Services, name)
		}
	}
	return run.ApplyPlan(ctx, env, st, p, sec, reason, "converge", true)
}
