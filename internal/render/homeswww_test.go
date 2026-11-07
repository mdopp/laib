package render

import (
	"strings"
	"testing"
)

// Owner 2026-11-07 (ADR 0031 amendment): Homes is the family's entry, so it
// lives on www.<domain>, and the domain itself redirects there. homes.<domain>
// and the legacy haus.<domain> are gone at once, no redirect: no site, no
// Authelia rule, so Caddy renews no certificate for them. Homes' own address
// (HOMES_PUBLIC_URL, the links it makes) is the same host as its first route.
func TestHomesLivesOnWWWAndTheOldNamesAreGone(t *testing.T) {
	st := shippedBox(t)
	out, err := Render(st, map[string]string{"HOMES_PROXY_SECRET": "psec"})
	if err != nil {
		t.Fatal(err)
	}
	caddy := out["caddy/Caddyfile"]
	i := strings.Index(caddy, "\nwww.x.home {\n")
	if i < 0 {
		t.Fatalf("no site www.x.home:\n%s", caddy)
	}
	site := caddy[i : i+strings.Index(caddy[i:], "\n}\n")]
	for _, want := range []string{"@bypass path /napi/*", "reverse_proxy 127.0.0.1:8650", "import authelia", `header_up X-Homes-Proxy "psec"`} {
		if !strings.Contains(site, want) {
			t.Errorf("www.x.home lacks %q:\n%s", want, site)
		}
	}
	if !strings.Contains(caddy, "\nx.home {\n\tredir https://www.x.home{uri}\n}") {
		t.Errorf("the domain itself does not lead to www.:\n%s", caddy)
	}
	acc := out["authelia/laib-access.yml"]
	if !strings.Contains(acc, "domain: www.x.home\n      policy: bypass\n      resources:") || !strings.Contains(acc, "domain: www.x.home\n      policy: one_factor") {
		t.Errorf("Authelia lacks www.'s rules (/napi/ bypass, one factor):\n%s", acc)
	}
	for _, old := range []string{"homes.x.home", "haus.x.home"} {
		if strings.Contains(caddy, old) || strings.Contains(acc, old) {
			t.Errorf("%s is still there: Caddy or Authelia names it", old)
		}
	}
	if u := HomesURL(st); u != "https://www.x.home/" {
		t.Errorf("the family's entry is %q, want www.", u)
	}
}

// HOMES_PUBLIC_URL is written in homes.container, the route's host in
// laib.yml: a move of one without the other would send every link Homes
// makes (vCards, the pairing, the Bote's answers) to a name with no site.
func TestHomesPublicURLIsItsRoute(t *testing.T) {
	st := shippedBox(t)
	out, err := Render(st, map[string]string{"HOMES_PROXY_SECRET": "psec"})
	if err != nil {
		t.Fatal(err)
	}
	unit := out["quadlet/homes/homes.container"]
	want := "\nEnvironment=HOMES_PUBLIC_URL=" + strings.TrimSuffix(HomesURL(st), "/") + "\n"
	if unit == "" || !strings.Contains(unit, want) {
		t.Errorf("homes.container does not say %q:\n%s", strings.TrimSpace(want), unit)
	}
}
