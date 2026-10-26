package render

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

func caddyfileFor(t *testing.T, dns state.DNS, tls string) string {
	t.Helper()
	st, err := state.Load(filepath.Join("..", "..", "testdata", "golden", "basic", "state"))
	if err != nil {
		t.Fatal(err)
	}
	st.Box.DNS, st.Box.TLS = dns, tls
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	return out["caddy/Caddyfile"]
}

// #378: with DNS-01, one wildcard certificate covers every route, so a new
// name needs no public record and no name lands in the CT logs. The
// challenge may be delegated (Strato -> deSEC): Caddy writes the TXT at the
// alias, and asks public resolvers, not the box's AdGuard, whose *.<domain>
// rewrite answers for _acme-challenge too.
func TestDNS01RendersOneWildcardAndTheChallengeAlias(t *testing.T) {
	cf := caddyfileFor(t, state.DNS{Provider: "desec", SecretRef: "DNS_API_TOKEN", ChallengeAlias: "_acme-challenge.laib-acme.dedyn.io"}, "")
	for _, want := range []string{
		"\tcert_issuer acme {\n\t\tdns desec {\n\t\t\ttoken {env.LAIB_DNS_TOKEN}\n\t\t}\n",
		"\t\tdns_challenge_override_domain _acme-challenge.laib-acme.dedyn.io\n",
		"\t\tresolvers 9.9.9.9 1.1.1.1\n",
		"\n*.example.home {\n\trequest_header -Remote-*\n\timport unknown\n}\n",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("missing %q in\n%s", want, cf)
		}
	}
	// the desec module takes no positional token: `acme_dns desec <token>` is an ArgErr
	if strings.Contains(cf, "acme_dns") {
		t.Errorf("acme_dns would replace the issuer's challenge config, the alias with it:\n%s", cf)
	}
}

func TestDNS01WithoutAliasKeepsThePositionalToken(t *testing.T) {
	cf := caddyfileFor(t, state.DNS{Provider: "cloudflare", SecretRef: "DNS_API_TOKEN"}, "")
	if !strings.Contains(cf, "\t\tdns cloudflare {env.LAIB_DNS_TOKEN}\n") || strings.Contains(cf, "dns_challenge_override_domain") {
		t.Errorf("cloudflare issuer:\n%s", cf)
	}
	if !strings.Contains(cf, "\n*.example.home {\n") {
		t.Errorf("no wildcard site:\n%s", cf)
	}
}

// HTTP-01 cannot get a wildcard, and local_certs excludes cert_issuer (Caddy:
// "global options are ambiguous"): neither renders the DNS issuer or the wildcard.
func TestNoWildcardWithoutDNS01(t *testing.T) {
	for _, c := range []struct {
		dns state.DNS
		tls string
	}{{state.DNS{}, ""}, {state.DNS{Provider: "desec", SecretRef: "DNS_API_TOKEN"}, "internal"}} {
		cf := caddyfileFor(t, c.dns, c.tls)
		if strings.Contains(cf, "*.example.home") || strings.Contains(cf, "cert_issuer") {
			t.Errorf("%+v %q:\n%s", c.dns, c.tls, cf)
		}
	}
}
