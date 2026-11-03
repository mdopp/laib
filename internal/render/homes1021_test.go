package render

import (
	"strings"
	"testing"
)

// #1021, ADR 0031: Haus is renamed Homes. The old app keeps calling /napi/
// on haus.<domain> with its device token until every phone runs Homes, so the
// legacy host is a second route of the same service, not a redirect (OkHttp
// drops Authorization on a cross-host redirect): the same port, the /napi/
// bypass and the proxy header named for the service on both.
func TestHomesKeepsTheLegacyHausRouteForTheOldApp(t *testing.T) {
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
	for _, host := range []string{"homes.x.home", "haus.x.home"} {
		s := site(host)
		for _, want := range []string{"@bypass path /napi/*", "reverse_proxy 127.0.0.1:8650", "import authelia", `header_up X-Homes-Proxy "psec"`} {
			if !strings.Contains(s, want) {
				t.Errorf("%s lacks %q:\n%s", host, want, s)
			}
		}
	}
	if !strings.Contains(caddy, "x.home, www.x.home {\n\tredir https://homes.x.home{uri}") {
		t.Errorf("the domain and www. do not lead to homes.:\n%s", caddy)
	}
	if strings.Contains(caddy, "redir https://homes.x.home{uri}") && strings.Contains(caddy, "haus.x.home, ") {
		t.Errorf("haus. is a Caddy redirect: the old app would lose its device token")
	}
	if u := HomesURL(st); u != "https://homes.x.home/" {
		t.Errorf("the family's entry is %q, want homes.", u)
	}
	acc := out["authelia/laib-access.yml"]
	for _, host := range []string{"domain: homes.x.home", "domain: haus.x.home"} {
		if !strings.Contains(acc, host) {
			t.Errorf("Authelia has no rule for %s:\n%s", host, acc)
		}
	}
}
