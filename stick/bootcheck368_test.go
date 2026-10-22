package stick

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #368: an OS update on the GPU box must be boring. install-box.sh writes
// two host units: laib-cdi.service regenerates the CDI spec at every boot (no
// one-shot marker), and laib-boot-check.service runs scripts/boot-check.sh,
// which proves a new deployment (GPU visible, the CDI spec names the loaded
// driver, laib answers with no more red than before) or rolls it back once,
// pauses Zincati and tells the owner. The boot check is its own file, a
// release asset checked against SHA256SUMS like the binaries: the one reboot
// is the boot check's, after a rollback; installing Laib never reboots (#367).

// bootBox is a scratch box: etc holds the boot check as the install puts it,
// and bin holds the stand-ins for the host's tools.
type bootBox struct {
	etc, bin, log string
	vardir, root  string   // /var/lib/laib (the boot check's records, shared by every deployment) and / (the deployments' own /etc)
	listen        []string // the box's listening TCP sockets, as `ss -Hltn` prints them (#445)
	nft           string   // laib's rendered nftables table (lan: false ports), "" = none (#481)
	breakAt       int      // the probe path breaks at this port probe of the first round, 0 = never (#481)
	timeout       string   // LAIB_BOOT_CHECK_TIMEOUT, "" = 1 s
	denied        bool     // every probe fails as the Kraftwerk's did: the connect is refused locally (Permission denied), not by the port (#481)
}

const proven, fresh = "aaaa1111aaaa1111", "bbbb2222bbbb2222"

func newBootBox(t *testing.T) *bootBox {
	t.Helper()
	root := t.TempDir()
	b := &bootBox{etc: filepath.Join(root, "etc"), bin: filepath.Join(root, "bin"), log: filepath.Join(root, "shim.log"), vardir: filepath.Join(root, "var", "lib", "laib"), root: root}
	// the rollback deployment's own /etc: ostree keeps one per deployment,
	// on a read-only /sysroot (the mount shim opens it on remount rw)
	backEtc := filepath.Join(root, "ostree", "deploy", "fedora-coreos", "deploy", proven+".0", "etc")
	os.MkdirAll(backEtc, 0o755)
	os.Chmod(backEtc, 0o555)
	t.Cleanup(func() {
		os.Chmod(backEtc, 0o755)
		os.Chmod(filepath.Join(backEtc, "zincati"), 0o755)
		os.Chmod(filepath.Join(backEtc, "zincati", "config.d"), 0o755)
	})
	os.MkdirAll(b.bin, 0o755)
	os.MkdirAll(filepath.Join(b.etc, "laib"), 0o755)
	t.Setenv("SHIM_LOG", b.log)
	src, err := os.ReadFile(filepath.Join("..", "scripts", "boot-check.sh"))
	if err != nil {
		t.Fatalf("the boot check is its own file: %v", err)
	}
	os.WriteFile(filepath.Join(b.etc, "laib", "boot-check.sh"), src, 0o755)
	return b
}

// run boots the box into booted (the rollback deployment is proven) and
// runs the boot check. gpuDriver "" is a box without an NVIDIA card; cdiDriver
// is the driver the CDI spec names; red is what /ready says ("" = laib down).
func (b *bootBox) run(t *testing.T, booted, gpuDriver, cdiDriver, red string) (log, out string) {
	t.Helper()
	return b.runWith(t, booted, gpuDriver, cdiDriver, red, "exit 0")
}

