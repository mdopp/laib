package render

import (
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// Owner decision 2026-10-26: ldap. (LLDAP's admin UI) declares two_factor,
// so Caddy asks Authelia first, after the unchanged LAN check. Only the
// route that is Authelia's portal never forward-auths, also when its auth
// is not none: a portal behind itself is a loop.
func TestSSORoutesAreBehindAutheliaExceptThePortal(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	// the portal guard must not rest on auth: none
	st.Services[st.Box.Roles.SSO].Contract.Routes[0].Auth = "one_factor"
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cf := out["caddy/Caddyfile"]
	site := func(host string) string {
		_, rest, ok := strings.Cut(cf, "\n"+host+"."+st.Box.Domain+" {\n")
		if !ok {
			t.Fatalf("no site for %s:\n%s", host, cf)
		}
		body, _, _ := strings.Cut(rest, "\n}\n")
		return body
	}
	ldap := site("ldap")
	lan, auth := strings.Index(ldap, "handle @lan {"), strings.Index(ldap, "import authelia")
	if auth < 0 || lan < 0 || auth < lan || auth > strings.Index(ldap, "reverse_proxy 127.0.0.1:17170") {
		t.Errorf("ldap. is not behind Authelia inside the LAN check:\n%s", ldap)
	}
	if strings.Contains(site("auth"), "import authelia") {
		t.Errorf("the portal forward-auths to itself:\n%s", site("auth"))
	}
	byName := map[string]NetName{}
	for _, n := range Net(st).Names {
		byName[n.Name] = n
	}
	if r := byName["ldap."+st.Box.Domain]; !r.ForwardAuth || r.Policy != "two_factor" || r.Reach != "nur LAN" {
		t.Errorf("laib net for ldap.: %+v", r)
	}
	if r := byName["auth."+st.Box.Domain]; r.ForwardAuth {
		t.Errorf("laib net shows the portal behind forward-auth: %+v", r)
	}
}
