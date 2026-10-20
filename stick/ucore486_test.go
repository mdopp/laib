package stick

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #486: on a box booted from a container image that is not Fedora CoreOS's
// own (uCore), Zincati asks Fedora's update graph, which does not know the
// image ("not found in the update graph"): it never finds an update, and
// says so at every poll. laib-host-image is the one update path there: the
// image pinned in state/host/image, staged by rpm-ostree, the reboot through
// laib's lock. So install-box.sh masks Zincati and rpm-ostree's own timer
// on such a box (a plain FCOS box keeps Zincati), and pins the booted image
// when the state has no pin. The pause stays one mechanism: Zincati's
// config.d drop-ins, read by laib-host-image as Zincati reads them.

const ucoreTag = "ostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal:stable-nvidia"

func needJQ(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not here; Fedora CoreOS ships it and the scripts read rpm-ostree's JSON with it")
	}
}

// booted is rpm-ostree status --json with one booted deployment: its
// container reference ("" for a plain ostree commit) and its digest.
func booted(ref, digest string) string {
	d := `{"booted":true,"staged":false,"checksum":"c1","serial":0`
	if ref != "" {
		d += `,"container-image-reference":"` + ref + `"`
	}
	if digest != "" {
		d += `,"container-image-reference-digest":"` + digest + `"`
	}
	return `{"deployments":[` + d + `}]}`
}

// boxConfigOnOS runs --box-config as on a box (sudo, systemctl and
// rpm-ostree shimmed): status is rpm-ostree's JSON, enabled what systemctl
// is-enabled says of every unit.
func boxConfigOnOS(t *testing.T, status, enabled string) (log, out string, err error) {
	t.Helper()
	needJQ(t)
	root := t.TempDir()
	bin, home := filepath.Join(root, "bin"), filepath.Join(root, "home")
	os.MkdirAll(bin, 0o755)
	os.MkdirAll(home, 0o755)
	logf := filepath.Join(root, "shim.log")
	shim(t, bin, "sudo", installBoxSudo)
	shim(t, bin, "systemctl", `case "$1 $2" in "is-enabled --quiet"*) exit 1;; is-enabled*) echo `+enabled+`;; is-active*) exit 3;; esac; exit 0`)
	shim(t, bin, "rpm-ostree", `[ "$1 $2" = "status --json" ] && echo '`+status+`'; exit 0`)
	cmd := exec.Command("bash", filepath.Join("..", "scripts", "install-box.sh"), "--box-config")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SHIM_LOG="+logf, "HOME="+home, "LAIB_ETC=", "LAIB_QUADLET="+filepath.Join(home, ".config", "containers", "systemd"))
	b, err := cmd.CombinedOutput()
	l, _ := os.ReadFile(logf)
	return string(l), string(b), err
}

func TestBoxConfigMasksZincatiOnAContainerImageBox(t *testing.T) {
	log, out, err := boxConfigOnOS(t, booted(ucoreOld, ""), "enabled")
	if err != nil {
		t.Fatalf("--box-config: %v\n%s", err, out)
	}
	for _, u := range []string{"zincati.service", "rpm-ostreed-automatic.timer"} {
		if !strings.Contains(log, "sudo systemctl mask --now "+u) {
			t.Errorf("%s is left on a uCore box: Zincati's graph does not know the image, the timer ignores the pin and the pauses\nshims:\n%s\noutput:\n%s", u, log, out)
		}
	}
	if !strings.Contains(out, "zincati.service: masked") || !strings.Contains(out, "laib-host-image") {
		t.Errorf("masking Zincati must be said, and what updates the box instead:\n%s", out)
	}
	if strings.Contains(log, "try-restart zincati") {
		t.Errorf("a masked Zincati is not restarted:\n%s", log)
	}
}

func TestBoxConfigKeepsZincatiOnFedoraCoreOS(t *testing.T) {
	for name, st := range map[string]string{
		"FCOS from its OCI image": booted("ostree-image-signed:docker://quay.io/fedora/fedora-coreos:stable", "sha256:c0"),
		"FCOS from an ostree ref": booted("", ""),
	} {
		log, out, err := boxConfigOnOS(t, st, "enabled")
		if err != nil || strings.Contains(log, "mask --now zincati") || strings.Contains(log, "mask --now rpm-ostreed") {
			t.Errorf("%s: Zincati is Fedora CoreOS's update driver and stays (%v)\nshims:\n%s\noutput:\n%s", name, err, log, out)
		}
	}
}

func TestBoxConfigLeavesAMaskedZincatiAlone(t *testing.T) {
	log, out, err := boxConfigOnOS(t, booted(ucoreOld, ""), "masked")
	if err != nil {
		t.Fatalf("a second --box-config on a uCore box fails: %v\n%s", err, out)
	}
	if strings.Contains(log, "mask --now") || strings.Contains(out, "masked") {
		t.Errorf("already masked: nothing to do, nothing to say\nshims:\n%s\noutput:\n%s", log, out)
	}
}

