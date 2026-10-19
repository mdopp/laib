package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/sysd"
)

// #281: journald's limits and podman's log driver are the box's own config,
// written by install-box.sh on every box (the stick's first boot, the
// migration's step 1). A box without them is red in diag, and the script's
// --box-config clears it: the files diag looks for are the ones it writes.
func TestDiagIsRedWithoutTheBoxConfigAndInstallBoxClearsIt(t *testing.T) {
	c := newCbox(t)
	etc := t.TempDir()
	t.Setenv("LAIB_ETC", etc)
	st, _ := cli.LoadState(c.env)
	box := func() (n int, detail string) {
		for _, f := range diag.Probes(context.Background(), c.env, st, "") {
			if f.Probe == "box" && f.Severity == "rot" {
				n, detail = n+1, detail+f.Detail+"\n"
			}
		}
		return n, detail
	}
	if n, _ := box(); n != 2 {
		t.Fatalf("a box without the journald drop-in and the containers.conf drop-in must be red twice, got %d", n)
	}
	cmd := exec.Command("bash", "../../scripts/install-box.sh", "--box-config")
	cmd.Env = append(os.Environ(), "LAIB_ETC="+etc, "LAIB_QUADLET="+c.env.Paths.Quadlet)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install-box.sh --box-config: %v\n%s", err, out)
	}
	if n, d := box(); n != 0 {
		t.Fatalf("still red after install-box.sh --box-config:\n%s", d)
	}
	j, _ := os.ReadFile(filepath.Join(etc, "systemd", "journald.conf.d", "50-laib.conf"))
	p, _ := os.ReadFile(filepath.Join(filepath.Dir(c.env.Paths.Quadlet), "containers.conf.d", "50-laib.conf"))
	for _, want := range []string{"Storage=persistent", "SystemMaxUse=4G", "MaxRetentionSec=4week", "MaxFileSec=1week"} {
		if !strings.Contains(string(j), want+"\n") {
			t.Errorf("journald drop-in lacks %s:\n%s", want, j)
		}
	}
	if !strings.Contains(string(p), "[containers]\nlog_driver = \"journald\"\n") {
		t.Errorf("containers.conf drop-in does not pin the log driver:\n%s", p)
	}
	// one source: the stick's Ignition no longer carries its own copy
	bu, _ := os.ReadFile("../../usb/fedora-coreos.bu")
	if strings.Contains(string(bu), "journald.conf.d") || strings.Contains(string(bu), "log_driver") {
		t.Error("usb/fedora-coreos.bu writes the box config too: two sources drift (#281)")
	}
}

// #368: without laib-boot-check.service a broken OS update stays broken; diag
// says so under its own probe (the install writes it, not --box-config).
func TestDiagIsRedWithoutTheBootCheck(t *testing.T) {
	c := newCbox(t)
	etc := t.TempDir()
	t.Setenv("LAIB_ETC", etc)
	st, _ := cli.LoadState(c.env)
	red := func() bool {
		for _, f := range diag.Probes(context.Background(), c.env, st, "") {
			if f.Probe == "boot-check" && f.Severity == "rot" {
				return true
			}
		}
		return false
	}
	if !red() {
		t.Fatal("no boot check on the box, and diag is not red about it")
	}
	os.MkdirAll(filepath.Join(etc, "systemd", "system"), 0o755)
	os.WriteFile(filepath.Join(etc, "systemd", "system", "laib-boot-check.service"), []byte("[Unit]\n"), 0o644)
	if red() {
		t.Fatal("still red with laib-boot-check.service in place")
	}
}

// #445: uCore enables firewalld (zone FedoraServer: ssh only). Laib filters
// with its own nftables table; a running firewalld blocks everything else
// from the LAN, and diag says so in red.
func TestDiagIsRedWithAnActiveFirewalld(t *testing.T) {
	c := newCbox(t)
	t.Setenv("LAIB_ETC", t.TempDir())
	st, _ := cli.LoadState(c.env)
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	state := "active"
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && strings.Contains(strings.Join(args, " "), "firewalld") {
			return state + "\n", nil
		}
		return real(ctx, name, args...)
	}
	red := func() string {
		for _, f := range diag.Probes(context.Background(), c.env, st, "") {
			if f.Probe == "firewall" && f.Severity == "rot" {
				return f.Detail + " → " + f.Fix
			}
		}
		return ""
	}
	if d := red(); !strings.Contains(d, "blockiert alles außer SSH") || !strings.Contains(d, "mask") {
		t.Fatalf("an active firewalld must be red, say what it blocks and how to stop it: %q", d)
	}
	state = "inactive"
	if d := red(); d != "" {
		t.Fatalf("firewalld inactive, still red: %q", d)
	}
}
