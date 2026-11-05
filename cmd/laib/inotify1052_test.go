package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #1052: every inotify instance of uid core counts against one limit, the
// host's fs.inotify.max_user_instances (checked in the init user namespace,
// across all of core's containers). At the default 128 it ran out:
// Paperless' consumer crash-looped on "Too many open files", conmon could
// not watch. install-box.sh's box config raises it: on a fresh box at its
// first boot, on a box in use with its next self-update.
func TestTheBoxConfigRaisesTheInotifyInstances(t *testing.T) {
	etc := t.TempDir()
	cmd := exec.Command("bash", "../../scripts/install-box.sh", "--box-config")
	cmd.Env = append(os.Environ(), "LAIB_ETC="+etc, "LAIB_QUADLET="+filepath.Join(t.TempDir(), "quadlet"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install-box.sh --box-config: %v\n%s", err, out)
	}
	conf, err := os.ReadFile(filepath.Join(etc, "sysctl.d", "90-laib-inotify.conf"))
	if err != nil || !strings.Contains(string(conf), "\nfs.inotify.max_user_instances=1024\n") {
		t.Fatalf("no sysctl drop-in for inotify (%v):\n%s", err, conf)
	}
	// one source (#281): the first boot runs install-box.sh before any container starts
	if bu, _ := os.ReadFile("../../usb/fedora-coreos.bu"); strings.Contains(string(bu), "inotify") {
		t.Error("usb/fedora-coreos.bu sets inotify too: two sources drift (#281)")
	}
}
