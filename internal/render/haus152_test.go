package render

import (
	"regexp"
	"testing"
)

// haus#152: Haus's Gesehen & Gehört reads what each person watched in
// Jellyfin with the API key media's post-deploy mints ("Haus (Gesehen)"),
// declared on both sides as external and optional, and rendered into the
// haus container as HAUS_JELLYFIN_KEY.
func TestHausGetsJellyfinsKeyForGesehen(t *testing.T) {
	st := shippedBox(t)
	for _, svc := range []string{"media", "haus"} {
		found := false
		for _, s := range st.Services[svc].Contract.Secrets {
			found = found || s.Name == "HAUS_JELLYFIN_KEY" && s.External && s.Optional
		}
		if !found {
			t.Errorf("%s does not declare HAUS_JELLYFIN_KEY as external and optional", svc)
		}
	}
	sec := map[string]string{"HAUS_JELLYFIN_KEY": "k-haus-gesehen", HaushaltToken: "t", HaushaltKey: "k"}
	out, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	out = asContainerSees(st, sec, out)
	m := regexp.MustCompile(`(?m)^Environment="?HAUS_JELLYFIN_KEY=(.*?)"?$`).FindStringSubmatch(out["quadlet/haus/haus.container"])
	if m == nil || m[1] != "k-haus-gesehen" {
		t.Errorf("HAUS_JELLYFIN_KEY = %v, want the key media minted", m)
	}
}
