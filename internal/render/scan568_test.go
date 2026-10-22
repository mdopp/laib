package render

import (
	"regexp"
	"strings"
	"testing"
)

// #568: the scan service tells box.yml's notify.person unless its values
// name someone (the NOTIFY_PERSON builtin), reaches the household model
// with the model builtins, and gets bote-notify's bearer as a podman
// secret: no token on argv or in a literal Environment= line.
func TestScanTellsTheNotifyPersonAndCarriesNoTokenInItsUnit(t *testing.T) {
	st := shippedBox(t)
	st.Box.Notify.Person = "mama"
	st.Box.Roles.Model = "llama"
	sec := map[string]string{"BOTE_NOTIFY_TOKEN": "t-notify", "MODEL_API_KEY": "k-model"}
	out, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	unit := out["quadlet/scan/scan.container"]
	if unit == "" {
		t.Fatal("no scan unit rendered")
	}
	env := func(name string) string {
		m := regexp.MustCompile(`(?m)^Environment=` + name + `=(.*)$`).FindStringSubmatch(unit)
		if m == nil {
			return "<unset>"
		}
		return m[1]
	}
	if v := env("NOTIFY_PERSON"); v != "mama" {
		t.Errorf("NOTIFY_PERSON = %q, want box.yml's notify.person", v)
	}
	if v := env("MODEL_LEASE"); v != "http://host.containers.internal:11435/api/model-lease" {
		t.Errorf("MODEL_LEASE = %q", v)
	}
	if strings.Contains(unit, "t-notify") || strings.Contains(unit, "k-model") {
		t.Errorf("a token stands in the unit itself:\n%s", unit)
	}
	if !regexp.MustCompile(`(?m)^Secret=[^,]+,type=env,target=NOTIFY_TOKEN$`).MatchString(unit) {
		t.Errorf("bote-notify's bearer does not arrive as a podman secret:\n%s", unit)
	}
	if strings.Contains(unit, "Exec=") {
		t.Errorf("the scan unit passes arguments: nothing may go on argv")
	}
}
