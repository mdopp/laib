package stick

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #368: the host OS image is pinned in the state (host/image: the rebase
// reference by digest, and the tag it follows). laib-host-image (root,
// every 15 minutes) stages a pinned image that is not booted, with
// rpm-ostree, and reboots into it only when laib's reboot lock (#467:
// window, backup, green, proven) says yes. A pause file (Zincati's
// updates.enabled = false: the hand-made one, the boot check's after a
// rollback) holds it. The boot check proves the new deployment after.

const (
	ucoreOld = "ostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal@sha256:a287759bae0552b75708cb3746977e98976dcfba392bca46ad36920724aeb06f"
	ucoreNew = "ostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal@sha256:b111111111111111111111111111111111111111111111111111111111111111"
)

type hostBox struct{ state, etc, bin, log string }

func newHostBox(t *testing.T, pin string) *hostBox {
	t.Helper()
	root := t.TempDir()
	h := &hostBox{state: filepath.Join(root, "state"), etc: filepath.Join(root, "etc"), bin: filepath.Join(root, "bin"), log: filepath.Join(root, "shim.log")}
	for _, d := range []string{h.bin, filepath.Join(h.state, "host"), filepath.Join(h.etc, "zincati", "config.d")} {
		os.MkdirAll(d, 0o755)
	}
	if pin != "" {
		os.WriteFile(filepath.Join(h.state, "host", "image"), []byte("# tag: stable-nvidia\n"+pin+"\n"), 0o644)
	}
	return h
}

// run runs host-image.sh once: booted and staged are the deployments'
// container references, lock is the HTTP code laib's reboot lock answers.
func (h *hostBox) run(t *testing.T, booted, staged, lock string) (log, out string) {
	t.Helper()
	os.WriteFile(h.log, nil, 0o644)
	deps := `{"booted":true,"staged":false,"checksum":"c1","container-image-reference":"` + booted + `"}`
	if staged != "" {
		deps = `{"booted":false,"staged":true,"checksum":"c2","container-image-reference":"` + staged + `"},` + deps
	}
	shim(t, h.bin, "rpm-ostree", `[ "$1 $2" = "status --json" ] && echo '{"deployments":[`+deps+`]}'; exit 0`)
	shim(t, h.bin, "curl", `echo `+lock)
	shim(t, h.bin, "systemctl", "exit 0")
	shim(t, h.bin, "runuser", "exit 0")
	shim(t, h.bin, "sync", "exit 0")
	cmd := exec.Command("bash", filepath.Join("..", "scripts", "host-image.sh"))
	cmd.Env = append(os.Environ(), "PATH="+h.bin+":"+os.Getenv("PATH"), "SHIM_LOG="+h.log, "LAIB_STATE="+h.state, "LAIB_ETC="+h.etc)
	o, _ := cmd.CombinedOutput()
	l, _ := os.ReadFile(h.log)
	return string(l), string(o)
}

func rebased(log string) bool  { return strings.Contains(log, "rpm-ostree rebase") }
func rebooted(log string) bool { return strings.Contains(log, "systemctl reboot") }

func TestNoPinOrTheBootedPinChangesNothing(t *testing.T) {
	for name, h := range map[string]*hostBox{"no pin": newHostBox(t, ""), "booted": newHostBox(t, ucoreOld)} {
		if log, out := h.run(t, ucoreOld, "", "200"); rebased(log) || rebooted(log) || strings.Contains(log, "curl") {
			t.Fatalf("%s: the host was touched:\n%s\n%s", name, log, out)
		}
	}
}

func TestANewPinIsStagedAndWaitsForTheLock(t *testing.T) {
	h := newHostBox(t, ucoreNew)
	log, out := h.run(t, ucoreOld, "", "409")
	if !strings.Contains(log, "rpm-ostree rebase --bypass-driver "+ucoreNew) {
		t.Fatalf("the pinned image was not staged (rebase by digest, Zincati is the driver):\n%s\n%s", log, out)
	}
	if !strings.Contains(log, "/v1/pre-reboot") || rebooted(log) {
		t.Fatalf("the reboot must ask laib's lock, and wait on a no:\n%s\n%s", log, out)
	}
}

func TestAStagedPinRebootsOnlyWithTheLock(t *testing.T) {
	h := newHostBox(t, ucoreNew)
	if log, out := h.run(t, ucoreOld, ucoreNew, "409"); rebased(log) || rebooted(log) {
		t.Fatalf("already staged: no second rebase, and no reboot without the lock:\n%s\n%s", log, out)
	}
	if log, out := h.run(t, ucoreOld, ucoreNew, "200"); !rebooted(log) {
		t.Fatalf("the lock said yes, the staged image was not booted:\n%s\n%s", log, out)
	}
}

func TestAPauseHoldsTheHostImage(t *testing.T) {
	h := newHostBox(t, ucoreNew)
	os.WriteFile(filepath.Join(h.etc, "zincati", "config.d", "95-laib-boot-check.toml"), []byte("[updates]\nenabled = false\n"), 0o644)
	log, out := h.run(t, ucoreOld, "", "200")
	if rebased(log) || rebooted(log) {
		t.Fatalf("a pause (after a rollback, or by hand) must hold the image:\n%s\n%s", log, out)
	}
	if !strings.Contains(out, "95-laib-boot-check.toml") {
		t.Errorf("the hold must name the pause file:\n%s", out)
	}
}

func TestInstallWritesTheHostImageFollower(t *testing.T) {
	real, _ := os.ReadFile(filepath.Join("..", "scripts", "boot-check.sh"))
	etc, out := runInstallTo(t, string(real))
	follow, _ := os.ReadFile(filepath.Join("..", "scripts", "host-image.sh"))
	if got, _ := os.ReadFile(filepath.Join(etc, "laib", "host-image.sh")); string(got) != string(follow) {
		t.Fatalf("/etc/laib/host-image.sh is not the released scripts/host-image.sh\n%s", out)
	}
	tm, err := os.ReadFile(filepath.Join(etc, "systemd", "system", "laib-host-image.timer"))
	if err != nil || !strings.Contains(string(tm), "OnUnitActiveSec=15min") {
		t.Fatalf("no 15-minute timer: %v\n%s", err, tm)
	}
	svc, _ := os.ReadFile(filepath.Join(etc, "systemd", "system", "laib-host-image.service"))
	for _, want := range []string{"ExecStart=/bin/bash /etc/laib/host-image.sh", "ConditionPathExists=/mnt/data/laib/state/host/image"} {
		if !strings.Contains(string(svc), want+"\n") {
			t.Errorf("laib-host-image.service lacks %q:\n%s", want, svc)
		}
	}
}
