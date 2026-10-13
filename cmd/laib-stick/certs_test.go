package main

import (
	"bufio"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/stick"
)

// #116: without a DNS provider the stick set tls: internal on its own, so
// the documented trial (HTTP-01 forwarded by the Kraftwerk, #45) ended with
// Caddy's own CA, which phones and their widgets do not trust. Let's Encrypt
// over port 80 is the default; Caddy's own CA only when asked for.
func TestCertificatesDefaultToLetsEncryptOverPort80(t *testing.T) {
	cases := []struct {
		name, input string
		ask         bool
		flags       stick.Box
		tls, dns    string
	}{
		{name: "flags only, nothing given: HTTP-01", ask: false, tls: "", dns: ""},
		{name: "flags only, --tls internal", ask: false, flags: stick.Box{TLS: "internal"}, tls: "internal"},
		{name: "flags only, a DNS provider", ask: false, flags: stick.Box{DNSProvider: "desec", DNSToken: "tok"}, dns: "desec"},
		{name: "wizard, Enter: HTTP-01", ask: true, input: "\n", tls: "", dns: ""},
		{name: "wizard, 2: a DNS provider", ask: true, input: "2\ndesec\ntok\n", dns: "desec"},
		{name: "wizard, 3: Caddy's own CA", ask: true, input: "3\n", tls: "internal"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in = bufio.NewReader(strings.NewReader(c.input))
			b := c.flags
			if err := certificates(c.ask, &b); err != nil {
				t.Fatal(err)
			}
			if b.TLS != c.tls || b.DNSProvider != c.dns {
				t.Fatalf("tls %q dns %q, want tls %q dns %q", b.TLS, b.DNSProvider, c.tls, c.dns)
			}
		})
	}
}
