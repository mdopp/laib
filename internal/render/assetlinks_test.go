package render

import (
	"regexp"
	"strings"
	"testing"
)

// Ablauf L-64 (homes ADR 0004 §5): a tag's address https://www.<domain>/tag/<id> opens
// the Homes app once Android has fetched www's /.well-known/assetlinks.json
// without a login. Authelia lets that one path through, exactly (not a
// prefix, not a neighbour); Caddy serves it on the authenticated handler,
// so Homes still gets its proxy secret; /tag/ stays behind the login. The
// app's certificate reaches Homes as HOMES_APP_SHA256.
func TestHomesAssetLinksPassWithoutALoginAndNothingElse(t *testing.T) {
	st := shippedBox(t)
	out, err := Render(st, map[string]string{"HOMES_PROXY_SECRET": "k-proxy"})
	if err != nil {
		t.Fatal(err)
	}
	access := out["authelia/laib-access.yml"]
	want := `resources: ["^/\\.well-known/assetlinks\\.json(\\?.*)?$"]`
	if !strings.Contains(access, want) || !strings.Contains(access, `resources: ["^/napi/.*$"]`) {
		t.Fatalf("Authelia's bypass for www lacks assetlinks.json alone, or lost /napi/:\n%s", access)
	}
	re := regexp.MustCompile(`^/\.well-known/assetlinks\.json(\?.*)?$`)
	for path, open := range map[string]bool{"/.well-known/assetlinks.json": true, "/.well-known/assetlinks.json?x=1": true,
		"/.well-known/assetlinks.jsonx": false, "/.well-known/other": false, "/tag/homes-0a1b2c3d4e5f": false, "/": false} {
		if re.MatchString(path) != open {
			t.Errorf("%s: open %v, want %v", path, !open, open)
		}
	}
	if strings.Contains(access, "/tag") {
		t.Errorf("/tag/ must stay behind the login:\n%s", access)
	}
	caddy := out["caddy/Caddyfile"]
	site, _, _ := strings.Cut(caddy[strings.Index(caddy, "\nwww.x.home {")+1:], "\n}\n")
	if !strings.Contains(site, "@bypass path /napi/*\n") || strings.Contains(site, "assetlinks") || !strings.Contains(site, `header_up X-Homes-Proxy "k-proxy"`) {
		t.Errorf("www: the file must go to the authenticated handler with the proxy secret, /napi/ alone past it:\n%s", site)
	}
	if !regexp.MustCompile(`(?m)^Environment="?HOMES_APP_SHA256=C3:6A:41:03:80:AB:C9:9D:D5:6E:DF:14:5F:34:FF:A6:DA:7F:4A:15:B0:98:A3:7F:FD:AB:D7:02:FD:68:A6:2D"?$`).MatchString(out["quadlet/homes/homes.container"]) {
		t.Errorf("Homes lacks the app's certificate:\n%s", out["quadlet/homes/homes.container"])
	}
}
