package registry

import (
	"testing"

	"github.com/mdopp/laib/internal/render"
)

// A line that runs as a whole under podman unshare writes in podman's user namespace in every
// segment, `&&` and `||` included (the Bote's back-online marker, 2026-10-30); the same write
// without it is still refused.
func TestAWholeLineUnderPodmanUnshareMayWrite(t *testing.T) {
	unit := func(cmd string) render.Output {
		return render.Output{"quadlet/bote/bote-hermes.container": "[Container]\nVolume=/d/bote/hermes:/opt/data:z\n[Service]\nExecStartPre=" + cmd + "\n"}
	}
	ok := "-/usr/bin/podman unshare sh -c 'test -f /d/bote/hermes/.clean_shutdown && echo {} > /d/bote/hermes/.restart_pending.json || true'"
	if errs := lintHostWrites("bote", unit(ok)); len(errs) != 0 {
		t.Fatalf("a whole line under podman unshare was refused: %v", errs)
	}
	bad := "-/bin/sh -c 'test -f /d/bote/hermes/.clean_shutdown && echo {} > /d/bote/hermes/.restart_pending.json || true'"
	if errs := lintHostWrites("bote", unit(bad)); len(errs) != 1 {
		t.Fatalf("a host write into the container's folder passed: %v", errs)
	}
}
