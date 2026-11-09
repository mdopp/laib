package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// laib#1131, owner decision I: a route with outside: two_factor asks the
// second factor from outside the LAN and its own factor at home. Authelia
// takes the first rule that matches, so the LAN's rule comes first, and the
// LAN is what Caddy's @lan means.
func TestOutsideAsksTheSecondFactorBeyondTheLAN(t *testing.T) {
	st, err := state.Load("../../testdata/golden/nuc/state")
	if err != nil {
		t.Fatal(err)
	}
	var mine []accessRule
	for _, r := range accessRules(st) {
		if r.Domain == "whoami.nuc.home" && r.Resource == "" {
			mine = append(mine, r)
		}
	}
	if len(mine) != 2 || mine[0].Policy != "one_factor" || len(mine[0].Networks) == 0 || mine[1].Policy != "two_factor" || mine[1].Networks != nil {
		t.Fatalf("want the LAN's one_factor first, then two_factor for everyone else, got %+v", mine)
	}
	// Caddy's private_ranges, the @lan every internal route uses
	want := "192.168.0.0/16 172.16.0.0/12 10.0.0.0/8 127.0.0.0/8 fd00::/8 ::1/128"
	if got := strings.Join(mine[0].Networks, " "); got != want {
		t.Fatalf("the LAN for Authelia is %s, Caddy's private_ranges are %s", got, want)
	}
	for _, n := range Net(st).Names {
		if n.Name == "whoami.nuc.home" && n.Factor() != "one_factor, von außen two_factor" {
			t.Fatalf("laib net shows %q", n.Factor())
		}
	}
}

// outside: only where it makes sense: a public route that asks one factor at home
func TestOutsideOnlyOnAPublicOneFactorRoute(t *testing.T) {
	for _, bad := range []string{
		"    exposure: internal\n    auth: one_factor\n    outside: two_factor\n",
		"    exposure: public\n    auth: two_factor\n    outside: two_factor\n",
		"    exposure: public\n    auth: one_factor\n    outside: one_factor\n",
	} {
		dir := t.TempDir()
		copyTree(t, "../../testdata/golden/nuc/state", dir)
		f := filepath.Join(dir, "services", "whoami", "laib.yml")
		b, _ := os.ReadFile(f)
		s := strings.Replace(string(b), "    exposure: public       # like Homes on www: the phone reaches /napi/ from outside\n    outside: two_factor    # and from outside the LAN the second factor (laib#1131)\n    auth: one_factor\n", bad, 1)
		if s == string(b) {
			t.Fatal("the golden whoami route changed: adapt the test")
		}
		os.WriteFile(f, []byte(s), 0o644)
		if _, err := state.Load(dir); err == nil || !strings.Contains(err.Error(), "outside") {
			t.Errorf("%q loads: %v", bad, err)
		}
	}
}
