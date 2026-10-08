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
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

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
