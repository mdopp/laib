package render

import (
	"strings"
	"testing"
)

// owner 2026-11-07: only www, auth, kontakt and fotos face the internet;
// the family's apps are LAN-only, reached at home or through the Fritz!Box's
// WireGuard, whose clients have private addresses. fotos is Immich Public
// Proxy alone: Caddy lets only its paths through. files is at home too
// since 2026-11-09 (decision J, laib#1133), its share links excepted.
// Ablauf L-42 @6301
func TestTheFamilysAppsAreLANOnlyAndFotosIsTheSharesAlone(t *testing.T) {
	st := shippedBox(t)
	out, err := Render(st, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	caddy := out["caddy/Caddyfile"]
	site := func(host string) string {
		t.Helper()
		i := strings.Index(caddy, "\n"+host+".x.home {\n")
		if i < 0 {
			t.Fatalf("no site %s:\n%s", host, caddy)
		}
		return caddy[i : i+strings.Index(caddy[i:], "\n}\n")]
	}
	for _, h := range []string{"pi", "llama", "home", "caldav", "media", "hoerbuecher", "photos", "vault", "files"} {
		if s := site(h); !strings.Contains(s, "@lan remote_ip private_ranges") || !strings.Contains(s, "import lanonly") {
			t.Errorf("%s is not LAN-only:\n%s", h, s)
		}
	}
	if s := site("files"); !strings.Contains(s, "\t@public path /share/* /api/public/* /static/*\n\thandle @public {\n\t\treverse_proxy 127.0.0.1:8088\n") {
		t.Errorf("files' share links are not for everyone:\n%s", s)
	}
	for _, h := range []string{"auth", "fotos"} {
		if s := site(h); strings.Contains(s, "@lan") {
			t.Errorf("%s should face the internet:\n%s", h, s)
		}
	}
	f := site("fotos")
	for _, want := range []string{"@notshare not path /share /share/* /s/* /upload/healthcheck", "respond @notshare 403", "zone fotos {", "path_regexp ^/(share|s)/[^/]+/?$", "key {remote_host}", "reverse_proxy 127.0.0.1:2284"} {
		if !strings.Contains(f, want) {
			t.Errorf("fotos lacks %q:\n%s", want, f)
		}
	}
	for _, not := range []string{"import authelia", "2283"} {
		if strings.Contains(f, not) {
			t.Errorf("fotos has %q:\n%s", not, f)
		}
	}
	if !strings.Contains(out["authelia/laib-access.yml"], "domain: fotos.x.home\n      policy: bypass") {
		t.Errorf("Authelia has no bypass for fotos")
	}
}
