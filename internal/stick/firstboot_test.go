package stick

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runFirstBoot runs the real laib-firstboot.sh from the Ignition, with its
// /etc/laib and /tmp moved into a temp dir and sudo, curl, getent, shred and
// systemctl shimmed. installer is the body of the install-box.sh it fetches.
// It returns the temp root, the shim log and the script's output.
func runFirstBoot(t *testing.T, installer string) (root, log, out string) {
	t.Helper()
	ign, err := DestIgnition(box())
	if err != nil {
		t.Fatal(err)
	}
	root = t.TempDir()
	etc, bin, fx := filepath.Join(root, "etc", "laib"), filepath.Join(root, "bin"), filepath.Join(root, "fx")
	for _, d := range []string{etc, bin, fx} {
		os.MkdirAll(d, 0o755)
	}
	script := fileFromIgnition(t, ign, "/usr/local/bin/laib-firstboot.sh")
	script = strings.ReplaceAll(script, "/etc/laib", etc)
	script = strings.ReplaceAll(script, "/tmp/install-box.sh", filepath.Join(root, "install-box.sh"))
	sp := filepath.Join(root, "laib-firstboot.sh")
	os.WriteFile(sp, []byte(script), 0o755)
	os.WriteFile(filepath.Join(fx, "install-box.sh"), []byte(installer), 0o644)
	os.WriteFile(filepath.Join(etc, "firstboot.env"), []byte("LAIB_GH_TOKEN=t\n"), 0o600)
	logf := filepath.Join(root, "shim.log")
	shim(t, bin, "sudo", `exec "$@"`)
	shim(t, bin, "getent", "exit 0")
	shim(t, bin, "systemctl", "exit 0")
	shim(t, bin, "shred", `for a in "$@"; do [ -f "$a" ] && rm -f "$a"; done; exit 0`)
	shim(t, bin, "curl", `while [ $# -gt 0 ]; do [ "$1" = -o ] && { cp "$FX/install-box.sh" "$2"; exit 0; }; shift; done; exit 0`)
	cmd := exec.Command("bash", sp)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SHIM_LOG="+logf, "FX="+fx,
		"LAIB_DOMAIN=vm.test", "LAIB_SOURCE=http://src.test")
	b, _ := cmd.CombinedOutput()
	l, _ := os.ReadFile(logf)
	return root, string(l), string(b)
}

func envKept(root string) bool {
	_, err := os.Stat(filepath.Join(root, "etc", "laib", "firstboot.env"))
	return err == nil
}

// #202: the installer reboots (`systemctl reboot; exit 75`) and the shutdown
// stops laib-firstboot with SIGTERM to its whole cgroup. When that signal
// reaches the outer script before the child's exit 75 does, bash runs the
// EXIT trap with KEEP=0 and shreds firstboot.env: every later boot fails with
// "Failed to load environment files" (vm-install 38716153749, 38571276082,
// 38549133839). The env file must outlive any first boot that did not finish.
func TestFirstBootKeepsItsEnvWhenTheRebootsSignalWinsTheRace(t *testing.T) {
	root, _, out := runFirstBoot(t, "kill -TERM $PPID; sleep 1; exit 75\n")
	if !envKept(root) {
		t.Fatalf("firstboot.env was shredded by the shutdown's SIGTERM: the next boot cannot resume\n%s", out)
	}
}

// #202: an installer that fails (GitHub down, a network blip, any error) is
// retried by the next boot: the marker is missing, so the unit runs again.
// It can only do that with its env file.
func TestFirstBootKeepsItsEnvWhenTheInstallerFails(t *testing.T) {
	root, _, out := runFirstBoot(t, "exit 1\n")
	if !envKept(root) {
		t.Fatalf("firstboot.env was shredded after a failed install: the next boot cannot retry\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "etc", "laib", "installed")); err == nil {
		t.Fatal("a failed install wrote the marker")
	}
}

// Success: the marker is written and the env file (the token) is shredded.
func TestFirstBootShredsItsEnvOnceInstalled(t *testing.T) {
	root, _, out := runFirstBoot(t, "exit 0\n")
	if _, err := os.Stat(filepath.Join(root, "etc", "laib", "installed")); err != nil {
		t.Fatalf("no marker after a successful install\n%s", out)
	}
	if envKept(root) {
		t.Fatalf("firstboot.env (the token) is still on disk after the install finished\n%s", out)
	}
}

// #208: the first boot waited up to 300 s for api.github.com before an
// install from --source, which needs nothing from GitHub; when GitHub did not
// answer the runner, vm-install's resumed boot blew its 240 s budget (run
// 38740629814). It waits for what it installs from.
func TestFirstBootFromASourceWaitsForTheSourceNotForGitHub(t *testing.T) {
	_, log, out := runFirstBoot(t, "exit 0\n")
	for _, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(l, "curl ") && strings.Contains(l, "api.github.com") {
			t.Fatalf("an install from --source asked GitHub: %s\nshims:\n%s\noutput:\n%s", l, log, out)
		}
	}
	if !strings.Contains(log, "http://src.test/VERSION") {
		t.Fatalf("the first boot never checked that its source answers\nshims:\n%s", log)
	}
}
