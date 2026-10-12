package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #55 (1): apps with their own login or OIDC cannot pass Authelia's
// forward-auth — Jellyfin's clients, the Paperless apps, File Browser.
// Their routes are auth: none; the browser-only admin surfaces stay
// behind Authelia. The policy is written down in templates/README.md.
func TestFamilyAppsRouteWithTheirOwnLogin(t *testing.T) {
	for _, tc := range []struct{ tpl, host, want string }{
		{"media", "media", "auth: none"},
		{"paperless", "paperless", "auth: none"},
		{"file-share", "files", "auth: none"},
		{"file-share", "sync", "auth: one_factor"},
	} {
		b, _ := os.ReadFile(filepath.Join("..", "..", "templates", tc.tpl, "laib.yml"))
		i := strings.Index(string(b), "host: "+tc.host)
		if i < 0 {
			t.Fatalf("%s: no route %s", tc.tpl, tc.host)
		}
		block := string(b)[i:]
		if j := strings.Index(block[1:], "- host:"); j > 0 {
			block = block[:j]
		}
		if !strings.Contains(block, tc.want) {
			t.Errorf("%s route %s: want %s:\n%s", tc.tpl, tc.host, tc.want, block)
		}
	}
	readme, _ := os.ReadFile(filepath.Join("..", "..", "templates", "README.md"))
	if !strings.Contains(string(readme), "## Route policy") || !strings.Contains(string(readme), "auth: none") {
		t.Fatal("templates/README.md must carry the route policy")
	}
}
