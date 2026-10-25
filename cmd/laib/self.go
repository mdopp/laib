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

// cmdSelf: rollback — the previous laib back (install-box.sh keeps it as
// laib.prev; laib-disk too, since #443), laib restarted, and the previous
// laib converges the box to its own rendering: laib-operator, laib-notify,
// laib-haushalt and laib-lease are containers pinned by the laib that
// renders them (ADR 0027), so they go back with it like any service. A
// binary of theirs an older release left stays for a rollback to it.
// Humans only.
func cmdSelf(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 1 || args[0] != "rollback" {
		return env.Fail(errors.New("usage: laib self rollback"))
	}
	if proposal.Hand(env.Actor) != "human" {
		return cli.Refused(env, "self rollback is the human's verb")
	}
	dir := binDir()
	bins := []string{"laib"}
	if _, err := os.Stat(filepath.Join(dir, "laib-disk.prev")); err == nil { // since #443; a box from before has none to roll back
		bins = append(bins, "laib-disk")
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
	sysd.Run(ctx, "systemctl", "--user", "try-restart", "laib.service")
	// the previous laib renders its own files: its programs' images among them
	out, err := sysd.Run(ctx, "env", "LAIB_HOME="+env.Paths.Home, "LAIB_ACTOR=laib", filepath.Join(dir, "laib"), "converge", "--force")
	if st, lerr := cli.LoadState(env); lerr == nil {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "self", Result: "info", Detail: "rollback: previous binaries restored, units restarted, converged"})
	}
	if err != nil {
		fmt.Fprintln(env.W(), "zurückgerollt: das vorige laib läuft; der Abgleich ist rot: "+cli.Tail(out, 300)+" (laib plan zeigt warum)")
		return cli.ExitRed
	}
	fmt.Fprintln(env.W(), "zurückgerollt: das vorige laib läuft, Operator, notify, haushalt und lease auf seinem Stand")
	return cli.ExitOK
}
