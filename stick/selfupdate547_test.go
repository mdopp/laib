package stick

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #547: Laib kept everything on the box current but itself — the Kraftwerk
// ran v0.11.1 while v0.16.0 was out, and nothing said so. self-update.sh
// runs before the nightly image run (laib-images.service ExecStartPre, as
// core): the newest release with binaries, or the version box.yml holds,
// through that release's own install-box.sh (verified against SHA256SUMS),
// only while no backup runs, nobody holds the lock and laib is green; after
// it /ready must report the new version green, else the binaries that ran
// come back. A rolled-back release is not tried again; a newer one is.

type selfBox struct{ root, bin, fx, home, opt, log string }

// newSelfBox: a box running v0.11.1 whose /ready is green. fx holds the
// releases API's answer, the release downloads and /ready's body.
func newSelfBox(t *testing.T) *selfBox {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not here; Fedora CoreOS ships it and the script reads the releases and /ready with it")
	}
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock is not here; util-linux on Fedora CoreOS has it")
	}
	root := t.TempDir()
	s := &selfBox{root: root, bin: filepath.Join(root, "bin"), fx: filepath.Join(root, "fx"), home: filepath.Join(root, "home"), opt: filepath.Join(root, "opt"), log: filepath.Join(root, "shim.log")}
	for _, d := range []string{s.bin, s.fx, s.home, s.opt} {
		os.MkdirAll(d, 0o755)
	}
	s.laib("v0.11.1")
	for _, b := range []string{"laib-operator", "laib-lease"} {
		os.WriteFile(filepath.Join(s.opt, b), []byte("#!/bin/sh\n# old "+b+"\n"), 0o755)
	}
	s.ready("v0.11.1", "bereit", 0)
	s.releases(`[{"tag_name":"v0.16.0","draft":false,"prerelease":false,"assets":[{"name":"laib-linux-amd64"},{"name":"SHA256SUMS"}]}]`)
	shim(t, s.bin, "systemctl", `[ "$*" = "--user is-active laib-backup.service" ] && cat "$FX/backup" 2>/dev/null; exit 0`)
	shim(t, s.bin, "sudo", `exec "$@"`)
	shim(t, s.bin, "sleep", "exit 0")
	// the network: /ready, the releases API and the release downloads
	shim(t, s.bin, "curl", `o=""; prev=""
for a in "$@"; do [ "$prev" = -o ] && o=$a; prev=$a; done
u="${@: -1}"
case "$u" in
  */ready) cat "$FX/ready";;
  *api.test/releases*) cat "$FX/releases";;
  *dl.test/*) f="$FX/dl/${u#*dl.test/}"; [ -f "$f" ] || exit 22; cp "$f" "$o";;
  *) exit 22;;
esac`)
	return s
}

// laibBin is a laib binary as the real one answers version: plain text,
// JSON only with --json (cmd/laib cmdVersion). It logs every call with its
// own version, so a test sees which binary ran converge.
func laibBin(version string) string {
	return `#!/bin/sh
echo "laib[` + version + `] $*" >> "$SHIM_LOG"
if [ "$1" = version ]; then
  if [ "${2:-}" = --json ]; then printf '{\n  "version": "` + version + `"\n}\n'; else echo "laib ` + version + `"; fi
fi
exit 0
`
}

// laib puts that binary in the bin dir; the release's binary waits in fx.
func (s *selfBox) laib(version string) {
	os.WriteFile(filepath.Join(s.opt, "laib"), []byte(laibBin(version)), 0o755)
}

func (s *selfBox) ready(version, state string, red int) {
	b, _ := json.Marshal(map[string]any{"state": state, "red": red, "version": version})
	os.WriteFile(filepath.Join(s.fx, "ready"), b, 0o644)
}

func (s *selfBox) releases(body string) {
	os.WriteFile(filepath.Join(s.fx, "releases"), []byte(body), 0o644)
}

// release publishes tag's install-box.sh (body) and its SHA256SUMS; sum
// overrides the listed checksum ("" lists the real one).
func (s *selfBox) release(tag, body, sum string) {
	d := filepath.Join(s.fx, "dl", tag)
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "laib-install-box.sh"), []byte(body), 0o644)
	if sum == "" {
		h := sha256.Sum256([]byte(body))
		sum = hex.EncodeToString(h[:])
	}
	os.WriteFile(filepath.Join(d, "SHA256SUMS"), []byte("1111  laib-linux-amd64\n"+sum+"  laib-install-box.sh\n"), 0o644)
}

