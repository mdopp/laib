package registry

import (
	"strings"
	"testing"
)

// #486: laib updates says it in words: a newer digest, or the pin is current,
// and that the OS updates are paused (by which file).
func TestTheHostLineSaysNewerAndPaused(t *testing.T) {
	u := ImageUpdate{Service: "host", Key: "stable-nvidia", Image: "ostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal", Current: "sha256:a287"}
	if l := hostLine(u); !strings.Contains(l, "ucore-minimal@sha256:a287") || !strings.Contains(l, "aktuell") {
		t.Errorf("the pinned reference, by digest, and current: %q", l)
	}
	u.Patch, u.Paused = "sha256:b111", "/etc/zincati/config.d/90-paused-by-hand.toml"
	if l := hostLine(u); !strings.Contains(l, "neuer Digest") || !strings.Contains(l, "sha256:b111") || !strings.Contains(l, "pausiert") || !strings.Contains(l, "90-paused-by-hand.toml") {
		t.Errorf("a newer digest, and the pause by its file: %q", l)
	}
}
