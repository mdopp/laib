package apply

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/sysd"
)

// The firewall is the one piece of state that is not a file on disk: nft
// loads the rendered ruleset into the kernel, and a reboot empties the
// kernel while the file stays the same, so plan and converge saw no drift
// and every lan: false port was open after the first reboot (#37).
// Readiness asks the kernel; serve and converge load what is missing.

func FirewallFile(paths boxfs.Paths) string {
	return filepath.Join(paths.Render, "nftables", "laib.nft")
}

// FirewallState: "" (nothing rendered), "geladen", "fehlt".
func FirewallState(ctx context.Context, paths boxfs.Paths) string {
	if _, err := os.Stat(FirewallFile(paths)); err != nil {
		return ""
	}
	if _, err := sysd.Run(ctx, "sudo", "nft", "list", "table", "inet", "laib"); err != nil {
		return "fehlt"
	}
	return "geladen"
}

// EnsureFirewall loads the rendered ruleset when the kernel has no table.
// Returns whether it loaded something.
func EnsureFirewall(ctx context.Context, paths boxfs.Paths) (bool, error) {
	if FirewallState(ctx, paths) != "fehlt" {
		return false, nil
	}
	if out, err := sysd.Run(ctx, "sudo", "nft", "-f", FirewallFile(paths)); err != nil {
		return false, fmt.Errorf("nft -f: %s", strings.TrimSpace(out))
	}
	return true, nil
}