// installer is a release's install-box.sh: it logs how it was called, puts
// the new laib in place (and a lease) and lets /ready answer with the
// version and state it would.
func (s *selfBox) installer(version, state string) string {
	b, _ := json.Marshal(map[string]any{"state": state, "red": 0, "version": version})
	os.WriteFile(filepath.Join(s.fx, "laib-"+version), []byte(laibBin(version)), 0o755)
	return `echo "install-box.sh $* LAIB_SELF_UPDATE=$LAIB_SELF_UPDATE" >> "$SHIM_LOG"
cp "$FX/laib-` + version + `" "$LAIB_BIN_DIR/laib"
echo "#!/bin/sh" > "$LAIB_BIN_DIR/laib-lease"
echo '` + string(b) + `' > "$FX/ready"
`
}

func (s *selfBox) run(t *testing.T, env ...string) (log, out string, rec map[string]string) {
	t.Helper()
	os.WriteFile(s.log, nil, 0o644)
	cmd := exec.Command("bash", filepath.Join("..", "scripts", "self-update.sh"))
	cmd.Env = append(os.Environ(), "PATH="+s.bin+":"+os.Getenv("PATH"), "SHIM_LOG="+s.log, "FX="+s.fx,
		"LAIB_HOME="+s.home, "LAIB_BIN_DIR="+s.opt, "LAIB_RELEASES=http://api.test/releases", "LAIB_DOWNLOAD=http://dl.test", "LAIB_READY_URL=http://127.0.0.1:5900/ready", "LAIB_SELF_UPDATE_WAIT=3")
	cmd.Env = append(cmd.Env, env...)
	o, _ := cmd.CombinedOutput()
	l, _ := os.ReadFile(s.log)
	b, _ := os.ReadFile(filepath.Join(s.home, "self-update.json"))
	rec = map[string]string{}
	json.Unmarshal(b, &rec)
	return string(l), string(o), rec
}

func installed(log string) bool { return strings.Contains(log, "install-box.sh --version") }

func TestSelfUpdateLeavesACurrentLaibAlone(t *testing.T) {
	s := newSelfBox(t)
	s.releases(`[{"tag_name":"v0.11.1","draft":false,"prerelease":false,"assets":[{"name":"laib-linux-amd64"}]}]`)
	log, out, rec := s.run(t)
	if installed(log) || rec["result"] != "current" || rec["running"] != "v0.11.1" {
		t.Fatalf("a current laib must stay as it is and say so: %v\n%s\n%s", rec, log, out)
	}
}

func TestSelfUpdateTakesTheNewestReleaseAndProvesIt(t *testing.T) {
	s := newSelfBox(t)
	// v0.17.0 is out, its binaries follow its VM test: v0.16.0 is the newest with them
	s.releases(`[{"tag_name":"v0.17.0","draft":false,"prerelease":false,"assets":[]},
	 {"tag_name":"v0.16.0","draft":false,"prerelease":false,"assets":[{"name":"laib-linux-amd64"},{"name":"SHA256SUMS"}]}]`)
	s.release("v0.16.0", s.installer("v0.16.0", "bereit"), "")
	log, out, rec := s.run(t)
	if !strings.Contains(log, "install-box.sh --version v0.16.0 LAIB_SELF_UPDATE=1") {
		t.Fatalf("v0.16.0's own installer did not run as a self-update:\n%s\n%s", log, out)
	}
	if rec["result"] != "updated" || rec["running"] != "v0.11.1" || rec["latest"] != "v0.16.0" {
		t.Errorf("the record must say v0.11.1 → v0.16.0, updated: %v\n%s", rec, out)
	}
	if !strings.Contains(log, "] note --kind watch") || !strings.Contains(log, "v0.11.1 → v0.16.0") {
		t.Errorf("the journal must carry the update:\n%s", log)
	}
}

