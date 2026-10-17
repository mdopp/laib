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

var selfUnits = []string{"laib.service", "laib-operator.service", "laib-lease.service"}

// cmdSelf: rollback — the previous binaries back (install-box.sh keeps them
// as *.prev) and the units that run them restarted. Humans only.
func cmdSelf(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 1 || args[0] != "rollback" {
		return env.Fail(errors.New("usage: laib self rollback"))
	}
	if proposal.Hand(env.Actor) != "human" {
		return cli.Refused(env, "self rollback is the human's verb")
	}
	dir := binDir()
	bins := []string{"laib", "laib-operator", "laib-lease"}
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
	if st, err := cli.LoadState(env); err == nil {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "self", Result: "info", Detail: "rollback: previous binaries restored, units restarted"})
	}
	fmt.Fprintln(env.W(), "zurückgerollt: vorherige Binaries aktiv, laib/operator/lease neu gestartet")
	return cli.ExitOK
}
