package apply

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/token"
)

// ApplyLocked is the apply mechanics after the policy check: lock, secrets,
// plan, apply, journal. Used by `laib apply` and by an approval.
func ApplyLocked(ctx context.Context, env *cli.Env, st *state.State, only, reason string) int {
	unlock, code := TakeLock(env, st, "apply")
	if code != cli.ExitOK {
		return code
	}
	defer unlock()
	return ApplyHeld(ctx, env, st, only, reason)
}

// TakeLock takes the one lock (#39) for a verb; a stale lock (its process
// gone) is taken over with a journal line, a live one refuses with exitLocked.
func TakeLock(env *cli.Env, st *state.State, verb string) (func(), int) {
	unlock, stale, err := Lock(env.Paths, verb+":"+env.Actor)
	if errors.Is(err, ErrLocked) {
		fmt.Fprintln(env.Errw(), "laib: eingeschränkt, gesperrt durch", LockHolder(env.Paths))
		return nil, cli.ExitLocked
	}
	if err != nil {
		return nil, env.Fail(err)
	}
	if stale != "" {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: verb, Result: "info", Detail: "Sperre übernommen: " + stale + " (Prozess weg)"})
	}
	return unlock, cli.ExitOK
}

// ApplyHeld is apply under a lock the caller holds.
func ApplyHeld(ctx context.Context, env *cli.Env, st *state.State, only, reason string) int {

	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return env.Fail(err)
	}
	changed := false
	// a refused apply (#227) generates and mints nothing: the plan below refuses
	conflict := len(plan.Conflicts(st, env.Paths)) > 0
	for _, s := range render.SecretNames(st) {
		if _, ok := sec[s.Name]; ok || conflict {
			continue
		}
		if s.External {
			if s.Mint != "" {
				// a Laib token the service needs: minted here, once, as the hand the template names
				h, ok := st.Policy.Hands[s.Mint]
				if !ok || s.Mint == "human" {
					fmt.Fprintf(env.Errw(), "laib: secret %s wants a token for hand %q, which policy.yml does not have (or is human)\n", s.Name, s.Mint)
					return cli.ExitUsage
				}
				store, err := token.Load(cli.TokenPath(env))
				if err != nil {
					return env.Fail(err)
				}
				ttl := time.Duration(0)
				if h.TTL != "" {
					ttl, _ = time.ParseDuration(h.TTL)
				}
				plain, rec, err := store.Mint(s.Mint, ttl, s.Name)
				if err != nil {
					return env.Fail(err)
				}
				journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "token", Result: "info", Detail: fmt.Sprintf("mint %s for %s (%s)", rec.ID, s.Mint, s.Name)})
				sec[s.Name] = plain
				changed = true
				continue
			}
			if s.Optional {
				continue
			}
			fmt.Fprintf(env.Errw(), "laib: secret %s is external and not set; add it to secrets.age first\n", s.Name)
			return cli.ExitUsage
		}
		sec[s.Name] = secrets.Generate()
		changed = true
	}
	if changed {
		if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
			return env.Fail(err)
		}
	}

	p, err := plan.Compute(ctx, st, sec, env.Paths, only)
	if err != nil {
		return env.Fail(err)
	}
	return ApplyPlan(ctx, env, st, p, sec, reason, "apply", true)
}

// ApplyPlan applies a computed plan under the lock the caller holds (or
// takes it when none is held), journals as `verb`, stores hook secrets.
func ApplyPlan(ctx context.Context, env *cli.Env, st *state.State, p *plan.Plan, sec map[string]string, reason, verb string, held bool) int {
	only := p.Only
	if !held {
		unlock, code := TakeLock(env, st, verb)
		if code != cli.ExitOK {
			return code
		}
		defer unlock()
	}
	// an apply killed between write and restart (#39): its services are
	// restarted and verified now, even with a clean file diff
	for _, name := range Pending(env.Paths) {
		if _, ok := st.Services[name]; ok && !slices.Contains(p.Services, name) {
			p.Services = append(p.Services, name)
		}
	}
	start := time.Now()
	res, err := Run(ctx, st, p, env.Paths, sec, reason)
	entry := journal.Entry{Actor: env.Actor, Verb: verb, Service: only, Reason: reason, Commit: res.Commit, MS: time.Since(start).Milliseconds()}
	if err != nil {
		entry.Result, entry.Detail = "red", err.Error()
		journal.Append(env.Paths.Journal, entry)
		return env.Fail(err)
	}
	if len(res.NewSecrets) > 0 {
		for k, v := range res.NewSecrets {
			sec[k] = v
		}
		if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
			return env.Fail(err)
		}
		// on main before green: a proposal cannot carry it off (#328)
		if err := proposal.CommitSecrets(ctx, env.Paths.State, "apply: secrets handed back by hooks"); err != nil {
			return env.Fail(err)
		}
	}
	entry.Result = "green"
	if !res.Green {
		entry.Result = "red"
		var bad []string
		for _, c := range res.Checks {
			if !c.OK {
				bad = append(bad, c.Service+": "+c.What+" "+c.Detail)
			}
		}
		entry.Detail = strings.Join(bad, "; ")
	}
	if p.Empty() && res.Green {
		entry.Result, entry.Detail = "info", "no change"
		if len(res.Restarted) > 0 { // a restart-only run is a run: it counts against converge's budget (#39)
			entry.Result, entry.Detail = "green", "neu gestartet: "+strings.Join(res.Restarted, ", ")
		}
	}
	cli.Record(env, st, entry)
	if env.JSON {
		cli.Emit(env, res)
	} else {
		for _, w := range res.Written {
			fmt.Fprintln(env.W(), "geschrieben:", w)
		}
		for _, u := range res.Restarted {
			fmt.Fprintln(env.W(), "neu gestartet:", u)
		}
		for _, c := range res.Checks {
			mark := "ok "
			if !c.OK {
				mark = "ROT"
			}
			fmt.Fprintf(env.W(), "%s %s %s %s\n", mark, c.Service, c.What, c.Detail)
		}
		if res.Green {
			fmt.Fprintf(env.W(), "grün · %s\n", res.Commit)
		} else {
			fmt.Fprintf(env.W(), "rot · %s · angewendet, aber verify ist rot\n", res.Commit)
		}
	}
	if !res.Green {
		return cli.ExitRed
	}
	return cli.ExitOK
}
