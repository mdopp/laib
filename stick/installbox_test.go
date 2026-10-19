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

// installBoxSudo is runInstallBox's sudo: it runs nothing, and as tee it
// reads what it is handed (#304).
const installBoxSudo = `[ "$1" != tee ] || cat >/dev/null`

// #304: install-box.sh runs under pipefail and hands files to `… | sudo
// tee`. A sudo that exits without reading its stdin breaks that pipe: the
// writer dies of SIGPIPE when it is slower than the shim (a loaded
// `go test ./...`), and the script stops at its journald drop-in without a
// word — the release was never fetched. The fake must take what tee is
// handed, as tee does. A writer slower than the shim, on purpose:
func TestTheSudoShimTakesWhatTeeIsHanded(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("SHIM_LOG", filepath.Join(bin, "log"))
	shim(t, bin, "sudo", installBoxSudo)
	cmd := exec.Command("bash", "-c", `set -euo pipefail; { sleep 0.3; printf 'x\n'; } | sudo tee /etc/x >/dev/null`)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("a pipe into the sudo shim fails (%v): the writer gets SIGPIPE\n%s", err, out)
	}
}

// runInstallBox runs scripts/install-box.sh without --version against a
// GitHub API served by a curl shim, with token as LAIB_GH_TOKEN. Downloading
// an asset fails on purpose: what is under test is which release the script
// resolved. The shim logs its argv (what ps shows) and, for a header read
// from a file (-H @file) that carries the token, that file and its mode.
func runInstallBox(t *testing.T, token, latest string, list ...string) (log, out string) {
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
	shim(t, bin, "sudo", installBoxSudo)
	shim(t, bin, "sysctl", "echo 0")
	shim(t, bin, "restic", "exit 0")
	shim(t, bin, "curl", `prev=
for a in "$@"; do
  [ "$prev" = -H ] && [ "${a:0:1}" = @ ] && [ -n "$LAIB_GH_TOKEN" ] && grep -qxF "Authorization: Bearer $LAIB_GH_TOKEN" "${a:1}" &&
    echo "curl: the bearer from a file of mode $(stat -c %a "${a:1}")" >> "$SHIM_LOG"
  prev=$a
done
url="${@: -1}"
case "$url" in
  */releases/latest) cat "$FX/latest";;
  */releases\?*) cat "$FX/list";;
  */releases/tags/*) cat "$FX/tag-${url##*/}" 2>/dev/null || exit 22;;
  *) exit 22;;
esac`)
	cmd := exec.Command("bash", filepath.Join("..", "scripts", "install-box.sh"), "--domain", "trial.example")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SHIM_LOG="+logf, "FX="+fx, "HOME="+home, "LAIB_GH_TOKEN="+token)
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
	log, out := runInstallBox(t, "", v040, v040, v035)
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
	log, out := runInstallBox(t, "", v040, v040, v035)
	if !strings.Contains(log, "/releases/assets/41\n") {
		t.Fatalf("the binaries of v0.4.0 were never fetched\nshim log:\n%s\noutput:\n%s", log, out)
	}
	if strings.Contains(log, "releases?") {
		t.Errorf("listed all releases although latest has its binaries:\n%s", log)
	}
}