// runWith is run with rpmOstree as the body of the rpm-ostree stand-in.
func (b *bootBox) runWith(t *testing.T, booted, gpuDriver, cdiDriver, red, rpmOstree string) (log, out string) {
	t.Helper()
	os.WriteFile(b.log, nil, 0o644)
	if !strings.Contains(booted, ".") {
		booted += ".0" // the deployment's serial
	}
	status := "* fedora-coreos " + booted + "\n    Version: 44.20261012.3.0\n  fedora-coreos " + proven + ".0 (rollback)\n    Version: 44.20260510.3.1\n"
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
	shim(t, b.bin, "rpm-ostree", rpmOstree)
	shim(t, b.bin, "systemctl", "exit 0")
	shim(t, b.bin, "runuser", "exit 0")
	// Fedora CoreOS mounts /sysroot read-only: another deployment's /etc is
	// writable only after a remount rw, in a private mount namespace as
	// ostree itself does (train 22). Here the fake deploy tree is read-only
	// until the boot check remounts; unshare runs its command.
	shim(t, b.bin, "unshare", `while [ "${1#-}" != "$1" ]; do shift; done; exec "$@"`)
	shim(t, b.bin, "mount", `case "$*" in *remount,rw*/sysroot*) chmod -R u+w "$LAIB_ROOT/ostree";; esac`)
	// #445: the LAN address is 127.0.0.2 here, and the probe namespace is this
	// process's own (the nsenter stand-in below), the rest of ip is a no-op
	ss := filepath.Join(b.etc, "..", "ss.out")
	os.WriteFile(ss, []byte(strings.Join(b.listen, "\n")+"\n"), 0o644)
	shim(t, b.bin, "ss", `cat "`+ss+`"`)
	// #481: the path into the namespace can break while the check runs (a
	// veth or route torn down under it): from the breakAt-th port probe of
	// the first round on, nothing answers through it, until a new namespace
	// is built. A converge reloads laib's table at the same moment.
	ipst := filepath.Join(b.root, "ipstate")
	os.MkdirAll(ipst, 0o755)
	nft := filepath.Join(b.root, "render", "nftables", "laib.nft")
	os.MkdirAll(filepath.Dir(nft), 0o755)
	os.Remove(nft)
	if b.nft != "" {
		os.WriteFile(nft, []byte(b.nft), 0o644)
	}
	shim(t, b.bin, "ip", `d="$IPSTATE"
case "$1 $2" in "netns add") rm -f "$d/down"; exit 0;; esac
case "$*" in
"-n laib-probe -4 route get "*) [ -e "$d/down" ] && exit 2; echo "$6 via 169.254.254.1 dev laibp1 src 169.254.254.2 uid 0";;
*"route get 169.254.254.2"*) [ -e "$d/down" ] && exit 2; echo "169.254.254.2 dev laibp0 src 169.254.254.1 uid 0";;
*"route get"*) echo "1.1.1.1 via 10.0.0.1 dev eth0 src 127.0.0.2 uid 0";;
esac
exit 0`)
	// the probe namespace is this process's own: `nsenter --net=<ns> cmd` runs cmd
	shim(t, b.bin, "nsenter", `d="$IPSTATE"
[ -z "$PROBE_DENIED" ] || { echo "bash: connect: Permission denied" >&2; exit 1; }
[ -e "$d/down" ] && exit 1
n=$(( $(cat "$d/execs" 2>/dev/null || echo 0) + 1 )); echo "$n" >"$d/execs"
if [ "$n" = "${IP_BREAK_AT:-0}" ]; then
  : >"$d/down"
  [ ! -f "$LAIB_NFT" ] || { c="$(cat "$LAIB_NFT")"; rm -f "$LAIB_NFT"; printf '%s\n' "$c" >"$LAIB_NFT.new"; mv "$LAIB_NFT.new" "$LAIB_NFT"; }
  exit 1
fi
shift; exec "$@"`)
	deny := ""
	if b.denied {
		deny = "1"
	}
	to := b.timeout
	if to == "" {
		to = "1"
	}
	cmd := exec.Command("bash", filepath.Join(b.etc, "laib", "boot-check.sh"))
	cmd.Env = append(os.Environ(), "PATH="+b.bin+":"+os.Getenv("PATH"), "LAIB_ETC="+b.etc, "LAIB_VAR="+b.vardir, "LAIB_ROOT="+b.root, "LAIB_BOOT_CHECK_TIMEOUT="+to, "LAIB_BOOT_CHECK_INTERVAL=0.2",
		"LAIB_NFT="+nft, "IPSTATE="+ipst, "IP_BREAK_AT="+fmt.Sprint(b.breakAt), "PROBE_DENIED="+deny)
	o, _ := cmd.CombinedOutput()
	l, _ := os.ReadFile(b.log)
	return string(l), string(o)
}

