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
	shim(t, bin, "sudo", "exit 0")
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

// runLayering runs install-box.sh --source against an rpm-ostree shim (body
// rpmOstree) until the layering is over: what is under test is which
// rpm-ostree calls it makes, whether it reboots, and its exit code. etc is
// LAIB_ETC, shared between runs that stand for consecutive boots.
func runLayering(t *testing.T, etc, rpmOstree string) (log, out string, rc int) {
	t.Helper()
	if _, err := exec.LookPath("restic"); err == nil {
		t.Skip("restic is installed here: install-box.sh would not layer it")
	}
	root := t.TempDir()
	bin, home, fx := filepath.Join(root, "bin"), filepath.Join(root, "home"), filepath.Join(etc, "fx")
	for _, d := range []string{bin, home, fx, filepath.Join(etc, "laib")} {
		os.MkdirAll(d, 0o755)
	}
	logf := filepath.Join(root, "shim.log")
	shim(t, bin, "id", "echo core")
	shim(t, bin, "sudo", `exec "$@"`)
	shim(t, bin, "getent", "exit 0")
	shim(t, bin, "sync", "exit 0")
	shim(t, bin, "systemctl", "exit 0")
	shim(t, bin, "sysctl", "echo 0")
	shim(t, bin, "rpm-ostree", rpmOstree)
	shim(t, bin, "curl", `case "${@: -1}" in */VERSION) echo abc1234;; *) exit 22;; esac`)
	cmd := exec.Command("bash", filepath.Join("..", "scripts", "install-box.sh"), "--domain", "vm.test", "--source", "http://src.test")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SHIM_LOG="+logf, "FX="+fx, "BIN="+bin, "HOME="+home, "LAIB_ETC="+etc, "LAIB_GH_TOKEN=")
	b, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	}
	l, _ := os.ReadFile(logf)
	return string(l), string(b), rc
}

// #206: a power cut during the first `rpm-ostree install` leaves zero-length
// files in its metadata cache (a metalink, a repo's GPG key), and every later
// run fails on them: "PKI file /var/cache/rpm-ostree/repomd/…/RPM-GPG-KEY-…
// contains no valid public key" (vm-install 38732387270, 38732400495). A
// reboot heals nothing; the box rebooted every 23 s, forever. The cache is
// cleared and the layering tried again in the same boot.
func TestLayeringHealsAnEmptiedCacheWithoutAReboot(t *testing.T) {
	etc := t.TempDir()
	os.MkdirAll(filepath.Join(etc, "laib"), 0o755)
	os.WriteFile(filepath.Join(etc, "laib", "layer-reboots"), []byte("1\n"), 0o644)
	log, out, _ := runLayering(t, etc, `case "$1" in
  cleanup) touch "$FX/cleaned";;
  install) [ -f "$FX/cleaned" ] || { echo "error: PKI file /var/cache/rpm-ostree/repomd/x/RPM-GPG-KEY-fedora-44-x86_64 contains no valid public key" >&2; exit 1; }
           printf '#!/bin/sh\n' > "$BIN/restic"; chmod +x "$BIN/restic";;
esac`)
	if !strings.Contains(log, "rpm-ostree cleanup --repomd") {
		t.Fatalf("the emptied metadata cache was never cleared\nshims:\n%s\noutput:\n%s", log, out)
	}
	if strings.Count(log, "\nrpm-ostree install") != 2 { // the sudo shim logs each call twice
		t.Fatalf("the layering must be tried once more after the cache is cleared\nshims:\n%s", log)
	}
	if strings.Contains(log, "systemctl reboot") {
		t.Fatalf("rebooted although the layering succeeded after clearing the cache\nshims:\n%s\noutput:\n%s", log, out)
	}
	if _, err := os.Stat(filepath.Join(etc, "laib", "layer-reboots")); err == nil {
		t.Error("the reboot count survives a layering that succeeded: the next failure gets fewer reboots")
	}
}

// #206: a layering failure no reboot fixes must not become a reboot loop.
// Two reboots into the layered tree (counted on disk, like the DNS reboots of
// #111), then install-box.sh fails and the box stays up and reachable.
func TestLayeringRebootsAtMostTwice(t *testing.T) {
	etc := t.TempDir()
	always := `case "$1" in install) echo "error: no luck" >&2; exit 1;; esac`
	for boot := 1; boot <= 3; boot++ {
		log, out, rc := runLayering(t, etc, always)
		rebooted := strings.Contains(log, "systemctl reboot")
		if boot <= 2 && (rc != 75 || !rebooted) {
			t.Fatalf("boot %d: want a reboot into the layered tree (exit 75), got rc %d\nshims:\n%s\noutput:\n%s", boot, rc, log, out)
		}
		if boot == 3 {
			if rebooted || rc == 75 || rc == 0 {
				t.Fatalf("boot 3: after two reboots the layering must fail without another reboot, got rc %d\nshims:\n%s\noutput:\n%s", rc, log, out)
			}
			if !strings.Contains(out, "rpm-ostree") {
				t.Errorf("boot 3: the failure should name rpm-ostree:\n%s", out)
			}
		}
	}
}
