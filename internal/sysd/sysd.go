// Package sysd is the thin seam to `systemctl --user`. Everything that touches
// the box goes through Run, so tests can swap it.
package sysd

import (
	"context"
	"os/exec"
	"strings"
)

// Run executes a command and returns its combined output. Swapped in tests.
var Run = func(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(Stdin(ctx))
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

type stdinKey struct{}

// WithStdin makes Run hand in to the command on stdin: how a secret reaches
// a process, since argv shows in ps to every user on the box (#224).
func WithStdin(ctx context.Context, in string) context.Context {
	return context.WithValue(ctx, stdinKey{}, in)
}

// Stdin is what WithStdin put on ctx, for a Run that tests swap in.
func Stdin(ctx context.Context) string { in, _ := ctx.Value(stdinKey{}).(string); return in }

// Active returns `systemctl --user is-active <unit>`: active, inactive, failed, activating, …
func Active(ctx context.Context, unit string) string {
	out, _ := Run(ctx, "systemctl", "--user", "is-active", unit)
	if out == "" {
		return "unknown"
	}
	return strings.Split(out, "\n")[0]
}

// DaemonReload reloads the user manager so Quadlet regenerates units.
func DaemonReload(ctx context.Context) error {
	_, err := Run(ctx, "systemctl", "--user", "daemon-reload")
	return err
}

// Restart restarts one unit.
func Restart(ctx context.Context, unit string) error {
	_, err := Run(ctx, "systemctl", "--user", "restart", unit)
	return err
}

// Stop stops and disables one unit (used when a service leaves the state).
func Stop(ctx context.Context, unit string) error {
	_, err := Run(ctx, "systemctl", "--user", "stop", unit)
	return err
}

// ResetFailed clears a unit's failed state: a stopped unit whose file goes
// would stay listed as failed, with no file to start it again (#551).
func ResetFailed(ctx context.Context, unit string) {
	Run(ctx, "systemctl", "--user", "reset-failed", unit)
}
