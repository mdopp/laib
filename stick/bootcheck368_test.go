package stick

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #368: an OS update on the GPU box must be boring. install-box.sh
// --box-config writes two host units on every box: laib-cdi.service
// regenerates the CDI spec at every boot (no one-shot marker), and
// laib-boot-check.service proves a new deployment (GPU visible, the CDI spec
// names the loaded driver, laib answers with no more red than before) or
// rolls it back once, pauses Zincati and tells the owner.

// bootBox is a scratch box: install-box.sh --box-config has written its
// host config under etc, and bin holds the stand-ins for the host's tools.
type bootBox struct{ etc, bin, log string }

const proven, fresh = "aaaa1111aaaa1111", "bbbb2222bbbb2222"

func newBootBox(t *testing.T) *bootBox {
	t.Helper()
	root := t.TempDir()
	b := &bootBox{etc: filepath.Join(root, "etc"), bin: filepath.Join(root, "bin"), log: filepath.Join(root, "shim.log")}
	os.MkdirAll(b.bin, 0o755)
	t.Setenv("SHIM_LOG", b.log)
	cmd := exec.Command("bash", "../scripts/install-box.sh", "--box-config")
	cmd.Env = append(os.Environ(), "LAIB_ETC="+b.etc, "LAIB_QUADLET="+filepath.Join(root, "home", ".config", "containers", "systemd"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install-box.sh --box-config: %v\n%s", err, out)
	}
	return b
}

// run boots the box into booted (the rollback deployment is proven) and
// runs the boot check. gpuDriver "" is a box without an NVIDIA card; cdiDriver
// is the driver the CDI spec names; red is what /ready says ("" = laib down).
func (b *bootBox) run(t *testing.T, booted, gpuDriver, cdiDriver, red string) (log, out string) {
	t.Helper()
	os.WriteFile(b.log, nil, 0o644)
	status := "* fedora-coreos " + booted + ".0\n    Version: 44.20261012.3.0\n  fedora-coreos " + proven + ".0 (rollback)\n    Version: 44.20260510.3.1\n"
	shim(t, b.bin, "ostree", "cat <<'EOF'\n"+status+"EOF")
	if gpuDriver != "" {
		shim(t, b.bin, "lspci", `echo "01:00.0 0300: 10de:2204 (rev a1)"`)
		shim(t, b.bin, "nvidia-smi", "echo "+gpuDriver)
	} else {
		shim(t, b.bin, "lspci", "exit 0")
		shim(t, b.bin, "nvidia-smi", "exit 9")
	}
	if cdiDriver != "" {
		os.MkdirAll(filepath.Join(b.etc, "cdi"), 0o755)
		os.WriteFile(filepath.Join(b.etc, "cdi", "nvidia.yaml"), []byte("cdiVersion: 0.5.0\nkind: nvidia.com/gpu\n  - hostPath: /usr/lib64/libcuda.so."+cdiDriver+"\n"), 0o644)
	}
	if red != "" {
		shim(t, b.bin, "curl", `echo '{"state":"bereit","services":12,"green":11,"yellow":0,"red":`+red+`,"version":"v0.7.0"}'`)
	} else {
		shim(t, b.bin, "curl", "exit 7")
	}
	shim(t, b.bin, "rpm-ostree", "exit 0")
	shim(t, b.bin, "systemctl", "exit 0")
	shim(t, b.bin, "runuser", "exit 0")
	cmd := exec.Command("bash", filepath.Join(b.etc, "laib", "boot-check.sh"))
	cmd.Env = append(os.Environ(), "PATH="+b.bin+":"+os.Getenv("PATH"), "LAIB_ETC="+b.etc, "LAIB_BOOT_CHECK_TIMEOUT=1", "LAIB_BOOT_CHECK_INTERVAL=0.2")
	o, _ := cmd.CombinedOutput()
	l, _ := os.ReadFile(b.log)
	return string(l), string(o)
}

func (b *bootBox) prove(t *testing.T, id, red string) {
	t.Helper()
	os.WriteFile(filepath.Join(b.etc, "laib", "boot-proven"), []byte(id+" "+red+"\n"), 0o644)
}

func rolledBack(log string) bool {
	return strings.Contains(log, "rpm-ostree rollback") || strings.Contains(log, "systemctl reboot")
}

func TestBoxConfigWritesTheCDIRefreshAndTheBootCheck(t *testing.T) {
	b := newBootBox(t)
	cdi, err := os.ReadFile(filepath.Join(b.etc, "systemd", "system", "laib-cdi.service"))
	if err != nil {
		t.Fatalf("no laib-cdi.service: %v", err)
	}
	for _, want := range []string{"ExecStart=/usr/bin/nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml", "ConditionPathExists=/usr/bin/nvidia-ctk", "Before=user@1000.service laib-boot-check.service", "WantedBy=multi-user.target"} {
		if !strings.Contains(string(cdi), want+"\n") {
			t.Errorf("laib-cdi.service lacks %q:\n%s", want, cdi)
		}
	}
	if strings.Contains(string(cdi), "-done") || strings.Contains(string(cdi), "ConditionPathExists=!") {
		t.Errorf("laib-cdi.service is a one-shot again: it must run at every boot\n%s", cdi)
	}
	chk, err := os.ReadFile(filepath.Join(b.etc, "systemd", "system", "laib-boot-check.service"))
	if err != nil {
		t.Fatalf("no laib-boot-check.service: %v", err)
	}
	for _, want := range []string{"ExecStart=/bin/bash /etc/laib/boot-check.sh", "After=laib-cdi.service network-online.target", "WantedBy=multi-user.target"} {
		if !strings.Contains(string(chk), want+"\n") {
			t.Errorf("laib-boot-check.service lacks %q:\n%s", want, chk)
		}
	}
	if _, err := os.Stat(filepath.Join(b.etc, "laib", "boot-check.sh")); err != nil {
		t.Fatal(err)
	}
}

func TestTheFirstBootCheckIsTheBaselineAndNeverRollsBack(t *testing.T) {
	b := newBootBox(t)
	log, out := b.run(t, fresh, "610.43.02", "595.71.05", "")
	if rolledBack(log) {
		t.Fatalf("no proven deployment yet, but the check rolled back:\n%s\n%s", log, out)
	}
	p, _ := os.ReadFile(filepath.Join(b.etc, "laib", "boot-proven"))
	if !strings.HasPrefix(string(p), fresh+" ") {
		t.Fatalf("the baseline is not recorded: %q\n%s", p, out)
	}
	if !strings.Contains(log, "note --kind incident") {
		t.Errorf("a red baseline must still reach the owner:\n%s", log)
	}
}

func TestANewDeploymentWithAStaleCDISpecRollsBackOnceAndPausesZincati(t *testing.T) {
	b := newBootBox(t)
	b.prove(t, proven, "0")
	log, out := b.run(t, fresh, "610.43.02", "595.71.05", "0")
	if !strings.Contains(log, "rpm-ostree rollback") || !strings.Contains(log, "systemctl reboot") {
		t.Fatalf("a CDI spec naming 595.71 under driver 610.43 must roll back and reboot:\n%s\n%s", log, out)
	}
	if !strings.Contains(log, "note --kind incident") || !strings.Contains(out, "610.43.02") {
		t.Errorf("the rollback must say why and reach the owner:\n%s\n%s", log, out)
	}
	z, err := os.ReadFile(filepath.Join(b.etc, "zincati", "config.d", "95-laib-boot-check.toml"))
	if err != nil || !strings.Contains(string(z), "[updates]\nenabled = false\n") {
		t.Fatalf("Zincati must be paused after a rollback, or it stages the same update again: %v %q", err, z)
	}
	if p, _ := os.ReadFile(filepath.Join(b.etc, "laib", "boot-proven")); !strings.HasPrefix(string(p), proven+" ") {
		t.Errorf("a failed deployment became proven: %q", p)
	}
	// booted into the same deployment again (the owner deployed it once more): never a second rollback
	log, out = b.run(t, fresh, "610.43.02", "595.71.05", "0")
	if rolledBack(log) {
		t.Fatalf("rolled back the same deployment twice: a loop\n%s\n%s", log, out)
	}
}

func TestANewHealthyDeploymentBecomesProven(t *testing.T) {
	b := newBootBox(t)
	b.prove(t, proven, "1")
	log, out := b.run(t, fresh, "610.43.02", "610.43.02", "1")
	if rolledBack(log) {
		t.Fatalf("a healthy deployment was rolled back:\n%s\n%s", log, out)
	}
	if p, _ := os.ReadFile(filepath.Join(b.etc, "laib", "boot-proven")); string(p) != fresh+" 1\n" {
		t.Fatalf("boot-proven = %q, want the new deployment with its red count", p)
	}
}

func TestMoreRedThanBeforeOrLaibDownRollsBack(t *testing.T) {
	for name, red := range map[string]string{"more red": "2", "laib down": ""} {
		t.Run(name, func(t *testing.T) {
			b := newBootBox(t)
			b.prove(t, proven, "1")
			if log, out := b.run(t, fresh, "", "", red); !strings.Contains(log, "rpm-ostree rollback") {
				t.Fatalf("not rolled back:\n%s\n%s", log, out)
			}
		})
	}
}

func TestAProvenDeploymentIsNeverRolledBack(t *testing.T) {
	b := newBootBox(t)
	b.prove(t, fresh, "0")
	if log, out := b.run(t, fresh, "610.43.02", "595.71.05", ""); rolledBack(log) {
		t.Fatalf("the proven deployment was rolled back:\n%s\n%s", log, out)
	}
}

func TestNoGPUNoGPUCheck(t *testing.T) {
	b := newBootBox(t)
	b.prove(t, proven, "0")
	if log, out := b.run(t, fresh, "", "", "0"); rolledBack(log) {
		t.Fatalf("a box without a GPU was rolled back for its GPU:\n%s\n%s", log, out)
	}
}
