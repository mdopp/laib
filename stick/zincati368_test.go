package stick

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #368: on a box with Zincati, install-box.sh (and --box-config) makes laib
// the reboot lock for OS updates: strategy fleet_lock against laib on
// loopback. Laib grants the reboot only in its window, green and proven.
// The file sorts after ServiceBay's 55-servicebay-lock.toml, so its strategy
// wins; the pause files (90-, 95-) still switch updates off.
func boxConfigTo(t *testing.T, zincati bool) string {
	t.Helper()
	root := t.TempDir()
	etc := filepath.Join(root, "etc")
	if zincati {
		os.MkdirAll(filepath.Join(etc, "zincati", "config.d"), 0o755)
	}
	cmd := exec.Command("bash", filepath.Join("..", "scripts", "install-box.sh"), "--box-config")
	cmd.Env = append(os.Environ(), "LAIB_ETC="+etc, "LAIB_QUADLET="+filepath.Join(root, "home", ".config", "containers", "systemd"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("--box-config: %v\n%s", err, out)
	}
	return etc
}

func TestBoxConfigMakesLaibZincatisRebootLock(t *testing.T) {
	etc := boxConfigTo(t, true)
	b, err := os.ReadFile(filepath.Join(etc, "zincati", "config.d", "60-laib-updates.toml"))
	if err != nil {
		t.Fatalf("no Zincati drop-in: %v", err)
	}
	for _, want := range []string{"[updates]\nstrategy = \"fleet_lock\"\n", "[updates.fleet_lock]\nbase_url = \"http://127.0.0.1:5900/\"\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the drop-in lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "enabled") {
		t.Errorf("the drop-in must not switch updates on or off: the pause files decide that\n%s", b)
	}
}

func TestBoxConfigLeavesABoxWithoutZincatiAlone(t *testing.T) {
	etc := boxConfigTo(t, false)
	if _, err := os.Stat(filepath.Join(etc, "zincati")); err == nil {
		t.Fatal("a Zincati config written on a box without Zincati")
	}
}
