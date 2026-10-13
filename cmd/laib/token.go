package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/token"
)

func tokenPath(env *cliEnv) string { return filepath.Join(env.paths.Home, "tokens.json") }

// cmdToken: mint <hand> [--ttl 8h] [--note x] | list | revoke <id>. Minting
// is a destroy-tier act in spirit (it hands out a capability), so only a
// human may do it; the TTL defaults to the hand's ttl in policy.yml.
func cmdToken(_ context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return fail(errors.New("usage: laib token mint <hand> [--ttl 8h] [--note text] | list | revoke <id>"))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	store, err := token.Load(tokenPath(env))
	if err != nil {
		return fail(err)
	}
	switch args[0] {
	case "list":
		if env.json {
			return emit(env, store.List())
		}
		for _, r := range store.List() {
			exp := "never"
			if !r.Expires.IsZero() {
				exp = r.Expires.Local().Format("02.01. 15:04")
			}
			fmt.Fprintf(env.w(), "%-8s %-8s created %s  expires %s  %s\n", r.ID, r.Hand, r.Created.Local().Format("02.01. 15:04"), exp, r.Note)
		}
		return exitOK
	case "revoke":
		if len(args) != 2 {
			return fail(errors.New("usage: laib token revoke <id>"))
		}
		if proposal.Hand(env.actor) != "human" {
			return exitRefusedMsg(env, "only a human revokes tokens")
		}
		if err := store.Revoke(args[1]); err != nil {
			return fail(err)
		}
		journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "token", Result: "info", Detail: "revoke " + args[1]})
		fmt.Fprintln(env.w(), "widerrufen:", args[1])
		return exitOK
	case "mint":
		if len(args) < 2 {
			return fail(errors.New("usage: laib token mint <hand>"))
		}
		if proposal.Hand(env.actor) != "human" {
			return exitRefusedMsg(env, "only a human mints tokens")
		}
		hand := args[1]
		h, ok := st.Policy.Hands[hand]
		if !ok || hand == "human" {
			return fail(fmt.Errorf("unknown hand %q (policy.yml hands, not human)", hand))
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
					return fail(err)
				}
				ttl = d
			case "--note":
				note = args[i+1]
			}
		}
		plain, rec, err := store.Mint(hand, ttl, note)
		if err != nil {
			return fail(err)
		}
		journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "token", Result: "info", Detail: fmt.Sprintf("mint %s for %s ttl=%s", rec.ID, hand, ttl)})
		if env.json {
			return emit(env, map[string]any{"token": plain, "id": rec.ID, "hand": hand, "ceiling": h.Ceiling, "expires": rec.Expires})
		}
		fmt.Fprintf(env.w(), "%s\n# id %s · hand %s · ceiling %s · shown once, never stored in clear\n", plain, rec.ID, hand, h.Ceiling)
		return exitOK
	}
	return fail(fmt.Errorf("unknown token subcommand %q", args[0]))
}

func exitRefusedMsg(env *cliEnv, msg string) int {
	fmt.Fprintln(os.Stderr, "laib:", msg)
	return exitRefused
}