func (b *bootBox) prove(t *testing.T, id, red string) {
	t.Helper()
	os.MkdirAll(b.vardir, 0o755)
	os.WriteFile(filepath.Join(b.vardir, "boot-proven"), []byte(id+" "+red+"\n"), 0o644)
}

func rolledBack(log string) bool {
	return strings.Contains(log, "rpm-ostree rollback") || strings.Contains(log, "systemctl reboot")
}

// runInstallTo runs install-box.sh --source against a build server served by
// a curl shim: three dummy binaries, laib-boot-check.sh (bootCheck; "" = a
// release from before #368, without the asset) and a SHA256SUMS over the
// real files. The script stops later (no laib to init);
// what is under test is what it wrote under LAIB_ETC before that. env is
// more of the script's environment; rpm-ostree answers RPMOSTREE_JSON, if set.
func runInstallTo(t *testing.T, bootCheck string, env ...string) (etc, out string) {
	t.Helper()
	root := t.TempDir()
	bin, home, fx := filepath.Join(root, "bin"), filepath.Join(root, "home"), filepath.Join(root, "fx")
	etc = filepath.Join(root, "etc")
	for _, d := range []string{bin, home, fx, etc} {
		os.MkdirAll(d, 0o755)
	}
	real, _ := os.ReadFile(filepath.Join("..", "scripts", "boot-check.sh"))
	follow, _ := os.ReadFile(filepath.Join("..", "scripts", "host-image.sh"))
	self, _ := os.ReadFile(filepath.Join("..", "scripts", "self-update.sh"))
	sums := ""
	for name, body := range map[string]string{"laib-linux-amd64": "#!/bin/sh\n", "laib-operator-linux-amd64": "#!/bin/sh\n", "laib-lease-linux-amd64": "#!/bin/sh\n", "laib-boot-check.sh": string(real), "laib-host-image.sh": string(follow), "laib-self-update.sh": string(self)} {
		if (name == "laib-boot-check.sh" || name == "laib-host-image.sh" || name == "laib-self-update.sh") && bootCheck == "" {
			continue // a release from before #368
		}
		sum := sha256.Sum256([]byte(body))
		sums += hex.EncodeToString(sum[:]) + "  " + name + "\n"
		if name == "laib-boot-check.sh" {
			body = bootCheck
		}
		os.WriteFile(filepath.Join(fx, name), []byte(body), 0o644)
	}
	os.WriteFile(filepath.Join(fx, "SHA256SUMS"), []byte(sums), 0o644)
	os.WriteFile(filepath.Join(fx, "VERSION"), []byte("abc1234"), 0o644)
	shim(t, bin, "id", "echo core")
	shim(t, bin, "sudo", installBoxSudo)
	shim(t, bin, "sysctl", "echo 0")
	shim(t, bin, "restic", "echo 'restic 0.19.1'")
	shim(t, bin, "systemctl", `[ "$1 $2" != "--user enable" ] || exit 1`) // the end: laib's own unit, after the host's
	shim(t, bin, "rpm-ostree", `[ -n "${RPMOSTREE_JSON:-}" ] && [ "$1 $2" = "status --json" ] || exit 1; echo "$RPMOSTREE_JSON"`)
	shim(t, bin, "curl", `o=""; prev=""
for a in "$@"; do [ "$prev" = -o ] && o=$a; prev=$a; done
u="${@: -1}"
case "$u" in http://src.test/*) f="$FX/${u#http://src.test/}";; *) exit 22;; esac
[ -f "$f" ] || exit 22
if [ -n "$o" ]; then cp "$f" "$o"; else cat "$f"; fi`)
	cmd := exec.Command("bash", filepath.Join("..", "scripts", "install-box.sh"), "--domain", "vm.test", "--source", "http://src.test")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SHIM_LOG="+filepath.Join(root, "shim.log"), "FX="+fx, "HOME="+home,
		"LAIB_ETC="+etc, "LAIB_QUADLET="+filepath.Join(home, ".config", "containers", "systemd"), "LAIB_RESTIC_BIN="+filepath.Join(root, "restic"), "LAIB_GH_TOKEN=")
	cmd.Env = append(cmd.Env, env...)
	b, _ := cmd.CombinedOutput()
	return etc, string(b)
}

