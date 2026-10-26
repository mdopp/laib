package render

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #480, the walk on the box: three dead ends the denial pages left.
//   - A service that is down (an update, the GPU lease, Authelia restarting)
//     answered Caddy's bare, empty 502 on every name behind it.
//   - With the wildcard certificate an unknown name (a typo, an old
//     bookmark like laib.<domain>) got abort: the browser's "leere Antwort".
//   - The LAN-only page said "Dein Gerät ist nicht im WLAN" to a phone at
//     home whose Private DNS resolves the public address: Caddy then sees
//     the router's public address (measured: the hairpin through the
//     router), so the page names the address it saw and that cause.
func TestNoDeadEndOnDownUnknownOrHairpin(t *testing.T) {
	cf := caddyfileFor(t, state.DNS{Provider: "desec", SecretRef: "DNS_API_TOKEN"}, "")
	for _, want := range []string{
		"handle_errors 502 503 504 {",
		"antwortet gerade nicht",
		"HTML {err.status_code}",
		"Diese Adresse gibt es auf dieser Box nicht",
		"HTML 404",
		"{http.request.remote.host}",
		"Privates DNS",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("the Caddyfile lacks %q", want)
		}
	}
	if regexp.MustCompile(`(?m)^\s*abort\s*$`).MatchString(cf) {
		t.Errorf("an unknown name still gets abort:\n%s", cf)
	}
	// every site that proxies says so when its service is down
	for _, site := range regexp.MustCompile(`(?ms)^\S+ \{\n.*?^\}$`).FindAllString(cf, -1) {
		if strings.Contains(site, "reverse_proxy") && !strings.Contains(site, "\timport down\n") {
			t.Errorf("a site without the down page:\n%s", site)
		}
	}
}
