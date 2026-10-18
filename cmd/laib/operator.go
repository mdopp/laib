package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/token"
)

// cmdOperator: `laib operator enable|disable|status`. enable mints the ops
// token (humans only), stores it as OPERATOR_TOKEN in secrets.age, sets
// box.yml operator.enabled and applies, which renders and starts the unit.
func cmdOperator(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return env.Fail(errors.New("usage: laib operator enable [--model alias] [--report-at 07:00] | disable | status"))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	switch args[0] {
	case "status":
		on := st.Box.Operator.Enabled
		if env.JSON {
			return cli.Emit(env, map[string]any{"enabled": on, "model": st.Box.Operator.Model, "modelUrl": st.ModelURL()})
		}
		fmt.Fprintf(env.W(), "operator: enabled=%v model=%s url=%s\n", on, st.Box.Operator.Model, st.ModelURL())
		return cli.ExitOK
	case "enable", "disable":
		if proposal.Hand(env.Actor) != "human" {
			return cli.Refused(env, "only a human enables or disables the operator")
		}
		if !policy.Allowed(st.Policy, "ops", "apply") {
			return env.Fail(errors.New("policy.yml: the ops hand needs ceiling apply for the operator"))
		}
		enable := args[0] == "enable"
		model, reportAt := "", ""
		for i := 1; i+1 < len(args); i += 2 {
			switch args[i] {
			case "--model":
				model = args[i+1]
			case "--report-at":
				reportAt = args[i+1]
			}
		}
		if enable {
			if st.ModelURL() == "" {
				return env.Fail(errors.New("no model: set box.yml model.url or a model role first"))
			}
			sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
			if err != nil {
				return env.Fail(err)
			}
			if _, ok := sec[render.OperatorToken]; !ok {
				store, err := token.Load(cli.TokenPath(env))
				if err != nil {
					return env.Fail(err)
				}
				plain, rec, err := store.Mint("ops", 0, "laib-operator")
				if err != nil {
					return env.Fail(err)
				}
				sec[render.OperatorToken] = plain
				if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
					return env.Fail(err)
				}
				journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "token", Result: "info", Detail: "mint " + rec.ID + " for ops (laib-operator)"})
			}
		}
		if err := state.SetBlock(filepath.Join(env.Paths.State, "box.yml"), "operator", "enabled", fmt.Sprint(enable), "model", model, "reportAt", reportAt); err != nil {
			return env.Fail(err)
		}
		if enable {
			// reds wake the operator (#43): the webhook points at its event endpoint unless the owner set another
			if st.Box.Notify.URL == "" {
				if err := state.SetBlock(filepath.Join(env.Paths.State, "box.yml"), "notify", "url", "http://127.0.0.1:5901/event"); err != nil {
					return env.Fail(err)
				}
			}
		}
		if !enable {
			// nothing keeps running with a valid token (#41): the ops tokens go
			if store, err := token.Load(cli.TokenPath(env)); err == nil {
				for _, r := range store.List() {
					if r.Hand == "ops" {
						store.Revoke(r.ID)
						journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "token", Result: "info", Detail: "revoke " + r.ID + " (operator disabled)"})
					}
				}
			}
		}
		journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "operator", Result: "info", Detail: args[0]})
		st, err = cli.LoadState(env)
		if err != nil {
			return env.Fail(err)
		}
		return apply.ApplyLocked(ctx, env, st, "", "operator "+args[0])
	}
	return env.Fail(fmt.Errorf("unknown operator subcommand %q", args[0]))
}
