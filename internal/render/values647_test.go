package render

import (
	"strings"
	"testing"
)

// #647: services/haus/values.yml writes RADICALE_PUBLIC_URL: https://caldav.${DOMAIN}
// (the template's own values.example.yml does). The value was put into the unit
// and its ${DOMAIN} left as it was, so systemd made it empty and Haus showed
// https://caldav./ as the calendar address. A builtin inside a value renders
// as it does in a unit.
func TestABuiltinInAValueIsRendered(t *testing.T) {
	st := shippedBox(t)
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	haus := out["quadlet/haus/haus.container"]
	for _, want := range []string{"Environment=HAUS_RADICALE_PUBLIC_URL=https://caldav.x.home", "Environment=HAUS_HA_PUBLIC_URL=https://home.x.home",
		"Environment=HAUS_PAPERLESS_PUBLIC_URL=https://paperless.x.home"} {
		if !strings.Contains(haus, want) {
			t.Errorf("haus.container lacks %q", want)
		}
	}
	for file, content := range out {
		if strings.HasPrefix(file, "quadlet/") && strings.HasSuffix(file, ".container") && strings.Contains(content, "${DOMAIN}") {
			t.Errorf("%s still carries ${DOMAIN}, which systemd makes empty", file)
		}
	}
	if got := Values(st, st.Services["haus"], nil)["RADICALE_PUBLIC_URL"]; got != "https://caldav.x.home" {
		t.Errorf("a hook gets RADICALE_PUBLIC_URL=%q", got)
	}
}
