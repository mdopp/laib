package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/token"
)

// cmdToken: mint <hand> [--ttl 8h] [--note x] | list | revoke <id>. Minting
// is a destroy-tier act in spirit (it hands out a capability), so only a
// human may do it; the TTL defaults to the hand's ttl in policy.yml.
func cmdToken(_ context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return env.Fail(errors.New("usage: laib token mint <hand> [--ttl 8h] [--note text] | list | revoke <id>"))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	store, err := token.Load(cli.TokenPath(env))
	if err != nil {
		return env.Fail(err)
	}
	switch args[0] {
	case "list":
		if env.JSON {
			return cli.Emit(env, store.List())
		}
		for _, r := range store.List() {
			exp := "never"
			if !r.Expires.IsZero() {
				exp = r.Expires.Local().Format("02.01. 15:04")
			}
			fmt.Fprintf(env.W(), "%-8s %-8s created %s  expires %s  %s\n", r.ID, r.Hand, r.Created.Local().Format("02.01. 15:04"), exp, r.Note)
		}
		return cli.ExitOK
	case "revoke":
		if len(args) != 2 {
			return env.Fail(errors.New("usage: laib token revoke <id>"))
		}
		if proposal.Hand(env.Actor) != "human" {
			return cli.Refused(env, "only a human revokes tokens")
		}
		if err := store.Revoke(args[1]); err != nil {
			return env.Fail(err)
		}
		journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "token", Result: "info", Detail: "revoke " + args[1]})
		fmt.Fprintln(env.W(), "widerrufen:", args[1])
		return cli.ExitOK
	case "mint":
		if len(args) < 2 {
			return env.Fail(errors.New("usage: laib token mint <hand>"))
		}
		if proposal.Hand(env.Actor) != "human" {
			return cli.Refused(env, "only a human mints tokens")
		}
		hand := args[1]
		h, ok := st.Policy.Hands[hand]
		if !ok || hand == "human" {
			return env.Fail(fmt.Errorf("unknown hand %q (policy.yml hands, not human)", hand))
		}
		ttl, note := time.Duration(0), ""
		if h.TTL != "" {
			if d, err := time.ParseDuration(h.TTL); err == nil {
				ttl = d
			}
		}
		for i := 2; i+1 < len(args); i += 2 {
			switch args[i] {
			case "--ttl":
				d, err := time.ParseDuration(args[i+1])
				if err != nil {
					return env.Fail(err)
				}
				ttl = d
			case "--note":
				note = args[i+1]
			}
		}
		plain, rec, err := store.Mint(hand, ttl, note)
		if err != nil {
			return env.Fail(err)
		}
		journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "token", Result: "info", Detail: fmt.Sprintf("mint %s for %s ttl=%s", rec.ID, hand, ttl)})
		if env.JSON {
			return cli.Emit(env, map[string]any{"token": plain, "id": rec.ID, "hand": hand, "ceiling": h.Ceiling, "expires": rec.Expires})
		}
		fmt.Fprintf(env.W(), "%s\n# id %s · hand %s · ceiling %s · shown once, never stored in clear\n", plain, rec.ID, hand, h.Ceiling)
		return cli.ExitOK
	}
	return env.Fail(fmt.Errorf("unknown token subcommand %q", args[0]))
}
