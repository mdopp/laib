package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/sysd"
)

// cmdConverge is the mechanical half of repair (docs/DESIGN.md "Repair"):
// bring the box back to the state, never the state to the box. It re-applies
// drifted files and restarts units that are not active, within a budget;
// when the budget is spent or a conflict blocks it, it stops and tells the
// human through the journal and the webhook. It never touches the state
// repo, never generates a secret anew, and never decides anything.
func cmdConverge(ctx context.Context, env *cliEnv, args []string) int {
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	if !policy.ConvergeOn(st.Policy) && !has(args, "--force") {
		fmt.Println("converge ist aus (policy.yml autonomy.converge: on schaltet die Schleife ein; --force für einen Lauf)")
		return exitOK
	}
	sec, err := secrets.Load(env.paths.Secrets, env.paths.Key)
	if err != nil {
		return fail(err)
	}
	p, err := plan.Compute(ctx, st, sec, env.paths, "")
	if err != nil {
		return fail(err)
	}
	if len(p.Conflicts) > 0 {
		record(env, st, journal.Entry{Actor: env.actor, Verb: "converge", Result: "red", Detail: "KONFLIKT: " + strings.Join(p.Conflicts, "; ")})
		return exitRed
	}
	var inactive []string
	for _, name := range st.ServiceNames() {
		for _, u := range st.Services[name].UnitNames() {
			if a := sysd.Active(ctx, u); a != "active" && a != "activating" {
				inactive = append(inactive, name)
				break
			}
		}
	}
	fw, fwErr := ensureFirewall(ctx, env)
	if fwErr != nil {
		record(env, st, journal.Entry{Actor: env.actor, Verb: "converge", Result: "red", Detail: "firewall: " + fwErr.Error()})
		return exitRed
	}
	if fw {
		record(env, st, journal.Entry{Actor: env.actor, Verb: "converge", Result: "info", Detail: "firewall: rendered ruleset loaded into the kernel"})
	}
	if p.Empty() && len(inactive) == 0 {
		if env.json {
			return emit(env, map[string]any{"converged": true})
		}
		fmt.Println("konvergent: Box entspricht dem State, alle Units aktiv")
		return exitOK
	}
	// Budget: three converge applies per hour; a service that needs more is
	// flapping, and flapping is a bug for a human or the ops hand, not a loop.
	since := time.Now().Add(-time.Hour)
	recent, _ := journal.Read(env.paths.Journal, since)
	runs := 0
	for _, e := range recent {
		if e.Verb == "converge" && e.Result != "info" {
			runs++
		}
	}
	if runs >= policy.ConvergeBudget {
		record(env, st, journal.Entry{Actor: env.actor, Verb: "converge", Result: "red", Detail: fmt.Sprintf("Budget erschöpft (%d/h): %d Dateien Drift, nicht aktiv: %s. Ein Mensch oder die Ops-Hand muss ran.", policy.ConvergeBudget, len(p.Changes), strings.Join(inactive, ", "))})
		return exitRed
	}
	reason := fmt.Sprintf("converge: %d Dateien Drift, nicht aktiv: %s", len(p.Changes), strings.Join(inactive, ", "))
	// Units that are down but whose files match get a restart through apply
	// by marking their service as changed.
	for _, name := range inactive {
		found := false
		for _, s := range p.Services {
			if s == name {
				found = true
			}
		}
		if !found {
			p.Services = append(p.Services, name)
		}
	}
	code := runApplyPlan(ctx, env, st, p, sec, reason, "converge")
	return code
}

func has(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

var _ = proposal.Hand
