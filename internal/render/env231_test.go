package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #231's VM probe: Quadlet splits an Environment= value at blanks outside
// quotes, as systemd does. Unquoted blanks made samba's two config values
// lists of stray words ("read", "=", "SMB2"), and samba did not start on
// the VM; they cut vaultwarden's SSO_SCOPES down to "email". Every
// built-in's Environment= line holds one KEY=value.
func TestEnvironmentLinesHoldWholeAssignments(t *testing.T) {
	units, _ := filepath.Glob("../../templates/*/*.container")
	if len(units) < 20 {
		t.Fatalf("found %d units", len(units))
	}
	for _, u := range units {
		b, _ := os.ReadFile(u)
		for _, line := range strings.Split(string(b), "\n") {
			k, rest, _ := strings.Cut(line, "=")
			if strings.TrimSpace(k) != "Environment" {
				continue
			}
			words := envWords(rest)
			if key, _, ok := strings.Cut(strings.Join(words, ""), "="); len(words) != 1 || !ok || key == "" {
				t.Errorf("%s: %s: Quadlet reads %d words %q; one assignment per line, a value with blanks in quotes", u, line, len(words), words)
			}
		}
	}
}
