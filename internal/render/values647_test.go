package render

import (
	"strings"
	"testing"
)

// #647: services/homes/values.yml writes RADICALE_PUBLIC_URL: https://caldav.${DOMAIN}
// (the template's own values.example.yml does). The value was put into the unit
// and its ${DOMAIN} left as it was, so systemd made it empty and Homes showed
// https://caldav./ as the calendar address. A builtin inside a value renders
// as it does in a unit.
func TestABuiltinInAValueIsRendered(t *testing.T) {
	st := shippedBox(t)
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	homes := out["quadlet/homes/homes.container"]
	for _, want := range []string{"Environment=HOMES_RADICALE_PUBLIC_URL=https://caldav.x.home", "Environment=HOMES_HA_PUBLIC_URL=https://home.x.home",
		"Environment=HOMES_PAPERLESS_PUBLIC_URL=https://paperless.x.home"} {
		if !strings.Contains(homes, want) {
			t.Errorf("homes.container lacks %q", want)
		}
	}
	for file, content := range out {
		if strings.HasPrefix(file, "quadlet/") && strings.HasSuffix(file, ".container") && strings.Contains(content, "${DOMAIN}") {
			t.Errorf("%s still carries ${DOMAIN}, which systemd makes empty", file)
		}
	}
	if got := Values(st, st.Services["homes"], nil)["RADICALE_PUBLIC_URL"]; got != "https://caldav.x.home" {
		t.Errorf("a hook gets RADICALE_PUBLIC_URL=%q", got)
	}
}