func TestInstallWritesTheCDIRefreshAndTheVerifiedBootCheck(t *testing.T) {
	real, _ := os.ReadFile(filepath.Join("..", "scripts", "boot-check.sh"))
	etc, out := runInstallTo(t, string(real))
	cdi, err := os.ReadFile(filepath.Join(etc, "systemd", "system", "laib-cdi.service"))
	if err != nil {
		t.Fatalf("no laib-cdi.service: %v\n%s", err, out)
	}
	for _, want := range []string{"ExecStart=/usr/bin/nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml", "ConditionPathExists=/usr/bin/nvidia-ctk", "Before=user@1000.service laib-boot-check.service", "WantedBy=multi-user.target"} {
		if !strings.Contains(string(cdi), want+"\n") {
			t.Errorf("laib-cdi.service lacks %q:\n%s", want, cdi)
		}
	}
	if strings.Contains(string(cdi), "-done") || strings.Contains(string(cdi), "ConditionPathExists=!") {
		t.Errorf("laib-cdi.service is a one-shot again: it must run at every boot\n%s", cdi)
	}
	chk, err := os.ReadFile(filepath.Join(etc, "systemd", "system", "laib-boot-check.service"))
	if err != nil {
		t.Fatalf("no laib-boot-check.service: %v\n%s", err, out)
	}
	for _, want := range []string{"ExecStart=/bin/bash /etc/laib/boot-check.sh", "After=laib-cdi.service network-online.target", "WantedBy=multi-user.target"} {
		if !strings.Contains(string(chk), want+"\n") {
			t.Errorf("laib-boot-check.service lacks %q:\n%s", want, chk)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(etc, "laib", "boot-check.sh")); string(got) != string(real) {
		t.Fatalf("/etc/laib/boot-check.sh is not scripts/boot-check.sh as released\n%s", out)
	}
}

// train 18: `install-box.sh --version v0.7.0` (a release from before #368)
// stopped with "has no asset laib-boot-check.sh". Such a release installs its
// binaries, says once that it carries no boot check, and writes no boot-check
// units; laib diag's boot-check probe stays the reminder.
func TestAReleaseWithoutTheBootCheckStillInstalls(t *testing.T) {
	etc, out := runInstallTo(t, "")
	if !strings.Contains(out, "binaries abc1234 in /var/opt/laib") {
		t.Fatalf("a release without laib-boot-check.sh did not install its binaries:\n%s", out)
	}
	if strings.Count(out, "carries no boot check") != 1 {
		t.Errorf("the missing boot check must be said once:\n%s", out)
	}
	if strings.Contains(out, "blank argument") {
		t.Errorf("curl got a blank URL:\n%s", out)
	}
	for _, f := range []string{"systemd/system/laib-boot-check.service", "systemd/system/laib-cdi.service", "laib/boot-check.sh"} {
		if _, err := os.Stat(filepath.Join(etc, f)); err == nil {
			t.Errorf("%s written without a verified boot check", f)
		}
	}
}

func TestABootCheckWithTheWrongChecksumIsRefused(t *testing.T) {
	etc, out := runInstallTo(t, "#!/bin/bash\nsystemctl reboot\n")
	if !strings.Contains(out, "checksum mismatch for laib-boot-check.sh") {
		t.Fatalf("an unverified boot check was not refused by name:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(etc, "laib", "boot-check.sh")); err == nil {
		t.Fatal("an unverified boot check was installed")
	}
}

// The boot check's one reboot follows a successful rollback and nothing else:
// a failed rollback ends without one, and no other path reboots.
func TestTheBootCheckRebootsOnlyAfterARollback(t *testing.T) {
	src, _ := os.ReadFile(filepath.Join("..", "scripts", "boot-check.sh"))
	if n := strings.Count(string(src), "systemctl reboot"); n != 1 {
		t.Fatalf("boot-check.sh has %d reboots, want exactly one (after the rollback)", n)
	}
	lines := strings.Split(strings.TrimRight(string(src), "\n"), "\n")
	last, before := lines[len(lines)-1], lines[len(lines)-2]
	if last != "sync; systemctl reboot" || !strings.HasPrefix(before, "rpm-ostree rollback || {") || !strings.Contains(before, "exit 1; }") {
		t.Fatalf("the reboot must be the last line, right after `rpm-ostree rollback || { …; exit 1; }`:\n%s\n%s", before, last)
	}
	b := newBootBox(t)
	b.prove(t, proven, "0")
	log, out := b.runWith(t, fresh, "", "", "", "exit 1")
	if !strings.Contains(log, "rpm-ostree rollback") || strings.Contains(log, "systemctl reboot") {
		t.Fatalf("a failed rollback must not reboot:\n%s\n%s", log, out)
	}
}

func TestTheFirstBootCheckIsTheBaselineAndNeverRollsBack(t *testing.T) {
	b := newBootBox(t)
	log, out := b.run(t, fresh, "610.43.02", "595.71.05", "")
	if rolledBack(log) {
		t.Fatalf("no proven deployment yet, but the check rolled back:\n%s\n%s", log, out)
	}
	p, _ := os.ReadFile(filepath.Join(b.vardir, "boot-proven"))
	if !strings.HasPrefix(string(p), fresh+".0 ") {
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
	// /etc is per deployment: the pause goes into the /etc of the deployment
	// the box rolls back to, or it is gone after the reboot (train 22)
	z, err := os.ReadFile(filepath.Join(b.root, "ostree", "deploy", "fedora-coreos", "deploy", proven+".0", "etc", "zincati", "config.d", "95-laib-boot-check.toml"))
	if err != nil || !strings.Contains(string(z), "[updates]\nenabled = false\n") {
		t.Fatalf("Zincati must be paused after a rollback, or it stages the same update again: %v %q", err, z)
	}
	if r, _ := os.ReadFile(filepath.Join(b.vardir, "boot-rolled-back")); strings.TrimSpace(string(r)) != fresh+".0" {
		t.Fatalf("the rollback record must be in /var/lib/laib, which every deployment shares: %q", r)
	}
	if p, _ := os.ReadFile(filepath.Join(b.vardir, "boot-proven")); !strings.HasPrefix(string(p), proven+" ") {
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
	if p, _ := os.ReadFile(filepath.Join(b.vardir, "boot-proven")); string(p) != fresh+".0 1 0\n" {
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

// A kernel-argument change is a new deployment of the same commit (serial
// .1 beside .0), and it can break the box as well: it is checked and rolled
// back like a new commit. A boot-proven from before the serial (the bare
// commit) still names its .0 deployment.
func TestANewDeploymentOfTheSameCommitIsChecked(t *testing.T) {
	b := newBootBox(t)
	b.prove(t, proven+".0", "0")
	if log, out := b.run(t, proven+".1", "", "", ""); !strings.Contains(log, "rpm-ostree rollback") {
		t.Fatalf("a kernel-argument deployment with laib down was not rolled back:\n%s\n%s", log, out)
	}
	b = newBootBox(t)
	b.prove(t, proven, "0") // the old format: the bare commit
	if log, out := b.run(t, proven+".0", "", "", ""); rolledBack(log) || !strings.Contains(out, "is the proven one") {
		t.Fatalf("an old boot-proven must still name its deployment:\n%s\n%s", log, out)
	}
}

// train 22: a boot-proven in /etc/laib from before is read once; the records
// live in /var/lib/laib from then on.
func TestAnOldBootProvenInEtcIsStillRead(t *testing.T) {
	b := newBootBox(t)
	os.WriteFile(filepath.Join(b.etc, "laib", "boot-proven"), []byte(proven+".0 0 0\n"), 0o644)
	if log, out := b.run(t, proven+".0", "", "", ""); rolledBack(log) || !strings.Contains(out, "is the proven one") {
		t.Fatalf("the proven deployment from /etc/laib/boot-proven was not recognised:\n%s\n%s", log, out)
	}
}
