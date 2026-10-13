package stick

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// release is the GitHub API JSON of one release; with binaries it carries the
// asset objects install-box.sh picks apart with sed.
func release(tag string, id int, binaries bool) string {
	n := strconv.Itoa(id)
	assets := ""
	if binaries {
		assets = `{"url": "https://api.github.com/repos/mdopp/laib/releases/assets/` + n + `1", "id": 1, "name": "laib-linux-amd64", "uploader": {"login": "github-actions[bot]"}},
    {"url": "https://api.github.com/repos/mdopp/laib/releases/assets/` + n + `2", "id": 2, "name": "SHA256SUMS", "uploader": {"login": "github-actions[bot]"}}`
	}
	return `{"url": "https://api.github.com/repos/mdopp/laib/releases/` + n + `0",
  "tag_name": "` + tag + `", "draft": false, "prerelease": false,
  "author": {"login": "github-actions[bot]"},
  "assets": [` + assets + `]}`
}

// runInstallBox runs scripts/install-box.sh without --version against a
// GitHub API served by a curl shim. Downloading an asset fails on purpose:
// what is under test is which release the script resolved.
func runInstallBox(t *testing.T, latest string, list ...string) (log, out string) {
	t.Helper()
	root := t.TempDir()
	bin, home, fx := filepath.Join(root, "bin"), filepath.Join(root, "home"), filepath.Join(root, "fx")
	for _, d := range []string{bin, home, fx} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(fx, "latest"), []byte(latest), 0o644)
	os.WriteFile(filepath.Join(fx, "list"), []byte("["+strings.Join(list, ",\n")+"]"), 0o644)
	for _, r := range list {
		tag := strings.SplitN(strings.SplitN(r, `"tag_name": "`, 2)[1], `"`, 2)[0]
		os.WriteFile(filepath.Join(fx, "tag-"+tag), []byte(r), 0o644)
	}
	logf := filepath.Join(root, "shim.log")
	shim(t, bin, "id", "echo core")
	shim(t, bin, "sudo", "exit 0")
	shim(t, bin, "sysctl", "echo 0")
	shim(t, bin, "restic", "exit 0")
	shim(t, bin, "curl", `url="${@: -1}"
case "$url" in
  */releases/latest) cat "$FX/latest";;
  */releases\?*) cat "$FX/list";;
  */releases/tags/*) cat "$FX/tag-${url##*/}" 2>/dev/null || exit 22;;
  *) exit 22;;
esac`)
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "install-box.sh"), "--domain", "trial.example")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SHIM_LOG="+logf, "FX="+fx, "HOME="+home, "LAIB_GH_TOKEN=")
	b, _ := cmd.CombinedOutput()
	l, _ := os.ReadFile(logf)
	return string(l), string(b)
}

// #99: a release exists for the length of a VM run before its binaries do
// (#47 builds them after vm-install is green). In that window "latest" has no
// laib-linux-amd64, and the one-liner from INSTALL.md failed on every box. It
// takes the newest release that has its binaries, and says which it skipped.
func TestLatestSkipsAReleaseWhoseBinariesAreNotBuiltYet(t *testing.T) {
	v040, v035 := release("v0.4.0", 4, false), release("v0.3.5", 3, true)
	log, out := runInstallBox(t, v040, v040, v035)
	if !strings.Contains(log, "/releases/assets/31\n") {
		t.Fatalf("the binaries of v0.3.5 were never fetched\nshim log:\n%s\noutput:\n%s", log, out)
	}
	if strings.Contains(out, "has no asset") {
		t.Errorf("failed on the release without binaries:\n%s", out)
	}
	if !strings.Contains(out, "v0.4.0") || !strings.Contains(out, "v0.3.5") {
		t.Errorf("the skip is silent; it must name the skipped release and the one installed:\n%s", out)
	}
}

func TestLatestWithBinariesIsTakenAsIs(t *testing.T) {
	v040, v035 := release("v0.4.0", 4, true), release("v0.3.5", 3, true)
	log, out := runInstallBox(t, v040, v040, v035)
	if !strings.Contains(log, "/releases/assets/41\n") {
		t.Fatalf("the binaries of v0.4.0 were never fetched\nshim log:\n%s\noutput:\n%s", log, out)
	}
	if strings.Contains(log, "releases?") {
		t.Errorf("listed all releases although latest has its binaries:\n%s", log)
	}
}
