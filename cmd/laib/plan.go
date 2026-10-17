package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/run"
	"github.com/mdopp/laib/internal/secrets"
)

func cmdPlan(ctx context.Context, env *cliEnv, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return env.Fail(err)
	}
	only := ""
	if len(args) > 0 {
		only = args[0]
	}
	p, err := plan.Compute(ctx, st, sec, env.Paths, only)
	if err != nil {
		return env.Fail(err)
	}
	if env.JSON {
		return cli.Emit(env, p)
	}
	if p.Empty() {
		fmt.Fprintln(env.W(), "Keine Änderung: die Box entspricht dem State.")
	}
	for _, c := range p.Changes {
		fmt.Fprintf(env.W(), "%-6s %s\n", c.Kind, c.Target)
		if c.Diff != "" {
			fmt.Fprintln(env.W(), c.Diff)
		}
	}
	for _, u := range p.Unmanaged {
		fmt.Fprintf(env.W(), "nicht verwaltet: %s   (laib adopt %s | laib ignore %s)\n", u, u, u)
	}
	for _, c := range p.Conflicts {
		fmt.Fprintf(env.W(), "KONFLIKT: %s   (erst die andere Quelle stoppen und entfernen, dann apply)\n", c)
	}
	for _, w := range st.Warnings {
		fmt.Fprintf(env.W(), "HINWEIS: %s\n", w)
	}
	for _, o := range st.Off {
		fmt.Fprintf(env.W(), "AUS: %s\n", o)
	}
	return cli.ExitOK
}

func cmdApply(ctx context.Context, env *cliEnv, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	hand := strings.SplitN(env.Actor, ":", 2)[0]
	if !policy.Allowed(st.Policy, hand, "apply") {
		return cli.Refused(env, fmt.Sprintf("%s may not apply (ceiling %s). Make a proposal: laib propose \"<grund>\"", env.Actor, st.Policy.Hands[hand].Ceiling))
	}
	if hand == "ops" {
		return envelope.OpsApply(ctx, env, st, args)
	}
	only, reason := "", ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--reason" && i+1 < len(args):
			reason = args[i+1]
			i++
		default:
			only = args[i]
		}
	}
	return run.Apply(ctx, env, st, only, reason)
}
