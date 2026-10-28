package main

import (
	"context"
	"fmt"
	"slices"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/registry"
	"github.com/mdopp/laib/internal/sysd"
)

// cmdUpdates is laib updates; --now (#578) starts the nightly run now
// (laib-images.service: self-update.sh, then `laib updates --run`) without
// waiting, at tier apply and under the night's guards: no backup running,
// the lock free, laib green. The run checks them again itself.
func cmdUpdates(ctx context.Context, env *cliEnv, args []string) int {
	if !slices.Contains(args, "--now") {
		return registry.Updates(ctx, env, args)
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "apply") {
		return cli.Refused(env, env.Actor+" darf den Update-Lauf nicht starten (Stufe apply)")
	}
	if since := registry.RunningSince(ctx); since != "" {
		return cli.Refused(env, "der Update-Lauf läuft schon seit "+since+": sein Ergebnis zeigt laib updates, wenn er fertig ist")
	}
	if a := sysd.Active(ctx, "laib-backup.service"); a == "active" || a == "activating" {
		return cli.Refused(env, "die Sicherung läuft: der Update-Lauf startet danach, oder in der Nacht")
	}
	if holder := apply.LockHolder(env.Paths); holder != "" {
		return cli.Refused(env, "gesperrt durch "+holder+": der Update-Lauf wartet, bis die Sperre frei ist")
	}
	if r, _ := pages.CollectStatus(ctx, env, st); r.State != "bereit" || r.Red > 0 || r.Firewall == "fehlt" {
		return cli.Refused(env, fmt.Sprintf("laib ist nicht grün (%s, %d rot): erst laib diag", r.State, r.Red))
	}
	if out, err := sysd.Run(ctx, "systemctl", "--user", "start", "--no-block", "laib-images.service"); err != nil {
		return env.Fail(fmt.Errorf("laib-images.service startet nicht: %s", cli.Tail(out, 300)))
	}
	msg := "Der Update-Lauf ist gestartet: erst Laib selbst, dann die Images (laib-images.service). Laibs eigenes Ergebnis steht in self-update.json, beides zeigt laib updates, die Images auch das Journal."
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "updates", Result: "info", Detail: "Lauf gestartet (--now)"})
	if env.JSON {
		return cli.Emit(env, map[string]any{"started": true, "unit": "laib-images.service", "message": msg})
	}
	fmt.Fprintln(env.W(), msg)
	return cli.ExitOK
}
