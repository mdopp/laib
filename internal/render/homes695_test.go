package render

import (
	"regexp"
	"testing"
)

// #695: Homes checks a wished song against the household's music in
// Jellyfin, read only. It gets Jellyfin as its pod reaches it and the token
// media's post-deploy mints for Jellyfin's user homes (music only, no
// administrator), declared on both sides as HOMES_ABS_TOKEN is (#676).
func TestHomesGetsJellyfinsMusic(t *testing.T) {
	st := shippedBox(t)
	for _, svc := range []string{"media", "homes"} {
		found := false
		for _, s := range st.Services[svc].Contract.Secrets {
			found = found || s.Name == "HOMES_JELLYFIN_TOKEN" && s.External && s.Optional
		}
		if !found {
			t.Errorf("%s does not declare HOMES_JELLYFIN_TOKEN as external and optional", svc)
		}
	}
	sec := map[string]string{"HOMES_JELLYFIN_TOKEN": "k-homes-jf", HaushaltToken: "t", HaushaltKey: "k"}
	out, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	out = asContainerSees(st, sec, out)
	env := func(name string) string {
		m := regexp.MustCompile(`(?m)^Environment="?` + name + `=(.*?)"?$`).FindStringSubmatch(out["quadlet/homes/homes.container"])
		if m == nil {
			return ""
		}
		return m[1]
	}
	if v := env("HOMES_JELLYFIN_URL"); v != "http://host.containers.internal:8096" {
		t.Errorf("HOMES_JELLYFIN_URL = %q, want Jellyfin as the homes pod reaches it", v)
	}
	if v := env("HOMES_JELLYFIN_TOKEN"); v != "k-homes-jf" {
		t.Errorf("HOMES_JELLYFIN_TOKEN = %q, want the token media minted", v)
	}
}
