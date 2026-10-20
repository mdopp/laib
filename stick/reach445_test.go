package stick

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #445: after the rebase to uCore, firewalld (zone FedoraServer: ssh only)
// blocked every port from the LAN for 2h20, and laib-boot-check proved the
// deployment: it asked /ready over loopback, which firewalld always lets
// through. The check now probes every TCP port the box listens on outside
// loopback, from a network namespace over a veth pair, so the packets come
// in on an interface as a LAN client's do. A port that listens but does not
// answer is blocked; more blocked ports than the proven boot had is "not
// proven". A service that is down does not listen and is no blocked port.

// listening opens a TCP listener on all addresses: a port that answers.
func listening(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

// blockedPort is a port the box lists as listening, but that does not answer.
func blockedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return p
}

func ssLine(addr string, port int) string {
	return fmt.Sprintf("LISTEN 0      4096         %s:%d      0.0.0.0:*", addr, port)
}

func TestABlockedLANPortIsNotProvenAndRollsBack(t *testing.T) {
	b := newBootBox(t)
	b.prove(t, proven, "0")
	open, shut := listening(t), blockedPort(t)
	b.listen = []string{ssLine("0.0.0.0", open), ssLine("[::]", shut)}
	log, out := b.run(t, fresh, "", "", "0")
	if !strings.Contains(log, "rpm-ostree rollback") {
		t.Fatalf("port %d listens but does not answer from the LAN, and the deployment was not rolled back:\n%s\n%s", shut, log, out)
	}
	if !strings.Contains(out, fmt.Sprint(shut)) || strings.Contains(out, fmt.Sprint(open)+",") {
		t.Errorf("the reason must name the blocked port %d, not the open %d:\n%s", shut, open, out)
	}
	if !strings.Contains(log, "netns exec") {
		t.Errorf("the probe must come from the namespace (an interface, not loopback):\n%s", log)
	}
}

func TestAReachableBoxIsProvenWithItsBlockedCount(t *testing.T) {
	b := newBootBox(t)
	b.prove(t, proven, "0")
	b.listen = []string{ssLine("0.0.0.0", listening(t)), ssLine("127.0.0.2", listening(t)), ssLine("127.0.0.1", blockedPort(t))}
	log, out := b.run(t, fresh, "", "", "0")
	if rolledBack(log) {
		t.Fatalf("every LAN port answers (the loopback-only one is not a LAN port), but it rolled back:\n%s\n%s", log, out)
	}
	if p, _ := os.ReadFile(filepath.Join(b.vardir, "boot-proven")); string(p) != fresh+".0 0 0\n" {
		t.Fatalf("boot-proven = %q, want the deployment, its red and its blocked count", p)
	}
}

func TestNoMoreBlockedThanTheProvenBootIsProven(t *testing.T) {
	b := newBootBox(t)
	b.prove(t, proven, "0 1")
	b.listen = []string{ssLine("0.0.0.0", listening(t)), ssLine("0.0.0.0", blockedPort(t))}
	if log, out := b.run(t, fresh, "", "", "0"); rolledBack(log) {
		t.Fatalf("one port was blocked at the proven boot too: no rollback for it\n%s\n%s", log, out)
	}
}

func TestTheBaselineRecordsItsBlockedPorts(t *testing.T) {
	b := newBootBox(t)
	shut := blockedPort(t)
	b.listen = []string{ssLine("0.0.0.0", shut)}
	log, out := b.run(t, fresh, "", "", "0")
	if rolledBack(log) {
		t.Fatalf("a baseline never rolls back:\n%s\n%s", log, out)
	}
	if p, _ := os.ReadFile(filepath.Join(b.vardir, "boot-proven")); string(p) != fmt.Sprintf("%s.0 0 1 %d\n", fresh, shut) {
		t.Fatalf("boot-proven = %q, want the blocked port counted and named in the baseline (#481)", p)
	}
}

// #445: install-box.sh (every install, and --box-config) leaves one firewall
// mechanism on the box, Laib's nftables table: an enabled or running
// firewalld is masked and stopped. A box without it is left alone.
func runBoxConfigOnHost(t *testing.T, firewalld string) (log, out string) {
	t.Helper()
	root := t.TempDir()
	bin, home := filepath.Join(root, "bin"), filepath.Join(root, "home")
	os.MkdirAll(bin, 0o755)
	os.MkdirAll(home, 0o755)
	logf := filepath.Join(root, "shim.log")
	shim(t, bin, "sudo", installBoxSudo)
	shim(t, bin, "systemctl", firewalld)
	cmd := exec.Command("bash", filepath.Join("..", "scripts", "install-box.sh"), "--box-config")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SHIM_LOG="+logf, "HOME="+home, "LAIB_ETC=", "LAIB_QUADLET="+filepath.Join(home, ".config", "containers", "systemd"))
	b, _ := cmd.CombinedOutput()
	l, _ := os.ReadFile(logf)
	return string(l), string(b)
}

func TestBoxConfigMasksAnActiveFirewalld(t *testing.T) {
	log, out := runBoxConfigOnHost(t, `case "$*" in "is-active --quiet firewalld.service"|"is-enabled --quiet firewalld.service") exit 0;; esac; exit 0`)
	if !strings.Contains(log, "sudo systemctl mask --now firewalld.service") {
		t.Fatalf("an active firewalld was left running: it blocks everything but SSH (#445)\nshims:\n%s\noutput:\n%s", log, out)
	}
	if !strings.Contains(out, "firewalld") {
		t.Errorf("masking firewalld must be said:\n%s", out)
	}
}

func TestBoxConfigLeavesABoxWithoutFirewalldAlone(t *testing.T) {
	log, out := runBoxConfigOnHost(t, `case "$*" in *firewalld*) exit 4;; esac; exit 0`)
	if strings.Contains(log, "mask") {
		t.Fatalf("no firewalld, but install-box.sh masked it:\n%s\n%s", log, out)
	}
}
