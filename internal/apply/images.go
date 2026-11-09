package apply

import (
	"cmp"
	"context"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// The nightly image run (#528): laib-images.timer runs `laib updates --run`
// in the update window after the backup. It pulls nothing (ADR 0034): a
// newer digest is a proposal — the Operator's nightly versionen files one
// card for all of them, a human taps it, and apply restarts, verifies and
// on red goes back (#609). What the run still does is remove the images
// nothing needs (#1071). podman auto-update's timer stays masked.

// ImageRun removes the images nothing needs, while no backup runs; keep
// names images laib runs beside the units (hooks, rclone).
func ImageRun(ctx context.Context, env *cli.Env, st *state.State, keep ...string) int {
	if a := sysd.Active(ctx, "laib-backup.service"); a == "active" || a == "activating" {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "updates", Result: "info", Detail: "übersprungen: die Sicherung läuft"})
		return cli.ExitOK
	}
	unlock, code := TakeLock(env, st, "updates")
	if code != cli.ExitOK {
		return code
	}
	defer unlock()
	PruneImages(ctx, env, st, append(keep, cmp.Or(st.Box.Laib.HooksImage, DefaultHooksImage))...)
	return cli.ExitOK
}
