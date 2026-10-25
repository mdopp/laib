package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// cmdPause: laib pause <service> [--until 2h|<RFC3339>] [--stop <unit>]…
// [--restart <unit>]… ["reason"] — the service is the caller's for a while
// (#39): converge leaves its units alone, the readiness shows "pausiert von
// … bis …". Default: two hours; a pause never outlives a day without being
// renewed. --stop and --restart act on the service's own units after the
// marker is written, and the marker keeps them: resume starts and restarts
// them again. That is how laib-lease, in its container, takes the card
// (ADR 0027). A renewal by the same hand moves the end and keeps the units,
// touching none.
func cmdPause(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 1 {
		return env.Fail(errors.New("usage: laib pause <service> [--until 2h|<RFC3339>] [--stop <unit>]… [--restart <unit>]… [\"<grund>\"]"))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "apply") {
		return cli.Refused(env, env.Actor+" may not pause a service")
	}
	name := args[0]
	svc, ok := st.Services[name]
	if !ok {
		return env.Fail(fmt.Errorf("unknown service %q", name))
	}
	until := time.Now().Add(2 * time.Hour)
	var words, stop, restart []string
	for i := 1; i < len(args); i++ {
		if (args[i] == "--stop" || args[i] == "--restart") && i+1 < len(args) {
			if !slices.Contains(svc.UnitNames(), args[i+1]) {
				return env.Fail(fmt.Errorf("%s: %s is no unit of %s (%s)", args[i], args[i+1], name, strings.Join(svc.UnitNames(), ", ")))
			}
			if args[i] == "--stop" {
				stop = append(stop, args[i+1])
			} else {
				restart = append(restart, args[i+1])
			}
			i++
			continue
		}
		if args[i] == "--until" && i+1 < len(args) {
			i++
			if d, err := time.ParseDuration(args[i]); err == nil {
				until = time.Now().Add(d)
			} else if ts, err := time.Parse(time.RFC3339, args[i]); err == nil {
				until = ts
			} else {
				return env.Fail(fmt.Errorf("--until %q: a duration (2h) or an RFC3339 time", args[i]))
			}
			continue
		}
		words = append(words, args[i])
	}
	if until.After(time.Now().Add(24 * time.Hour)) {
		return env.Fail(errors.New("a pause lasts at most a day; renew it"))
	}
	pz := apply.Pause{Service: name, By: env.Actor, Until: until.UTC(), Reason: strings.Join(words, " ")}
	if old, ok := apply.Paused(env.Paths)[name]; ok && old.By == env.Actor {
		pz.Stop, pz.Restart = old.Stop, old.Restart // a renewal keeps what the pause holds
	}
	for _, u := range stop {
		if !slices.Contains(pz.Stop, u) {
			pz.Stop = append(pz.Stop, u)
		}
	}
	for _, u := range restart {
		if !slices.Contains(pz.Restart, u) {
			pz.Restart = append(pz.Restart, u)
		}
	}
	if err := apply.WritePause(env.Paths, pz); err != nil {
		return env.Fail(err)
	}
	for _, u := range stop {
		sysd.Stop(ctx, u)
	}
	for _, u := range restart {
		sysd.Restart(ctx, u)
	}
	detail := "pausiert bis " + until.Local().Format("02.01. 15:04")
	if len(stop)+len(restart) > 0 {
		detail += "; angehalten: " + strings.Join(stop, ", ") + "; neu gestartet: " + strings.Join(restart, ", ")
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "pause", Service: name, Reason: pz.Reason, Result: "info", Detail: detail})
	if env.JSON {
		return cli.Emit(env, pz)
	}
	fmt.Fprintf(env.W(), "%s pausiert bis %s: converge lässt die Units in Ruhe (laib resume %s hebt es auf)\n", name, until.Local().Format("02.01. 15:04"), name)
	return cli.ExitOK
}

// cmdResume lifts a pause and brings back what it stopped or restarted.
func cmdResume(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) != 1 {
		return env.Fail(errors.New("usage: laib resume <service>"))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "apply") {
		return cli.Refused(env, env.Actor+" may not resume a service")
	}
	pz, ok := apply.Paused(env.Paths)[args[0]]
	if !ok {
		fmt.Fprintf(env.W(), "%s ist nicht pausiert\n", args[0])
		return cli.ExitOK
	}
	apply.Resume(env.Paths, args[0])
	for _, u := range pz.Stop {
		sysd.Run(ctx, "systemctl", "--user", "start", u)
	}
	for _, u := range pz.Restart {
		sysd.Restart(ctx, u)
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "resume", Service: args[0], Result: "info", Detail: "Pause aufgehoben; der nächste converge fasst den Dienst wieder an"})
	fmt.Fprintf(env.W(), "%s: Pause aufgehoben\n", args[0])
	return cli.ExitOK
}
