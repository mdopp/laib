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
// not watch. install-box.sh's box config raises it, and so does the next
// self-update on a box in use; the stick's Ignition carries the same file,
// so a fresh box has it from its first boot.
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
	bu, _ := os.ReadFile("../../usb/fedora-coreos.bu")
	_, block, ok := strings.Cut(string(bu), "- path: /etc/sysctl.d/90-laib-inotify.conf\n")
	for _, line := range strings.Split(strings.TrimSpace(string(conf)), "\n") {
		if !ok || !strings.Contains(block, "          "+line+"\n") {
			t.Errorf("usb/fedora-coreos.bu's 90-laib-inotify.conf lacks %q: the stick and install-box.sh drift", line)
		}
	}
}
