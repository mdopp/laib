package render

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mdopp/laib/internal/state"
)

// The network map (#439): what reaches what on this box, computed with the
// same rules that render the Caddyfile, Authelia's access control and the
// nftables drop, never guessed from templates and never probed.

type NetName struct {
	Name        string   `json:"name"`
	Reach       string   `json:"reach"`       // nur LAN | LAN+Internet
	ForwardAuth bool     `json:"forwardAuth"` // Caddy asks Authelia first
	Policy      string   `json:"policy"`      // Authelia's: bypass | one_factor | two_factor
	Subject     string   `json:"subject,omitempty"`
	Upstream    string   `json:"upstream"` // "<service> 127.0.0.1:<port>", or "→ <name>" for a redirect
	Bypass      []string `json:"bypass,omitempty"`
}

type NetPort struct {
	Service string `json:"service"`
	Port    int    `json:"port"`
	Reach   string `json:"reach"` // LAN | nur Box und Pods (lan: false: dropped on en*, eth*, wl*)
}

type NetLink struct {
	From string `json:"from"`
	To   string `json:"to"`
	Port int    `json:"port"`
}

type NetMap struct {
	Names       []NetName `json:"names"`
	Ports       []NetPort `json:"ports"`
	HostNetwork []string  `json:"hostNetwork"`
	Links       []NetLink `json:"links"` // a pod to another service over host.containers.internal
}

// forwardAuth: Caddy imports Authelia for the route. The SSO service's other
// routes too (ldap., owner 2026-10-26); only the portal itself never: a
// portal behind itself is a loop.
func forwardAuth(st *state.State, svc string, r state.Route) bool {
	return r.Auth != "none" && (svc != st.Box.Roles.SSO || r.Host != portalHost(st))
}

// rule: Authelia's rule for a name, the one without a path (#487)
func rule(rules []accessRule, domain string) accessRule {
	for _, r := range rules {
		if r.Domain == domain && r.Resource == "" {
			return r
		}
	}
	return accessRule{}
}

func policyOf(r state.Route) string {
	if r.Auth == "none" {
		return "bypass"
	}
	return r.Auth
}

// subjects: family and admins, never just any LLDAP account; admins reach what the family reaches
func subjects(r state.Route) []string {
	if r.Group == "admins" {
		return []string{"admins"}
	}
	return []string{cmp.Or(r.Group, "family"), "admins"}
}

var hciPort = regexp.MustCompile(`host\.containers\.internal:(\d+)`)

func Net(st *state.State) NetMap {
	var n NetMap
	reach := map[string]string{"internal": "nur LAN"}
	owner := map[int]string{st.Box.Laib.Port: "laib"}
	rules, dropped := accessRules(st), droppedPorts(st)
	if st.Box.Roles.Proxy != "" {
		r := rule(rules, fqdn(st, st.Box.Laib.Host))
		n.Names = append(n.Names, NetName{Name: r.Domain, Reach: "nur LAN", ForwardAuth: st.Box.Roles.SSO != "", Policy: r.Policy, Subject: strings.Join(r.Subjects, ", "), Upstream: fmt.Sprintf("laib 127.0.0.1:%d", st.Box.Laib.Port)})
	}
	// Laib's own port: loopback, or for the pods too while nftables keeps the LAN out
	n.Ports = append(n.Ports, NetPort{Service: "laib", Port: st.Box.Laib.Port, Reach: map[bool]string{true: "nur Box und Pods", false: "nur Box"}[slices.Contains(dropped, st.Box.Laib.Port)]})
	for _, name := range st.ServiceNames() {
		c := st.Services[name].Contract
		for _, r := range c.Routes {
			owner[r.Port] = name
			a := rule(rules, fqdn(st, r.Host))
			nn := NetName{Name: a.Domain, Reach: cmp.Or(reach[r.Exposure], "LAN+Internet"), ForwardAuth: forwardAuth(st, name, r), Policy: a.Policy, Subject: strings.Join(a.Subjects, ", "), Upstream: fmt.Sprintf("%s 127.0.0.1:%d", name, r.Port), Bypass: r.Bypass}
			n.Names = append(n.Names, nn)
			for _, h := range r.RedirectFrom {
				n.Names = append(n.Names, NetName{Name: strings.TrimPrefix(fqdn(st, h), "@."), Reach: "LAN+Internet", Policy: "bypass", Upstream: "→ " + fqdn(st, r.Host)})
			}
		}
		for _, p := range c.Ports {
			owner[p.Port] = name
			n.Ports = append(n.Ports, NetPort{Service: name, Port: p.Port, Reach: map[bool]string{false: "LAN", true: "nur Box und Pods"}[slices.Contains(dropped, p.Port)]})
		}
		if c.HostNetwork {
			n.HostNetwork = append(n.HostNetwork, name)
		}
	}
	for _, name := range st.ServiceNames() {
		seen := map[int]bool{}
		for _, file := range slices.Sorted(maps.Keys(st.Services[name].Units)) {
			for _, m := range hciPort.FindAllStringSubmatch(st.Services[name].Units[file], -1) {
				port, _ := strconv.Atoi(m[1])
				if to := owner[port]; to != "" && to != name && !seen[port] {
					seen[port] = true
					n.Links = append(n.Links, NetLink{From: name, To: to, Port: port})
				}
			}
		}
	}
	return n
}

// Mermaid is the map as a flowchart: Internet and LAN to Caddy, Caddy to
// Authelia or straight on, the pods' links dotted.
func (n NetMap) Mermaid(st *state.State) string {
	id := func(s string) string { return strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(s) }
	var b strings.Builder
	b.WriteString("flowchart LR\n  internet((Internet)) -->|80, 443| fritz[Fritz!Box]\n  fritz --> caddy[Caddy]\n")
	fmt.Fprintf(&b, "  lan((LAN)) -->|*.%s| rewrite[\"AdGuard: → %s\"]\n  rewrite --> caddy\n", st.Box.Domain, cmp.Or(st.Box.LanIP, "?"))
	if st.Box.Roles.SSO != "" {
		b.WriteString("  authelia{Authelia}\n")
	}
	for _, r := range n.Names {
		to, _, _ := strings.Cut(r.Upstream, " ")
		label := strings.TrimSuffix(r.Name, "."+st.Box.Domain)
		if r.Reach == "nur LAN" {
			label += ", nur LAN"
		}
		switch {
		case to == "→":
			fmt.Fprintf(&b, "  caddy -->|%s| %s[\"%s\"]\n", label, id("redir "+r.Name), r.Upstream)
		case r.ForwardAuth:
			fmt.Fprintf(&b, "  caddy -->|%s| authelia -->|%s| %s\n", label, r.Policy, id(to))
		default:
			fmt.Fprintf(&b, "  caddy -->|%s| %s\n", label, id(to))
		}
	}
	for _, p := range n.Ports {
		if p.Reach == "LAN" {
			fmt.Fprintf(&b, "  lan -->|%d| %s\n", p.Port, id(p.Service))
		}
	}
	for _, l := range n.Links {
		fmt.Fprintf(&b, "  %s -.->|%d| %s\n", id(l.From), l.Port, id(l.To))
	}
	return b.String()
}
