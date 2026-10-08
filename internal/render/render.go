// Package render turns a loaded state into the files that go on the box.
// Pure: state in, files out. Golden-tested (docs/TESTING.md).
package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mdopp/laib/internal/state"
)

// Output maps a relative path to file content. Paths:
//
//	quadlet/<service>/<file>      → ~/.config/containers/systemd/<service>/<file>
//	caddy/Caddyfile               → /mnt/data/laib/render/caddy/Caddyfile
//	authelia/laib-access.yml      → /mnt/data/laib/render/authelia/…
//	authelia/laib-clients.yml
//	adguard/rewrites.json
//	nftables/laib.nft
type Output map[string]string

// Placeholder is what a secret renders as when no secret values are given
// (plan mode) and what Redact turns a real value back into.
func Placeholder(name string) string { return "${secret:" + name + "}" }

// Render produces every file. secrets may be nil: then secret references
// render as placeholders, which is what `laib plan` shows and what the repo's
// rendered/ copy stores. Apply passes the real values.
func Render(st *state.State, secrets map[string]string) (Output, error) {
	out := Output{}
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		vars, err := variables(st, svc, secrets)
		if err != nil {
			return nil, err
		}
		for file, content := range svc.Units {
			out["quadlet/"+name+"/"+file] = substitute(content, vars)
		}
	}
	if st.Box.Roles.Proxy != "" {
		out["caddy/Caddyfile"] = caddyfile(st)
	}
	if st.Box.Roles.SSO != "" {
		out["authelia/laib-access.yml"] = autheliaAccess(st)
		out["authelia/laib-clients.yml"] = autheliaClients(st, secrets)
	}
	if st.Box.Roles.DNS != "" && st.Box.LanIP != "" {
		out["adguard/rewrites.json"] = adguardRewrites(st)
	}
	if nft := nftables(st); nft != "" {
		out["nftables/laib.nft"] = nft
	}
	return out, nil
}

// Redact replaces every secret value in the output with its placeholder, so
// a diff or a repo copy never carries a secret.
func Redact(out Output, secrets map[string]string) Output {
	if len(secrets) == 0 {
		return out
	}
	red := Output{}
	for p, c := range out {
		for name, val := range secrets {
			if val != "" {
				c = strings.ReplaceAll(c, val, Placeholder(name))
			}
		}
		red[p] = c
	}
	return red
}

