package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
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
		fmt.Printf("── %s ──\n%s\n", unit, out[unit])
	}
	return exitOK
}

// cmdRollback reverts one state commit (default: the last one) and applies.
func cmdRollback(ctx context.Context, env *cliEnv, args []string) int {
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "apply") {
		fmt.Fprintf(env.err(), "laib: %s may not rollback\n", env.actor)
		return exitRefused
	}
	commit := "HEAD"
	if len(args) > 0 {
		commit = args[0]
	}
	subject, err := sysd.Run(ctx, "git", "-C", env.paths.State, "log", "-1", "--format=%h %s", commit)
	if err != nil {
		return fail(fmt.Errorf("unknown commit %s: %s", commit, subject))
	}
	if out, err := sysd.Run(ctx, "git", "-C", env.paths.State, "-c", "user.name=laib", "-c", "user.email=laib@localhost", "revert", "--no-edit", commit); err != nil {
		return fail(fmt.Errorf("revert: %s", out))
	}
	journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "rollback", Result: "info", Detail: subject})
	st, err = loadState(env)
	if err != nil {
		return fail(err)
	}
	return runApply(ctx, env, st, "", "rollback "+subject)
}

var _ = time.Now
