package render

import (
	"os"
	"strings"
	"testing"
)

// owner 2026-11-07: Kontakt prüfen lassen's page is Homes' only one without
// a login. It has a host of its own on a port of its own (Homes serves
// nothing else there); homes.<domain> stays wholly behind Authelia, with
// /napi/ its only bypass as before. Caddy limits the rate and the body on
// the new host and sends no proxy secret there.
func TestHomesKontaktIsItsOwnHostWithoutLoginAndHomesStaysBehindAuthelia(t *testing.T) {
	st := shippedBox(t)
	out, err := Render(st, map[string]string{"HOMES_PROXY_SECRET": "psec"})
	if err != nil {
		t.Fatal(err)
	}
	caddy := out["caddy/Caddyfile"]
	site := func(host string) string {
		t.Helper()
		i := strings.Index(caddy, "\n"+host+" {\n")
		if i < 0 {
			t.Fatalf("no site %s:\n%s", host, caddy)
		}
		return caddy[i : i+strings.Index(caddy[i:], "\n}\n")]
	}
	k := site("kontakt.x.home")
	// the body: 32 KB, 6 MB only for the multipart POST that carries the contact's photo (owner 2026-11-08, homes#304)
	for _, want := range []string{"request_header -Remote-*", "reverse_proxy 127.0.0.1:8652", "rate_limit {", "zone kontakt {", "key {remote_host}", "events 30", "window 10m",
		"@foto {\n\t\tmethod POST\n\t\theader Content-Type multipart/form-data*\n\t}\n\trequest_body @foto {\n\t\tmax_size 6MB\n\t}",
		"@klein not {\n\t\tmethod POST\n\t\theader Content-Type multipart/form-data*\n\t}\n\trequest_body @klein {\n\t\tmax_size 32KB\n\t}"} {
		if !strings.Contains(k, want) {
			t.Errorf("kontakt site lacks %q:\n%s", want, k)
		}
	}
	if strings.Count(k, "request_body") != 2 {
		t.Errorf("a body limit without its matcher:\n%s", k)
	}
	for _, not := range []string{"import authelia", "X-Homes-Proxy", "psec", "8650", "@lan"} {
		if strings.Contains(k, not) {
			t.Errorf("kontakt site has %q:\n%s", not, k)
		}
	}
	homes := st.Services["homes"].Contract.Routes[0].Host // wherever Homes lives (its host may move)
	h := site(homes + ".x.home")
	if strings.Count(h, "path ") != 1 || !strings.Contains(h, "@bypass path /napi/*") || !strings.Contains(h, "import authelia") || strings.Contains(h, "8652") {
		t.Errorf("homes site changed:\n%s", h)
	}
	access := out["authelia/laib-access.yml"]
	if !strings.Contains(access, "domain: kontakt.x.home\n      policy: bypass") {
		t.Errorf("Authelia has no bypass for kontakt.x.home:\n%s", access)
	}
	if n := strings.Count(access, "domain: "+homes+".x.home\n      policy: bypass\n"); n != 1 {
		t.Errorf("Homes' host has %d paths without login, want 1 (/napi/):\n%s", n, access)
	}
	if p := os.Getenv("LAIB_CADDYFILE_OUT"); p != "" { // checked by a real caddy with the rate limit, by hand
		os.WriteFile(p, []byte(caddy), 0o600)
	}
}
