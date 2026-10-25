package render

import (
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #673: Laib's own containers took the host's zone with Timezone=local,
// which podman resolves through /etc/localtime. Fedora CoreOS ships none
// (UTC by absence), so podman refused to start laib-haushalt on every fresh
// box: "restart laib-haushalt.service exit status 1". They name box.yml's
// timezone instead, which podman finds in the host's zoneinfo; without one
// they run in UTC, as the host does.
func TestOwnContainersNameTheBoxTimezoneNotTheHostsLocaltime(t *testing.T) {
	st, err := state.Load("../../testdata/golden/nuc/state")
	if err != nil {
		t.Fatal(err)
	}
	sec := map[string]string{HaushaltToken: "t", HaushaltKey: "k", OperatorToken: "o", NotifyToken: "n"}
	for _, tz := range []string{"Europe/Berlin", ""} {
		st.Box.Timezone = tz
		out, err := Render(st, sec)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range Own {
			unit, ok := out["quadlet/"+name+"/"+name+".container"]
			if !ok {
				continue
			}
			if strings.Contains(unit, "Timezone=local") {
				t.Errorf("%s needs the host's /etc/localtime:\n%s", name, unit)
			}
			has := strings.Contains(unit, "\nTimezone=")
			if tz != "" && !strings.Contains(unit, "\nTimezone="+tz+"\n") || tz == "" && has {
				t.Errorf("%s with box timezone %q:\n%s", name, tz, unit)
			}
		}
		if _, ok := out["quadlet/laib-haushalt/laib-haushalt.container"]; !ok {
			t.Fatal("no laib-haushalt rendered")
		}
	}
}
