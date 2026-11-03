package render

import (
	"regexp"
	"testing"
)

// haus#152: Homes's Gesehen & Gehört reads what each person watched in
// Jellyfin with the API key media's post-deploy mints ("Haus (Gesehen)"),
// declared on both sides as external and optional, and rendered into the
// homes container as HOMES_JELLYFIN_KEY.
func TestHomesGetsJellyfinsKeyForGesehen(t *testing.T) {
	st := shippedBox(t)
	for _, svc := range []string{"media", "homes"} {
		found := false
		for _, s := range st.Services[svc].Contract.Secrets {
			found = found || s.Name == "HOMES_JELLYFIN_KEY" && s.External && s.Optional
		}
		if !found {
			t.Errorf("%s does not declare HOMES_JELLYFIN_KEY as external and optional", svc)
		}
	}
	sec := map[string]string{"HOMES_JELLYFIN_KEY": "k-homes-gesehen", HaushaltToken: "t", HaushaltKey: "k"}
	out, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	out = asContainerSees(st, sec, out)
	m := regexp.MustCompile(`(?m)^Environment="?HOMES_JELLYFIN_KEY=(.*?)"?$`).FindStringSubmatch(out["quadlet/homes/homes.container"])
	if m == nil || m[1] != "k-homes-gesehen" {
		t.Errorf("HOMES_JELLYFIN_KEY = %v, want the key media minted", m)
	}
}
