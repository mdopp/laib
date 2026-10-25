package render

import (
	"regexp"
	"strings"
	"testing"
)

// #676 (haus#123): Haus subscribes podcasts by link and tells only the
// subscriber of a new episode. Haus gets Audiobookshelf as its pod reaches
// it and the key audiobookshelf's post-deploy mints for ABS's user haus,
// declared on both sides as music-assistant's ABS_MA_TOKEN is (#646).
// The message goes through laib tell, served by laib-haushalt: with a Bote
// on the box its unit carries bote-notify's address and bearer, as
// laib-notify's does; without one it carries neither.
func TestHausGetsAudiobookshelfAndHaushaltTheBote(t *testing.T) {
	st := shippedBox(t)
	for _, svc := range []string{"audiobookshelf", "haus"} {
		found := false
		for _, s := range st.Services[svc].Contract.Secrets {
			found = found || s.Name == "HAUS_ABS_TOKEN" && s.External && s.Optional
		}
		if !found {
			t.Errorf("%s does not declare HAUS_ABS_TOKEN as external and optional", svc)
		}
	}
	sec := map[string]string{"HAUS_ABS_TOKEN": "k-haus-abs", "BOTE_NOTIFY_TOKEN": "k-notify", HaushaltToken: "t", HaushaltKey: "k"}
	out, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	out = asContainerSees(st, sec, out)
	env := func(unit, name string) (string, bool) {
		m := regexp.MustCompile(`(?m)^Environment="?` + name + `=(.*?)"?$`).FindStringSubmatch(out[unit])
		if m == nil {
			return "", false
		}
		return m[1], true
	}
	if v, _ := env("quadlet/haus/haus.container", "HAUS_ABS_URL"); v != "http://host.containers.internal:13378" {
		t.Errorf("HAUS_ABS_URL = %q, want Audiobookshelf as the haus pod reaches it", v)
	}
	if v, _ := env("quadlet/haus/haus.container", "HAUS_ABS_TOKEN"); v != "k-haus-abs" {
		t.Errorf("HAUS_ABS_TOKEN = %q, want the key audiobookshelf minted", v)
	}
	h := "quadlet/laib-haushalt/laib-haushalt.container"
	if v, _ := env(h, "BOTE_NOTIFY_URL"); v != "http://127.0.0.1:8645" {
		t.Errorf("laib-haushalt: BOTE_NOTIFY_URL = %q, want bote-notify on the host port:\n%s", v, out[h])
	}
	if v, _ := env(h, "BOTE_NOTIFY_TOKEN"); v != "k-notify" {
		t.Errorf("laib-haushalt: BOTE_NOTIFY_TOKEN = %q, want bote-notify's bearer as a podman secret:\n%s", v, out[h])
	}

	delete(st.Services, "bote")
	out, err = Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out[h], "BOTE_NOTIFY") {
		t.Errorf("without a Bote laib-haushalt names bote-notify:\n%s", out[h])
	}
}
