package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// The firewall is the one piece of state that is not a file on disk: nft
// loads the rendered ruleset into the kernel, and a reboot empties the
// kernel while the file stays the same, so plan and converge saw no drift
// and every lan: false port was open after the first reboot (#37).
// Readiness asks the kernel; serve and converge load what is missing.

func firewallFile(env *cliEnv) string {
	return filepath.Join(env.paths.Render, "nftables", "laib.nft")
}

// firewallState: "" (nothing rendered), "geladen", "fehlt".
func firewallState(ctx context.Context, env *cliEnv) string {
	if _, err := os.Stat(firewallFile(env)); err != nil {
		return ""
	}
	if _, err := sysd.Run(ctx, "sudo", "nft", "list", "table", "inet", "laib"); err != nil {
		return "fehlt"
	}
	return "geladen"
}

// ensureFirewall loads the rendered ruleset when the kernel has no table.
// Returns whether it loaded something.
func ensureFirewall(ctx context.Context, env *cliEnv) (bool, error) {
	if firewallState(ctx, env) != "fehlt" {
		return false, nil
	}
	if out, err := sysd.Run(ctx, "sudo", "nft", "-f", firewallFile(env)); err != nil {
		return false, fmt.Errorf("nft -f: %s", strings.TrimSpace(out))
	}
	return true, nil
}

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
		return fail(errors.New("usage: laib self rollback"))
	}
	if proposal.Hand(env.actor) != "human" {
		return exitRefusedMsg(env, "self rollback is the human's verb")
	}
	dir := binDir()
	bins := []string{"laib", "laib-operator", "laib-lease"}
	for _, b := range bins {
		if _, err := os.Stat(filepath.Join(dir, b+".prev")); err != nil {
			return fail(fmt.Errorf("no previous %s in %s: nothing to roll back to (install-box.sh keeps *.prev after an update)", b, dir))
		}
	}
	for _, b := range bins {
		cur, prev := filepath.Join(dir, b), filepath.Join(dir, b+".prev")
		if _, err := sysd.Run(ctx, "sudo", "mv", "-f", cur, cur+".rolled"); err != nil {
			return fail(err)
		}
		if _, err := sysd.Run(ctx, "sudo", "mv", prev, cur); err != nil {
			return fail(err)
		}
		sysd.Run(ctx, "sudo", "mv", "-f", cur+".rolled", prev)
	}
	for _, u := range selfUnits {
		sysd.Run(ctx, "systemctl", "--user", "try-restart", u)
	}
	if st, err := loadState(env); err == nil {
		record(env, st, journal.Entry{Actor: env.actor, Verb: "self", Result: "info", Detail: "rollback: previous binaries restored, units restarted"})
	}
	fmt.Println("zurückgerollt: vorherige Binaries aktiv, laib/operator/lease neu gestartet")
	return exitOK
}