// SecretNames lists every secret a state declares, sorted.
func SecretNames(st *state.State) []state.Secret {
	var out []state.Secret
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		out = append(out, svc.Contract.Secrets...)
		if svc.Contract.OIDC != nil {
			out = append(out, state.Secret{Name: oidcSecretName(svc.Contract.OIDC.ClientID)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func oidcSecretName(clientID string) string {
	return "OIDC_" + strings.ToUpper(strings.ReplaceAll(clientID, "-", "_")) + "_SECRET"
}

func variables(st *state.State, svc *state.Service, secrets map[string]string) (map[string]string, error) {
	vars := map[string]string{
		"DOMAIN":   st.Box.Domain,
		"DATA_DIR": st.Box.DataDir,
		"TZ":       st.Box.Timezone,
		"SERVICE":  svc.Name,
		"LAN_IP":   st.Box.LanIP,
	}
	for k, v := range svc.Values {
		if v.Secret != "" {
			vars[k] = secretValue(secrets, v.Secret)
			continue
		}
		vars[k] = v.Literal
	}
	return vars, nil
}

func secretValue(secrets map[string]string, name string) string {
	if v, ok := secrets[name]; ok && v != "" {
		return v
	}
	return Placeholder(name)
}

func substitute(content string, vars map[string]string) string {
	for _, name := range state.Placeholders(content) {
		if v, ok := vars[name]; ok {
			content = strings.ReplaceAll(content, "${"+name+"}", v)
		}
	}
	return content
}

func fqdn(st *state.State, host string) string { return host + "." + st.Box.Domain }

// ssoPort is the Authelia listener Caddy forwards auth to: the sso service's
// first route port.
func ssoPort(st *state.State) int {
	if svc, ok := st.Services[st.Box.Roles.SSO]; ok && len(svc.Contract.Routes) > 0 {
		return svc.Contract.Routes[0].Port
	}
	return 9091
}

func caddyfile(st *state.State) string {
	var b strings.Builder
	b.WriteString("# Rendered by laib. Do not edit; change services/*/laib.yml and run laib apply.\n")
	b.WriteString("{\n\temail admin@" + st.Box.Domain + "\n")
	if st.Box.DNS.Provider != "" {
		fmt.Fprintf(&b, "\tacme_dns %s {env.LAIB_DNS_TOKEN}\n", st.Box.DNS.Provider)
	}
	b.WriteString("}\n\n")
	if st.Box.Roles.SSO != "" {
		fmt.Fprintf(&b, "(authelia) {\n\tforward_auth 127.0.0.1:%d {\n\t\turi /api/authz/forward-auth\n\t\tcopy_headers Remote-User Remote-Groups Remote-Email Remote-Name\n\t}\n}\n\n", ssoPort(st))
	}
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		for _, r := range svc.Contract.Routes {
			fmt.Fprintf(&b, "%s {\n", fqdn(st, r.Host))
			indent := "\t"
			if r.Exposure == "internal" {
				b.WriteString("\t@lan remote_ip private_ranges\n\thandle @lan {\n")
				indent = "\t\t"
			}
			if r.Auth != "none" && name != st.Box.Roles.SSO {
				b.WriteString(indent + "import authelia\n")
			}
			if r.Caddy != "" {
				for _, line := range strings.Split(strings.TrimRight(r.Caddy, "\n"), "\n") {
					b.WriteString(indent + line + "\n")
				}
			}
			fmt.Fprintf(&b, "%sreverse_proxy 127.0.0.1:%d\n", indent, r.Port)
			if r.Exposure == "internal" {
				b.WriteString("\t}\n\trespond 403\n")
			}
			b.WriteString("}\n\n")
		}
	}
	return b.String()
}

func autheliaAccess(st *state.State) string {
	var b strings.Builder
	b.WriteString("# Rendered by laib: one rule per route. The apex deny and the wildcard\n# default live in the auth service's own config.\naccess_control:\n  rules:\n")
	for _, name := range st.ServiceNames() {
		for _, r := range st.Services[name].Contract.Routes {
			policy := r.Auth
			if policy == "none" {
				policy = "bypass"
			}
			fmt.Fprintf(&b, "    - domain: %s\n      policy: %s\n", fqdn(st, r.Host), policy)
			if r.Group != "" {
				fmt.Fprintf(&b, "      subject: \"group:%s\"\n", r.Group)
			}
		}
	}
	return b.String()
}

func autheliaClients(st *state.State, secrets map[string]string) string {
	var b strings.Builder
	b.WriteString("# Rendered by laib: one OIDC client per service that declares oidc.\nidentity_providers:\n  oidc:\n    clients:\n")
	for _, name := range st.ServiceNames() {
		o := st.Services[name].Contract.OIDC
		if o == nil {
			continue
		}
		fmt.Fprintf(&b, "      - client_id: %s\n        client_name: %s\n        client_secret: %q\n        redirect_uris:\n", o.ClientID, name, secretValue(secrets, oidcSecretName(o.ClientID)))
		for _, u := range o.RedirectURIs {
			fmt.Fprintf(&b, "          - %s\n", strings.ReplaceAll(u, "${DOMAIN}", st.Box.Domain))
		}
		scopes := o.Scopes
		if len(scopes) == 0 {
			scopes = []string{"openid", "profile", "email", "groups"}
		}
		b.WriteString("        scopes: [" + strings.Join(scopes, ", ") + "]\n")
	}
	return b.String()
}

func adguardRewrites(st *state.State) string {
	type rw struct {
		Domain string `json:"domain"`
		Answer string `json:"answer"`
	}
	rws := []rw{{Domain: "*." + st.Box.Domain, Answer: st.Box.LanIP}, {Domain: st.Box.Domain, Answer: st.Box.LanIP}}
	b, _ := json.MarshalIndent(rws, "", "  ")
	return string(b) + "\n"
}

func nftables(st *state.State) string {
	var ports []string
	for _, name := range st.ServiceNames() {
		for _, p := range st.Services[name].Contract.Ports {
			if !p.LAN {
				ports = append(ports, fmt.Sprint(p.Port))
			}
		}
	}
	if len(ports) == 0 {
		return ""
	}
	return "# Rendered by laib: ports declared lan: false are dropped on physical interfaces.\n" +
		"table inet laib {\n\tchain input {\n\t\ttype filter hook input priority -10; policy accept;\n" +
		"\t\tiifname != \"lo\" tcp dport { " + strings.Join(ports, ", ") + " } drop\n\t}\n}\n"
}