// runFollower runs host-image.sh on rpm-ostree's JSON as it is.
func (h *hostBox) runFollower(t *testing.T, status, lock string) (log, out string) {
	t.Helper()
	needJQ(t)
	os.WriteFile(h.log, nil, 0o644)
	shim(t, h.bin, "rpm-ostree", `[ "$1 $2" = "status --json" ] && echo '`+status+`'; exit 0`)
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

// A box rebased by tag runs the pinned digest already: the follower must not
// rebase it to the same image by digest (a new deployment, a reboot for nothing).
func TestTheBootedDigestIsThePin(t *testing.T) {
	h := newHostBox(t, ucoreOld)
	digest := ucoreOld[strings.LastIndex(ucoreOld, "@")+1:]
	if log, out := h.runFollower(t, booted(ucoreTag, digest), "200"); rebased(log) || rebooted(log) {
		t.Fatalf("booted by tag, its digest is the pin: nothing to do\n%s\n%s", log, out)
	}
	if log, out := h.runFollower(t, booted(ucoreTag, "sha256:0000"), "409"); !strings.Contains(log, "rpm-ostree rebase --bypass-driver "+ucoreOld) {
		t.Fatalf("booted another digest of the tag: the pin is staged\n%s\n%s", log, out)
	}
}

// One pause, read as Zincati reads it: the drop-ins in name order, the last
// one that sets updates.enabled decides. ServiceBay's lock file is a pause.
func TestThePauseIsReadAsZincatiReadsIt(t *testing.T) {
	for name, c := range map[string]struct {
		files map[string]string
		held  bool
	}{
		"ServiceBay's lock":           {map[string]string{"55-servicebay-lock.toml": "[updates]\nenabled = false\n"}, true},
		"a later file lifts it":       {map[string]string{"90-paused-by-hand.toml": "[updates]\nenabled = false\n", "99-resume.toml": "[updates]\nenabled = true\n"}, false},
		"an earlier one does not":     {map[string]string{"10-on.toml": "[updates]\nenabled = true\n", "95-laib-boot-check.toml": "[updates]\nenabled = false\n"}, true},
		"laib's strategy is no pause": {map[string]string{"60-laib-updates.toml": "[updates]\nstrategy = \"fleet_lock\"\n"}, false},
	} {
		h := newHostBox(t, ucoreNew)
		for f, body := range c.files {
			os.WriteFile(filepath.Join(h.etc, "zincati", "config.d", f), []byte(body), 0o644)
		}
		log, out := h.runFollower(t, booted(ucoreOld, ""), "409")
		if rebased(log) == c.held {
			t.Errorf("%s: held %v, want %v\n%s\n%s", name, !rebased(log), c.held, log, out)
		}
	}
}

// installPin runs install-box.sh with a state that has a box.yml (no init),
// rpm-ostree answering status, and an optional pin already there.
func installPin(t *testing.T, status, pin string) (state, out string) {
	t.Helper()
	needJQ(t)
	state = filepath.Join(t.TempDir(), "state")
	os.MkdirAll(filepath.Join(state, "host"), 0o755)
	os.WriteFile(filepath.Join(state, "box.yml"), []byte("domain: vm.test\n"), 0o644)
	if pin != "" {
		os.WriteFile(filepath.Join(state, "host", "image"), []byte(pin), 0o644)
	}
	if b, err := exec.Command("git", "-C", state, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, b)
	}
	real, _ := os.ReadFile(filepath.Join("..", "scripts", "boot-check.sh"))
	_, out = runInstallTo(t, string(real), "LAIB_STATE="+state, "RPMOSTREE_JSON="+status)
	return state, out
}

func TestInstallPinsTheBootedImage(t *testing.T) {
	digest := ucoreOld[strings.LastIndex(ucoreOld, "@")+1:]
	state, out := installPin(t, booted(ucoreTag, digest), "")
	got, err := os.ReadFile(filepath.Join(state, "host", "image"))
	if want := "# tag: stable-nvidia\n" + ucoreOld + "\n"; err != nil || string(got) != want {
		t.Fatalf("host/image: %q (%v), want %q\n%s", got, err, want, out)
	}
	if b, _ := exec.Command("git", "-C", state, "log", "--format=%s", "--", "host/image").Output(); !strings.Contains(string(b), "#486") {
		t.Errorf("the pin is not committed to the state: %q\n%s", b, out)
	}
}

func TestInstallLeavesAPinAndFedoraCoreOSAlone(t *testing.T) {
	old := "# tag: stable\n" + ucoreNew + "\n"
	state, out := installPin(t, booted(ucoreTag, "sha256:a2"), old)
	if got, _ := os.ReadFile(filepath.Join(state, "host", "image")); string(got) != old {
		t.Fatalf("an existing pin was rewritten: %q\n%s", got, out)
	}
	for name, st := range map[string]string{
		"FCOS":                  booted("ostree-image-signed:docker://quay.io/fedora/fedora-coreos:stable", "sha256:c0"),
		"no container":          booted("", ""),
		"booted by digest only": booted(ucoreOld, ""),
	} {
		state, out := installPin(t, st, "")
		if _, err := os.Stat(filepath.Join(state, "host", "image")); err == nil {
			t.Errorf("%s: a pin was written\n%s", name, out)
		}
		if name == "booted by digest only" && !strings.Contains(out, "host/image") {
			t.Errorf("booted by digest, the tag is unknown: the install must say how to pin\n%s", out)
		}
	}
}
