package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
)

// cmdPause: laib pause <service> [--until 2h|<RFC3339>] ["reason"] — the
// service is the caller's for a while (#39): converge leaves its units
// alone, the readiness shows "pausiert von … bis …". Default: two hours;
// a pause never outlives a day without being renewed.
func cmdPause(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 1 {
		return fail(errors.New("usage: laib pause <service> [--until 2h|<RFC3339>] [\"<grund>\"]"))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "apply") {
		return exitRefusedMsg(env, env.actor+" may not pause a service")
	}
	name := args[0]
	if _, ok := st.Services[name]; !ok {
		return fail(fmt.Errorf("unknown service %q", name))
	}
	until := time.Now().Add(2 * time.Hour)
	var words []string
	for i := 1; i < len(args); i++ {
		if args[i] == "--until" && i+1 < len(args) {
			i++
			if d, err := time.ParseDuration(args[i]); err == nil {
				until = time.Now().Add(d)
			} else if ts, err := time.Parse(time.RFC3339, args[i]); err == nil {
				until = ts
			} else {
				return fail(fmt.Errorf("--until %q: a duration (2h) or an RFC3339 time", args[i]))
			}
			continue
		}
		words = append(words, args[i])
	}
	if until.After(time.Now().Add(24 * time.Hour)) {
		return fail(errors.New("a pause lasts at most a day; renew it"))
	}
	pz := apply.Pause{Service: name, By: env.actor, Until: until.UTC(), Reason: strings.Join(words, " ")}
	if err := apply.WritePause(env.paths, pz); err != nil {
		return fail(err)
	}
	record(env, st, journal.Entry{Actor: env.actor, Verb: "pause", Service: name, Reason: pz.Reason, Result: "info", Detail: "pausiert bis " + until.Local().Format("02.01. 15:04")})
	if env.json {
		return emit(env, pz)
	}
	fmt.Fprintf(env.w(), "%s pausiert bis %s: converge lässt die Units in Ruhe (laib resume %s hebt es auf)\n", name, until.Local().Format("02.01. 15:04"), name)
	return exitOK
}

// cmdResume lifts a pause.
func cmdResume(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) != 1 {
		return fail(errors.New("usage: laib resume <service>"))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "apply") {
		return exitRefusedMsg(env, env.actor+" may not resume a service")
	}
	if _, ok := apply.Paused(env.paths)[args[0]]; !ok {
		fmt.Fprintf(env.w(), "%s ist nicht pausiert\n", args[0])
		return exitOK
	}
	apply.Resume(env.paths, args[0])
	record(env, st, journal.Entry{Actor: env.actor, Verb: "resume", Service: args[0], Result: "info", Detail: "Pause aufgehoben; der nächste converge fasst den Dienst wieder an"})
	fmt.Fprintf(env.w(), "%s: Pause aufgehoben\n", args[0])
	return exitOK
}
