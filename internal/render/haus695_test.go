package render

import (
	"regexp"
	"testing"
)

// #695: Haus checks a wished song against the household's music in
// Jellyfin, read only. It gets Jellyfin as its pod reaches it and the token
// media's post-deploy mints for Jellyfin's user haus (music only, no
// administrator), declared on both sides as HAUS_ABS_TOKEN is (#676).
func TestHausGetsJellyfinsMusic(t *testing.T) {
	st := shippedBox(t)
	for _, svc := range []string{"media", "haus"} {
		found := false
		for _, s := range st.Services[svc].Contract.Secrets {
			found = found || s.Name == "HAUS_JELLYFIN_TOKEN" && s.External && s.Optional
		}
		if !found {
			t.Errorf("%s does not declare HAUS_JELLYFIN_TOKEN as external and optional", svc)
		}
	}
	sec := map[string]string{"HAUS_JELLYFIN_TOKEN": "k-haus-jf", HaushaltToken: "t", HaushaltKey: "k"}
	out, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	out = asContainerSees(st, sec, out)
	env := func(name string) string {
		m := regexp.MustCompile(`(?m)^Environment="?` + name + `=(.*?)"?$`).FindStringSubmatch(out["quadlet/haus/haus.container"])
		if m == nil {
			return ""
		}
		return m[1]
	}
	if v := env("HAUS_JELLYFIN_URL"); v != "http://host.containers.internal:8096" {
		t.Errorf("HAUS_JELLYFIN_URL = %q, want Jellyfin as the haus pod reaches it", v)
	}
	if v := env("HAUS_JELLYFIN_TOKEN"); v != "k-haus-jf" {
		t.Errorf("HAUS_JELLYFIN_TOKEN = %q, want the token media minted", v)
	}
}
