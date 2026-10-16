package render

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #251: Authelia's client defaults are two_factor and client_secret_basic.
// ServiceBay registered every family client one_factor with the method the
// app speaks (Home Assistant's auth_oidc posts its secret in the form body).
// Every client says both, and a laib.yml may name client_secret_post.
func TestOIDCClientsAreOneFactorWithTheirTokenMethod(t *testing.T) {
	st, err := state.Load(filepath.Join("..", "..", "testdata", "golden", "basic", "state"))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := Render(st, nil)
	c := out["authelia/laib-clients.yml"]
	for _, want := range []string{"authorization_policy: one_factor", "token_endpoint_auth_method: client_secret_basic"} {
		if !strings.Contains(c, "\n        "+want+"\n") {
			t.Errorf("no %q in\n%s", want, c)
		}
	}
	for _, s := range st.Services {
		if s.Contract.OIDC != nil {
			s.Contract.OIDC.TokenEndpointAuthMethod = "client_secret_post"
		}
	}
	out, _ = Render(st, nil)
	if c := out["authelia/laib-clients.yml"]; !strings.Contains(c, "token_endpoint_auth_method: client_secret_post\n") || strings.Contains(c, "client_secret_basic") {
		t.Errorf("the declared method is not the client's:\n%s", c)
	}
}