func TestSelfUpdateRollsBackARedReleaseAndDoesNotRetryIt(t *testing.T) {
	s := newSelfBox(t)
	s.release("v0.16.0", s.installer("v0.16.0", "eingeschränkt"), "")
	log, out, rec := s.run(t)
	if !installed(log) {
		t.Fatalf("the installer never ran:\n%s\n%s", log, out)
	}
	if b, _ := os.ReadFile(filepath.Join(s.opt, "laib-lease")); !strings.Contains(string(b), "old laib-lease") {
		t.Errorf("the lease that ran before is not back:\n%s", b)
	}
	// the Operator and notify are containers (ADR 0027): the old laib's converge brings their images back
	if strings.Contains(log, "laib-operator") || strings.Contains(log, "laib-notify") {
		t.Errorf("the Operator or notify were handled as binaries or host units:\n%s", log)
	}
	if b, _ := exec.Command(filepath.Join(s.opt, "laib"), "version").Output(); string(b) != "laib v0.11.1\n" {
		t.Errorf("laib is not back on v0.11.1: %s\n%s", b, out)
	}
	if !strings.Contains(log, "systemctl --user try-restart laib.service") {
		t.Errorf("the restored binaries were not restarted:\n%s", log)
	}
	// the new release's converge rendered its files; the old laib renders its own back
	if i, j := strings.Index(log, "laib[v0.16.0] converge"), strings.LastIndex(log, "laib[v0.11.1] converge --force"); j < 0 || j < i {
		t.Errorf("after the rollback the old laib must converge the box back to its own rendering:\n%s", log)
	}
	if rec["result"] != "failed" || rec["latest"] != "v0.16.0" || !strings.Contains(log, "] note --kind incident") {
		t.Errorf("a rollback must be recorded and journaled as an incident: %v\n%s", rec, log)
	}
	s.ready("v0.11.1", "bereit", 0)
	log, out, rec = s.run(t)
	if installed(log) || rec["result"] != "failed" {
		t.Fatalf("a rolled-back release was tried again: %v\n%s\n%s", rec, log, out)
	}
	// a night the releases cannot be read keeps the mark
	keep := s.releases
	keep(`not json`)
	s.run(t)
	keep(`[{"tag_name":"v0.16.0","draft":false,"prerelease":false,"assets":[{"name":"laib-linux-amd64"}]}]`)
	if log, out, rec = s.run(t); installed(log) || rec["failed"] != "v0.16.0" {
		t.Fatalf("a night without the releases list erased the rollback's mark: %v\n%s\n%s", rec, log, out)
	}
	s.releases(`[{"tag_name":"v0.16.1","draft":false,"prerelease":false,"assets":[{"name":"laib-linux-amd64"}]}]`)
	s.release("v0.16.1", s.installer("v0.16.1", "bereit"), "")
	if log, out, rec = s.run(t); !installed(log) || rec["result"] != "updated" {
		t.Fatalf("a newer release after a rollback must be tried: %v\n%s\n%s", rec, log, out)
	}
}

func TestSelfUpdateRefusesAnInstallerThatDoesNotMatchItsSums(t *testing.T) {
	s := newSelfBox(t)
	s.release("v0.16.0", s.installer("v0.16.0", "bereit"), strings.Repeat("0", 64))
	if log, out, rec := s.run(t); installed(log) || rec["result"] != "failed" {
		t.Fatalf("an unverified installer ran: %v\n%s\n%s", rec, log, out)
	}
}

func TestSelfUpdateOffAndHeld(t *testing.T) {
	s := newSelfBox(t)
	s.release("v0.16.0", s.installer("v0.16.0", "bereit"), "")
	if log, out, rec := s.run(t, "LAIB_UPDATE=off"); installed(log) || rec["result"] != "off" || rec["latest"] != "v0.16.0" {
		t.Fatalf("off must install nothing and still say what is out: %v\n%s\n%s", rec, log, out)
	}
	s.release("v0.12.0", s.installer("v0.12.0", "bereit"), "")
	if log, out, rec := s.run(t, "LAIB_UPDATE=v0.12.0"); !strings.Contains(log, "install-box.sh --version v0.12.0 ") || rec["latest"] != "v0.12.0" {
		t.Fatalf("a held version is the target, not the newest release: %v\n%s\n%s", rec, log, out)
	}
}

