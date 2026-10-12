package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// cmdLogs reads journald for every unit of a service, secrets redacted
// (ADR 0003: journald is the agent).
func cmdLogs(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return fail(errors.New("usage: laib logs <service> [--since 1h] [--lines 200]"))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	svc, ok := st.Services[args[0]]
	if !ok {
		return fail(fmt.Errorf("unknown service %q", args[0]))
	}
	since, lines := "1h", "200"
	for i := 1; i+1 < len(args); i += 2 {
		switch args[i] {
		case "--since":
			since = args[i+1]
		case "--lines":
			lines = args[i+1]
		}
	}
	sec, _ := secrets.Load(env.paths.Secrets, env.paths.Key)
	out := map[string]string{}
	for _, unit := range svc.UnitNames() {
		text, _ := sysd.Run(ctx, "journalctl", "--user", "-u", unit, "--since", "-"+since, "-n", lines, "--no-pager", "-o", "short-iso")
		for k, v := range sec {
			if v != "" {
				text = strings.ReplaceAll(text, v, "${secret:"+k+"}")
			}
		}
		out[unit] = text
	}
	if env.json {
		return emit(env, out)
	}
	for _, unit := range svc.UnitNames() {
		fmt.Fprintf(env.w(), "── %s ──\n%s\n", unit, out[unit])
	}
	return exitOK
}

// cmdRollback: `laib rollback [#<proposal> | <commit>]` (#41). By proposal
// it reverts the approve merge (-m 1) without touching secrets.age, drops
// the secrets of services that leave the state, and applies — the plan's
// manifest then stops and removes the units and files. By commit the
// argument is validated first (never an option for git); a merge commit
// is reverted with -m 1.
func cmdRollback(ctx context.Context, env *cliEnv, args []string) int {
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "apply") {
		fmt.Fprintf(env.err(), "laib: %s may not rollback\n", env.actor)
		return exitRefused
	}
	arg := "HEAD"
	if len(args) > 0 {
		arg = args[0]
	}
	if strings.HasPrefix(arg, "-") {
		return fail(fmt.Errorf("%q is not a commit", arg))
	}
	unlock, code := takeLock(env, st, "rollback")
	if code != exitOK {
		return code
	}
	defer unlock()
	if strings.HasPrefix(arg, "#") {
		id, err := strconv.Atoi(strings.TrimPrefix(arg, "#"))
		if err != nil {
			return fail(fmt.Errorf("%q is not a proposal", arg))
		}
		subject, err := revertProposal(ctx, env, st, id)
		if err != nil {
			return fail(err)
		}
		st, err = loadState(env)
		if err != nil {
			return fail(err)
		}
		return applyHeld(ctx, env, st, "", "rollback "+subject)
	}
	sha, err := sysd.Run(ctx, "git", "-C", env.paths.State, "rev-parse", "--verify", "--quiet", "--end-of-options", arg+"^{commit}")
	if err != nil || strings.TrimSpace(sha) == "" {
		return fail(fmt.Errorf("unknown commit %q", arg))
	}
	sha = strings.TrimSpace(sha)
	subject, _ := sysd.Run(ctx, "git", "-C", env.paths.State, "log", "-1", "--format=%h %s", "--end-of-options", sha)
	subject = strings.TrimSpace(subject)
	revert := []string{"-C", env.paths.State, "-c", "user.name=laib", "-c", "user.email=laib@localhost", "revert", "--no-edit"}
	if parents, _ := sysd.Run(ctx, "git", "-C", env.paths.State, "rev-list", "--parents", "-n", "1", sha); len(strings.Fields(parents)) > 2 {
		revert = append(revert, "-m", "1")
	}
	if out, err := sysd.Run(ctx, "git", append(revert, sha)...); err != nil {
		sysd.Run(ctx, "git", "-C", env.paths.State, "revert", "--abort")
		return fail(fmt.Errorf("revert: %s", out))
	}
	journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "rollback", Result: "info", Detail: subject})
	st, err = loadState(env)
	if err != nil {
		return fail(err)
	}
	return applyHeld(ctx, env, st, "", "rollback "+subject)
}

// revertProposal reverts the approve merge of proposal id (#41): the tree
// without the proposal, secrets.age as it is now (the running services'
// secrets stay), minus the secrets of services the revert takes out of
// the state. Returns the subject for the journal.
func revertProposal(ctx context.Context, env *cliEnv, st *state.State, id int) (string, error) {
	g := func(args ...string) (string, error) {
		return sysd.Run(ctx, "git", append([]string{"-C", env.paths.State, "-c", "user.name=laib", "-c", "user.email=laib@localhost"}, args...)...)
	}
	merge, err := g("log", "--format=%H", "--grep=^approve #"+strconv.Itoa(id)+" by ", "-n", "1", "main")
	if err != nil || strings.TrimSpace(merge) == "" {
		return "", fmt.Errorf("no approve of #%d in the state's history", id)
	}
	merge = strings.TrimSpace(merge)
	before := st.ServiceNames()
	if out, err := g("revert", "--no-commit", "-m", "1", merge); err != nil {
		g("revert", "--abort")
		return "", fmt.Errorf("revert of #%d: %s", id, out)
	}
	// secrets.age stays what it is: the services still there keep the
	// secrets their data was made with
	g("checkout", "HEAD", "--", "secrets.age")
	if out, err := g("commit", "-q", "-m", fmt.Sprintf("rollback #%d", id)); err != nil {
		g("revert", "--abort")
		return "", fmt.Errorf("rollback commit: %s", out)
	}
	after, err := loadState(env)
	if err != nil {
		return "", fmt.Errorf("the state without #%d does not load: %w", id, err)
	}
	gone := map[string]bool{}
	for _, n := range before {
		if _, ok := after.Services[n]; !ok {
			gone[n] = true
		}
	}
	if len(gone) > 0 {
		sec, err := secrets.Load(env.paths.Secrets, env.paths.Key)
		if err == nil {
			for n := range gone {
				for _, sdecl := range st.Services[n].Contract.Secrets {
					delete(sec, sdecl.Name)
				}
			}
			if err := secrets.Save(env.paths.Secrets, env.paths.Key, sec); err != nil {
				return "", err
			}
			g("add", "-A")
			g("commit", "-q", "-m", fmt.Sprintf("rollback #%d: secrets of removed services dropped", id))
		}
	}
	subject := fmt.Sprintf("#%d zurückgenommen", id)
	journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "rollback", Result: "info", Detail: subject + " (" + merge[:8] + ")"})
	return subject, nil
}

var _ = time.Now