// runPrereqs runs install-box.sh --source until its prerequisites are in
// place: a curl shim (body curl) stands for the network, rpm-ostree and
// systemctl only log. What is under test is what the script does to the OS
// for restic, git and diff (#367). LAIB_RESTIC_BIN is where restic goes.
func runPrereqs(t *testing.T, curl string) (log, out string, rc int, resticBin string) {
	t.Helper()
	root := t.TempDir()
	bin, home, fx, etc := filepath.Join(root, "bin"), filepath.Join(root, "home"), filepath.Join(root, "fx"), filepath.Join(root, "etc")
	for _, d := range []string{bin, home, fx, filepath.Join(etc, "laib"), filepath.Join(root, "usrlocal")} {
		os.MkdirAll(d, 0o755)
	}
	resticBin = filepath.Join(root, "usrlocal", "restic")
	logf := filepath.Join(root, "shim.log")
	shim(t, bin, "id", "echo core")
	shim(t, bin, "sudo", `exec "$@"`)
	shim(t, bin, "getent", "exit 0")
	shim(t, bin, "sync", "exit 0")
	shim(t, bin, "systemctl", "exit 0")
	shim(t, bin, "sysctl", "echo 0")
	shim(t, bin, "rpm-ostree", "exit 0")
	shim(t, bin, "curl", curl)
	cmd := exec.Command("bash", filepath.Join("..", "scripts", "install-box.sh"), "--domain", "vm.test", "--source", "http://src.test")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SHIM_LOG="+logf, "FX="+fx, "HOME="+home, "LAIB_ETC="+etc, "LAIB_RESTIC_BIN="+resticBin, "LAIB_GH_TOKEN=")
	b, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	}
	l, _ := os.ReadFile(logf)
	return string(l), string(b), rc, resticBin
}

// #367: on the Kraftwerk, a missing restic made install-box.sh run `rpm-ostree
// install restic`: 98 packages (515 MB), because a layering re-resolves every
// layered package (the NVIDIA ones), and with a staged deployment the live
// apply is refused and the script rebooted the box mid-migration into a stale
// tree. Installing Laib is not an OS update: the script never layers and never
// reboots, on any box. restic is restic's own static release, pinned by its
// SHA256; git and diff are in the CoreOS image.
func TestInstallBoxNeverLayersNorReboots(t *testing.T) {
	if _, err := exec.LookPath("restic"); err == nil {
		t.Skip("restic is installed here: a failed download would keep it")
	}
	src, _ := os.ReadFile(filepath.Join("..", "scripts", "install-box.sh"))
	for _, bad := range []string{"rpm-ostree install", "systemctl reboot"} {
		if strings.Contains(string(src), bad) {
			t.Errorf("install-box.sh still runs %q: installing Laib must not change the OS or reboot the box", bad)
		}
	}
	// restic's download fails: the script stops and says so
	log, out, rc, _ := runPrereqs(t, `case "${@: -1}" in */VERSION) echo abc1234;; *) exit 22;; esac`)
	if strings.Contains(log, "rpm-ostree") || strings.Contains(log, "systemctl reboot") {
		t.Fatalf("install-box.sh touched rpm-ostree or rebooted for a missing restic\nshims:\n%s\noutput:\n%s", log, out)
	}
	if rc == 0 || rc == 75 {
		t.Fatalf("without restic the install must stop (not succeed, not ask for a reboot), got rc %d\n%s", rc, out)
	}
	if !strings.Contains(out, "restic") {
		t.Errorf("the stop must name restic:\n%s", out)
	}
}

// #367: restic comes from restic's GitHub release, so its checksum is pinned in
// the script: a download that does not match is refused and nothing lands in
// /usr/local/bin.
func TestResticIsRefusedOnAChecksumMismatch(t *testing.T) {
	log, out, rc, resticBin := runPrereqs(t, `o=""; prev=""
for a in "$@"; do [ "$prev" = -o ] && o=$a; prev=$a; done
case "${@: -1}" in
  */VERSION) echo abc1234;;
  https://github.com/restic/restic/releases/download/v*/restic_*_linux_amd64.bz2) printf 'not restic' > "$o";;
  *) exit 22;;
esac`)
	if !strings.Contains(out, "checksum") {
		t.Fatalf("a restic download with the wrong checksum was not refused by name\nshims:\n%s\noutput:\n%s", log, out)
	}
	if rc == 0 {
		t.Errorf("the install went on with an unverified restic:\n%s", out)
	}
	if _, err := os.Stat(resticBin); err == nil {
		t.Errorf("an unverified restic was installed at %s", resticBin)
	}
}
