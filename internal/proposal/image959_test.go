package proposal

import (
	"strings"
	"testing"
)

// #959: Freigabe #297 read „Home Assistant bekommt eine neue Version:
// 407b89cd…98f → 23f4dfbb…972e133“ — for a digest pin the „tag“ was the
// digest. A card never shows a digest: it says the version the registry's
// labels name, or that the tag has a new build.
func TestAnImageCardNeverShowsADigest(t *testing.T) {
	const ha = "ghcr.io/home-assistant/home-assistant:stable@sha256:"
	old, new := ha+"407b89cd0e2f98f", ha+"23f4dfbb5c1972e133"
	for _, c := range []struct {
		name, oldV, newV, old, new, want string
	}{
		{"two versions", "2026.10.3", "2026.10.4", old, new, "Home Assistant bekommt ein Update: 2026.10.3 → 2026.10.4"},
		{"no labels", "", "", old, new, "Home Assistant bekommt ein Update (neuer Stand von „stable“)"},
		{"only the new one", "", "2026.10.4", old, new, "Home Assistant bekommt ein Update auf 2026.10.4"},
		{"a rebuild", "2026.10.3", "2026.10.3", old, new, "Home Assistant bekommt einen neu gebauten Stand von 2026.10.3"},
		{"tag to tag", "", "", "docker.io/traefik/whoami:v1.10", "docker.io/traefik/whoami:v1.11", "bekommt eine neue Version: v1.10 → v1.11"},
		{"pinned tags that move", "", "", "x.io/a/b:1.2@sha256:aa11", "x.io/a/b:1.3@sha256:bb22", "bekommt eine neue Version: 1.2 → 1.3"},
	} {
		svc := "home-assistant"
		if strings.Contains(c.old, "whoami") {
			svc = "whoami"
		}
		p := Proposal{ID: 297, Actor: "ops", Reason: "neues Image", Kind: "values", Class: "values", Tier: "apply", Files: []string{"services/" + svc + "/values.yml"},
			Changes: []Change{{Op: "~", Service: svc, Key: "HA_IMAGE", Old: c.old, New: c.new, OldVersion: c.oldV, NewVersion: c.newV}}}
		d := Card(&p, "box.test")
		if !strings.Contains(d.Summary, c.want) {
			t.Errorf("%s: summary %q, want %q", c.name, d.Summary, c.want)
		}
		if text := cardText(d); strings.Contains(text, "sha256") || strings.Contains(text, "407b") || strings.Contains(text, "aa11") {
			t.Errorf("%s: the card shows a digest:\n%s", c.name, text)
		}
		if !strings.Contains(d.Details, c.new) {
			t.Errorf("%s: the exact image belongs under Details: %q", c.name, d.Details)
		}
	}
}
