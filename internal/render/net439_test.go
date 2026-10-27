package render

import (
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #439: asked how the network looks, an agent guessed from templates and got
// it wrong (Authelia in front of everything, ports and names missing). The
// map is computed with the rules that render Caddy, Authelia and nftables,
// from the state: names with their reach, forward-auth and policy, the
// redirects, the direct ports, the host network, the pods' links.
func TestTheNetworkMapFollowsTheRenderedRules(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	st.Services["whoami"].Units["whoami.container"] += "Environment=LDAP=ldap://host.containers.internal:3890\n"
	st.Services["auth"].Contract.Routes[0].LoginProbe = "/api/user/info" // #774: laib-app check reads it here
	n := Net(st)
	byName := map[string]NetName{}
	for _, r := range n.Names {
		byName[r.Name] = r
	}
	if r := byName["whoami.example.home"]; r.Reach != "nur LAN" || !r.ForwardAuth || r.Policy != "one_factor" || r.Subject != "family, admins" || r.Upstream != "whoami 127.0.0.1:8080" {
		t.Fatalf("whoami: %+v", r)
	}
	if r := byName["auth.example.home"]; r.Reach != "LAN+Internet" || r.ForwardAuth || r.Policy != "bypass" {
		t.Fatalf("the portal itself is not behind forward-auth: %+v", r)
	}
	if r := byName["auth.example.home"]; r.LoginProbe != "/api/user/info" {
		t.Fatalf("an auth: none route carries where its own login refuses: %+v", r)
	}
	if r := byName[st.Box.Laib.Host+".example.home"]; r.Reach != "nur LAN" || !r.ForwardAuth || !strings.Contains(r.Upstream, "laib") {
		t.Fatalf("laib's own page: %+v", r)
	}
	var ports []string
	for _, p := range n.Ports {
		ports = append(ports, p.Service+":"+p.Reach)
	}
	if !strings.Contains(strings.Join(ports, " "), "auth:nur Box und Pods") {
		t.Fatalf("lan: false is dropped on the physical interfaces: %v", ports)
	}
	if strings.Join(n.HostNetwork, ",") != "caddy" {
		t.Fatalf("host network: %v", n.HostNetwork)
	}
	if len(n.Links) != 1 || n.Links[0] != (NetLink{From: "whoami", To: "auth", Port: 3890}) {
		t.Fatalf("links: %+v", n.Links)
	}
	m := n.Mermaid(st)
	for _, want := range []string{"flowchart LR", "AdGuard", "Fritz!Box", "authelia{", "whoami -.->|3890| auth"} {
		if !strings.Contains(m, want) {
			t.Fatalf("mermaid lacks %q:\n%s", want, m)
		}
	}
}
