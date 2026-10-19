package state

import (
	"strings"
	"testing"
)

// #378: a challenge alias without a DNS provider does nothing, silently.
func TestChallengeAliasNeedsAProvider(t *testing.T) {
	dir := minimal(t)
	write(t, dir, "box.yml", "domain: x.home\ndataDir: /d\nroles: {sso: auth}\ndns:\n  challengeAlias: _acme-challenge.x.dedyn.io\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "dns.provider") {
		t.Fatalf("want an error naming dns.provider, got %v", err)
	}
	write(t, dir, "box.yml", "domain: x.home\ndataDir: /d\nroles: {sso: auth}\ndns:\n  provider: desec\n  secretRef: DNS_API_TOKEN\n  challengeAlias: _acme-challenge.x.dedyn.io\n")
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Box.DNS.ChallengeAlias != "_acme-challenge.x.dedyn.io" {
		t.Fatalf("alias %q", st.Box.DNS.ChallengeAlias)
	}
}
