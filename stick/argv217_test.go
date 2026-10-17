package stick

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #217: ps and /proc/<pid>/cmdline show every argv on the box to every local
// user. A private fork's GitHub token reaches curl in a header file only
// its reader can read, never as `-H "Authorization: Bearer …"` on argv.
func TestInstallBoxKeepsTheGitHubTokenOffArgv(t *testing.T) {
	const tok = "ghp_argv217secret"
	v040 := release("v0.4.0", 4, true)
	log, out := runInstallBox(t, tok, v040, v040)
	if strings.Contains(log, tok) {
		t.Fatalf("the GitHub token is on curl's argv, where ps shows it:\n%s", log)
	}
	if !strings.Contains(log, "/releases/assets/41\n") {
		t.Fatalf("the release was not resolved with the token\nshims:\n%s\noutput:\n%s", log, out)
	}
	// every GitHub call still carries the bearer: the API reads and the asset download
	if n := strings.Count(log, "curl: the bearer from a file of mode 600\n"); n < 2 {
		t.Fatalf("want the bearer from a 0600 file on the API call and the download, got %d\nshims:\n%s", n, log)
	}
}

// The token as a flag is on bash's argv for the whole install: install-box.sh
// takes it from LAIB_GH_TOKEN only and says so.
func TestInstallBoxRefusesTheTokenAsAFlag(t *testing.T) {
	cmd := exec.Command("bash", filepath.Join("..", "scripts", "install-box.sh"), "--domain", "trial.example", "--token", "ghp_x")
	b, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(b), "LAIB_GH_TOKEN") {
		t.Fatalf("--token must be refused with a pointer to LAIB_GH_TOKEN (err %v):\n%s", err, b)
	}
}

// The first boot fetches the installer of the release it installs from the
// GitHub API, with the token when the stick carries one: off argv there too.
func TestFirstBootKeepsTheGitHubTokenOffArgv(t *testing.T) {
	const tok = "ghp_firstboot217"
	_, log, out := runFirstBoot(t, "exit 0\n", "LAIB_SOURCE=", "LAIB_GH_TOKEN="+tok)
	if strings.Contains(log, tok) {
		t.Fatalf("the GitHub token is on curl's argv, where ps shows it:\n%s", log)
	}
	if !strings.Contains(log, "/contents/scripts/install-box.sh?ref=v9.9.9") {
		t.Fatalf("the installer of the latest release was not fetched\nshims:\n%s\noutput:\n%s", log, out)
	}
	if n := strings.Count(log, "curl: the bearer from a file of mode 600\n"); n != 2 {
		t.Fatalf("want the bearer from a 0600 file on the release lookup and the installer download, got %d\nshims:\n%s", n, log)
	}
}
