// Package boxfs knows where Laib's files live on the box. Everything is
// overridable by environment so tests and the CI VM can point elsewhere.
package boxfs

import (
	"os"
	"path/filepath"
)

// Paths are the on-box locations (CLAUDE.md "Where things are").
type Paths struct {
	Home    string // /mnt/data/laib
	State   string // Home/state — the Git repo
	Journal string // Home/journal
	Render  string // Home/render — proxy/sso/dns/nftables outputs
	Quadlet string // ~/.config/containers/systemd
	Key     string // Home/age.key
	Secrets string // State/secrets.age
	Lock    string // Home/apply.lock
}

// Default resolves the paths from LAIB_HOME, LAIB_STATE and LAIB_QUADLET.
func Default() Paths {
	home := env("LAIB_HOME", "/mnt/data/laib")
	state := env("LAIB_STATE", filepath.Join(home, "state"))
	userHome, _ := os.UserHomeDir()
	return Paths{
		Home:    home,
		State:   state,
		Journal: filepath.Join(home, "journal"),
		Render:  filepath.Join(home, "render"),
		Quadlet: env("LAIB_QUADLET", filepath.Join(userHome, ".config", "containers", "systemd")),
		Key:     filepath.Join(home, "age.key"),
		Secrets: filepath.Join(state, "secrets.age"),
		Lock:    filepath.Join(home, "apply.lock"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
