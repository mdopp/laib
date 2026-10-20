package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #480: a person never meets a bare 403. A LAN-only name opened from outside
// (the phone on mobile data) says so in German and names the way out; a
// login whose account may not open a name (a family member on an admin
// page) gets a page with "abmelden, anderes Konto" instead of Authelia's
// plain "403 Forbidden". The status stays 403 and the LAN check still runs
// before Authelia: only the words change, never who gets in.
func TestNoBare403ReachesAPerson(t *testing.T) {
	st, err := state.Load("../../testdata/golden/nuc/state")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	caddy := out["caddy/Caddyfile"]
	if regexp.MustCompile(`(?m)^\s*respond 403\s*$`).MatchString(caddy) {
		t.Errorf("a bare respond 403 is left:\n%s", caddy)
	}
	for _, want := range []string{
		"Diese Seite ist nur zu Hause im WLAN erreichbar",
		"Kein Zugang mit diesem Konto",
		"https://auth.nuc.home/logout?rd=https://{http.request.host}/",
		"@denied status 403",
		"#070709", "prefers-color-scheme: light", // the household look (#382)
	} {
		if !strings.Contains(caddy, want) {
			t.Errorf("the Caddyfile lacks %q", want)
		}
	}
	// every LAN-only site: the LAN check first, the page for everyone else
	for _, site := range regexp.MustCompile(`(?ms)^\S+ \{\n.*?^\}$`).FindAllString(caddy, -1) {
		if !strings.Contains(site, "@lan remote_ip private_ranges") {
			continue
		}
		lan, page := strings.Index(site, "handle @lan {"), strings.Index(site, "import lanonly")
		if lan < 0 || page < lan {
			t.Errorf("a LAN-only site without its page after the LAN check:\n%s", site)
		}
	}
	if n := strings.Count(caddy, "HTML 403"); n != 2 {
		t.Errorf("want the two pages, each with status 403, got %d", n)
	}
	if bin := os.Getenv("LAIB_CADDY"); bin != "" { // the real binary (CI validates the golden files the same way)
		f := filepath.Join(t.TempDir(), "Caddyfile")
		os.WriteFile(f, []byte(caddy), 0o644)
		if b, err := exec.Command(bin, "validate", "--config", f, "--adapter", "caddyfile").CombinedOutput(); err != nil {
			t.Fatalf("caddy validate: %v\n%s", err, b)
		}
	}
}
