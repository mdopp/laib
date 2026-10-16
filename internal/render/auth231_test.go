package render

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mdopp/laib/internal/state"
)

// #231: Caddy strips client-sent Remote-* on every site, before forward_auth
// copies Authelia's own, so no app behind laib can be handed a forged
// Remote-User — also not one whose route skips Authelia.
func TestCaddyStripsClientRemoteHeadersOnEverySite(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cf := out["caddy/Caddyfile"]
	global := cf[:strings.Index(cf, "\n}\n")]
	// Caddy's default order runs request_header after forward_auth: the strip
	// would delete Authelia's headers instead of the client's
	if !strings.Contains(global, "\torder request_header first\n") {
		t.Errorf("the global block does not order request_header first:\n%s", global)
	}
	sites := 0
	for _, block := range strings.Split(cf, "\n}\n\n") {
		first, body, _ := strings.Cut(strings.TrimLeft(block, "\n"), " {\n")
		if !strings.HasSuffix(first, "."+st.Box.Domain) {
			continue
		}
		sites++
		if !strings.HasPrefix(body, "\trequest_header -Remote-*\n") {
			t.Errorf("%s does not strip client-sent Remote-* first:\n%s", first, body)
		}
	}
	if sites < 3 {
		t.Fatalf("found %d site blocks", sites)
	}
}

// #231: a route with auth: none fronts an app that logs in itself. None of
// that service's units may trust a proxy header (any client could send one
// past Caddy, any process on the box straight to the port) or switch the
// app's own login off.
func TestNoAuthRoutesFrontNoHeaderTrust(t *testing.T) {
	trust := []string{"REMOTE_USER", "Remote-User", "auth.method=proxy"}
	ymls, _ := filepath.Glob("../../templates/*/laib.yml")
	if len(ymls) < 10 {
		t.Fatalf("found %d templates", len(ymls))
	}
	for _, y := range ymls {
		b, _ := os.ReadFile(y)
		var c state.Contract
		if err := yaml.Unmarshal(b, &c); err != nil {
			t.Fatalf("%s: %v", y, err)
		}
		open := slices.ContainsFunc(c.Routes, func(r state.Route) bool { return r.Auth == "none" })
		units, _ := filepath.Glob(filepath.Join(filepath.Dir(y), "*.container"))
		for _, u := range units {
			ub, _ := os.ReadFile(u)
			if strings.Contains(string(ub), "FB_NOAUTH=true") {
				t.Errorf("%s turns File Browser's login off", u)
			}
			for _, m := range trust {
				if open && strings.Contains(string(ub), m) {
					t.Errorf("%s trusts %s, but %s has a route with auth: none", u, m, filepath.Base(filepath.Dir(y)))
				}
			}
		}
	}
	// files and paperless sit behind Authelia and take the user from it, as
	// in ServiceBay: one family login, no second password; one LLDAP account
	// is File Browser's admin from the start, as ServiceBay's post-deploy did
	for unit, wants := range map[string][]string{
		"file-share/file-share-filebrowser.container": {"--auth.method=proxy", "--auth.header=Remote-User", "users update ${FILEBROWSER_ADMIN_USER} --perm.admin"},
		"paperless/paperless.container":               {"PAPERLESS_ENABLE_HTTP_REMOTE_USER=true", "PAPERLESS_HTTP_REMOTE_USER_HEADER_NAME=HTTP_REMOTE_USER"},
	} {
		b, _ := os.ReadFile("../../templates/" + unit)
		for _, want := range wants {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s: no %s", unit, want)
			}
		}
	}
}

// #231: Z-Wave JS's server websocket (3000, 3001 on a box migrated from
// ServiceBay) has no login; on the host network laib.nft must drop it.
func TestZwaveServerPortIsNotOnTheLAN(t *testing.T) {
	b, _ := os.ReadFile("../../templates/home-assistant/laib.yml")
	var c state.Contract
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{3000, 3001} {
		if !slices.ContainsFunc(c.Ports, func(p state.Port) bool { return p.Port == want && !p.LAN }) {
			t.Errorf("home-assistant: port %d is not declared lan: false", want)
		}
	}
}
