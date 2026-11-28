package diag

import (
	"errors"
	"testing"
)

// Field failure 2026-11-28: a guest's link to kontakt.<domain> on the
// household's Wi-Fi answered the LAN address, and Chrome refused it. The
// probe compares the box's AdGuard with a public resolver, then dials the
// public address through the router's NAT loopback.
// Ablauf L-66
func TestGuestFinding(t *testing.T) {
	const n, lan = "kontakt.dopp.cloud", "192.168.178.100"
	pub := []string{"203.0.113.7"}
	ok := func(string) error { return nil }
	if f := guestFinding(n, lan, pub, nil, pub, nil, ok); f != nil {
		t.Fatalf("the public answer at home and a reachable address is no finding: %+v", f)
	}
	if f := guestFinding(n, lan, []string{lan}, nil, pub, nil, ok); f == nil || f.Unmeasured {
		t.Fatalf("the LAN address at home is a finding: %+v", f)
	}
	if f := guestFinding(n, lan, []string{"198.51.100.1"}, nil, pub, nil, ok); f == nil {
		t.Fatal("an answer at home that is none of the public ones is a finding")
	}
	var dialed string
	f := guestFinding(n, lan, append([]string{"2001:db8::7"}, pub...), nil, append([]string{"2001:db8::7"}, pub...), nil, func(a string) error { dialed = a; return errors.New("i/o timeout") })
	if f == nil || dialed != pub[0] {
		t.Fatalf("no NAT loopback to the public IPv4 is a finding: %+v, dialed %q", f, dialed)
	}
	if f := guestFinding(n, lan, nil, errors.New("refused"), pub, nil, ok); f == nil || !f.Unmeasured {
		t.Fatalf("AdGuard silent is unmeasured: %+v", f)
	}
	if f := guestFinding(n, lan, pub, nil, nil, errors.New("timeout"), ok); f == nil || !f.Unmeasured {
		t.Fatalf("the public resolver silent is unmeasured: %+v", f)
	}
}