func TestSelfUpdateWaitsForACalmBox(t *testing.T) {
	for name, setup := range map[string]func(s *selfBox){
		"backup":    func(s *selfBox) { os.WriteFile(filepath.Join(s.fx, "backup"), []byte("active\n"), 0o644) },
		"not green": func(s *selfBox) { s.ready("v0.11.1", "bereit", 1) },
		"locked": func(s *selfBox) {
			lock := filepath.Join(s.home, "apply.lock")
			os.WriteFile(lock, []byte("apply:human:core pid 1 since now"), 0o644)
			hold := exec.Command("flock", lock, "sleep", "5")
			hold.Start()
			t.Cleanup(func() { hold.Process.Kill(); hold.Wait() })
			for i := 0; i < 50 && exec.Command("flock", "-n", lock, "true").Run() == nil; i++ {
				exec.Command("/bin/sleep", "0.05").Run()
			}
		},
	} {
		s := newSelfBox(t)
		s.release("v0.16.0", s.installer("v0.16.0", "bereit"), "")
		setup(s)
		if log, out, rec := s.run(t); installed(log) || rec["result"] != "skipped" {
			t.Errorf("%s: the update must wait for the next night: %v\n%s\n%s", name, rec, log, out)
		}
	}
}

func TestInstallPutsTheVerifiedSelfUpdateInPlace(t *testing.T) {
	real, _ := os.ReadFile(filepath.Join("..", "scripts", "boot-check.sh"))
	etc, out := runInstallTo(t, string(real))
	log, _ := os.ReadFile(filepath.Join(filepath.Dir(etc), "shim.log"))
	if !strings.Contains(string(log), "sudo mv /var/opt/laib/self-update.sh.new /var/opt/laib/self-update.sh\n") {
		t.Fatalf("install-box.sh did not put the release's self-update.sh in place:\n%s\n%s", log, out)
	}
}

// the installer can stop before it touched a binary (a download, restic, a
// stalled connection): that is no verdict on the release. Nothing comes
// back, nothing is marked, the next night tries again.
func TestSelfUpdateRetriesAReleaseWhoseInstallStoppedEarly(t *testing.T) {
	for name, body := range map[string]string{
		"fails":  `echo "install-box.sh $*" >> "$SHIM_LOG"; echo "cannot download restic" >&2; exit 1`,
		"stalls": `echo "install-box.sh $*" >> "$SHIM_LOG"; /bin/sleep 30`,
	} {
		s := newSelfBox(t)
		s.release("v0.16.0", body, "")
		log, out, rec := s.run(t, "LAIB_SELF_UPDATE_TIMEOUT=1")
		if !installed(log) || rec["result"] != "skipped" || rec["failed"] != "" || strings.Contains(log, "try-restart") {
			t.Fatalf("%s: an install that never reached the binaries is skipped, not rolled back or marked: %v\n%s\n%s", name, rec, log, out)
		}
		s.release("v0.16.0", s.installer("v0.16.0", "bereit"), "")
		if log, out, rec = s.run(t); !installed(log) || rec["result"] != "updated" {
			t.Fatalf("%s: the next night must try it again: %v\n%s\n%s", name, rec, log, out)
		}
	}
}

// auto never goes back: a box on a build newer than the newest release (the
// VM job's --source, a hand-installed fix) stays on it.
func TestSelfUpdateNeverDowngradesInAuto(t *testing.T) {
	s := newSelfBox(t)
	s.laib("v0.17.0")
	s.release("v0.16.0", s.installer("v0.16.0", "bereit"), "")
	if log, out, rec := s.run(t); installed(log) || rec["result"] != "current" {
		t.Fatalf("auto went from v0.17.0 back to v0.16.0: %v\n%s\n%s", rec, log, out)
	}
}

// behindSince: since when the box runs older than the newest release, kept
// across skipped nights, so diag can say a box that never gets its calm
// night (a red service that stays red) has stopped following.
func TestSelfUpdateKeepsSinceWhenItIsBehind(t *testing.T) {
	s := newSelfBox(t)
	s.ready("v0.11.1", "bereit", 1)
	s.release("v0.16.0", s.installer("v0.16.0", "bereit"), "")
	_, _, first := s.run(t)
	_, _, second := s.run(t)
	if first["behindSince"] == "" || second["behindSince"] != first["behindSince"] {
		t.Fatalf("behindSince must be set once and kept: %v then %v", first, second)
	}
	s.ready("v0.11.1", "bereit", 0)
	if _, _, rec := s.run(t); rec["result"] != "updated" || rec["behindSince"] != "" {
		t.Fatalf("an updated box is not behind: %v", rec)
	}
}
