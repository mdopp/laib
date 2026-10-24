package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// binDir is where install-box.sh puts the binaries (and keeps *.prev).
func binDir() string {
	if d := os.Getenv("LAIB_BIN_DIR"); d != "" {
		return d
	}
	return "/var/opt/laib"
}

var selfUnits = []string{"laib.service", "laib-lease.service"}

// cmdSelf: rollback — the previous binaries back (install-box.sh keeps them
// as *.prev), the units that run them restarted, and the previous laib
// converges the box to its own rendering: laib-operator and laib-notify are
// containers pinned by the laib that renders them (ADR 0027), so they go
// back with it like any service. Humans only.
func cmdSelf(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 1 || args[0] != "rollback" {
		return env.Fail(errors.New("usage: laib self rollback"))
	}
	if proposal.Hand(env.Actor) != "human" {
		return cli.Refused(env, "self rollback is the human's verb")
	}
	dir := binDir()
	bins := []string{"laib", "laib-lease"}
	for _, b := range []string{"laib-disk", "laib-haushalt"} { // since #443, #598; a box from before has none to roll back
		if _, err := os.Stat(filepath.Join(dir, b+".prev")); err == nil {
			bins = append(bins, b)
		}
	}
	for _, b := range bins {
		if _, err := os.Stat(filepath.Join(dir, b+".prev")); err != nil {
			return env.Fail(fmt.Errorf("no previous %s in %s: nothing to roll back to (install-box.sh keeps *.prev after an update)", b, dir))
		}
	}
	for _, b := range bins {
		cur, prev := filepath.Join(dir, b), filepath.Join(dir, b+".prev")
		if _, err := sysd.Run(ctx, "sudo", "mv", "-f", cur, cur+".rolled"); err != nil {
			return env.Fail(err)
		}
		if _, err := sysd.Run(ctx, "sudo", "mv", prev, cur); err != nil {
			return env.Fail(err)
		}
		sysd.Run(ctx, "sudo", "mv", "-f", cur+".rolled", prev)
	}
	for _, u := range selfUnits {
		sysd.Run(ctx, "systemctl", "--user", "try-restart", u)
	}
	// the previous laib renders its own files: the Operator's and notify's images among them
	out, err := sysd.Run(ctx, "env", "LAIB_HOME="+env.Paths.Home, "LAIB_ACTOR=laib", filepath.Join(dir, "laib"), "converge", "--force")
	if st, lerr := cli.LoadState(env); lerr == nil {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "self", Result: "info", Detail: "rollback: previous binaries restored, units restarted, converged"})
	}
	if err != nil {
		fmt.Fprintln(env.W(), "zurückgerollt: vorherige Binaries aktiv, laib und lease neu gestartet; der Abgleich ist rot: "+cli.Tail(out, 300)+" (laib plan zeigt warum)")
		return cli.ExitRed
	}
	fmt.Fprintln(env.W(), "zurückgerollt: vorherige Binaries aktiv, laib und lease neu gestartet, Operator und notify auf dem vorigen Stand")
	return cli.ExitOK
}
