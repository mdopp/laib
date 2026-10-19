package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/token"
)

// cmdSession [<name>]: a token for one session of the hand policy.yml names
// in the caller's `session:` (#380, ADR 0006 amendment). The Werkstatt holds
// the narrow `werkstatt` hand and asks for a `claude` token per Claude
// session: the TTL is claude's, the mint is journaled, `laib token list`
// shows it with the session's name, and an expired one ends that session
// only. Expired session tokens leave tokens.json here; the journal keeps them.
func cmdSession(_ context.Context, env *cliEnv, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	caller := proposal.Hand(env.Actor)
	hand := st.Policy.Hands[caller].Session
	if hand == "" {
		return cli.Refused(env, caller+" has no session: in policy.yml")
	}
	ttl, err := time.ParseDuration(st.Policy.Hands[hand].TTL)
	if err != nil || ttl <= 0 {
		return env.Fail(fmt.Errorf("policy.yml: hand %q has no ttl, and a session token must expire", hand))
	}
	name := strings.Join(args, " ")
	if len(name) > 80 {
		name = name[:80]
	}
	store, err := token.Load(cli.TokenPath(env))
	if err != nil {
		return env.Fail(err)
	}
	now := time.Now()
	store.Records = slices.DeleteFunc(store.Records, func(r token.Record) bool {
		return r.Expired(now) && strings.HasPrefix(r.Note, "session ")
	})
	plain, rec, err := store.Mint(hand, ttl, "session "+caller+": "+name)
	if err != nil {
		return env.Fail(err)
	}
	journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "session", Result: "info", Detail: fmt.Sprintf("mint %s for %s ttl=%s (%s)", rec.ID, hand, ttl, name)})
	return cli.Emit(env, map[string]any{"token": plain, "id": rec.ID, "hand": hand, "expires": rec.Expires})
}
