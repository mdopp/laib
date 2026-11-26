package render

import (
	"regexp"
	"strings"
	"testing"
)

// homes ADR 0004 §3: the Werkstatt reaches Home Assistant's automations
// through laib automat, which laib-haushalt hands to the Bote's Automat on
// its bearer port, published on the host's loopback only. laib-haushalt's
// unit carries the address and the Automat's bearer,
// never an HA token; the Automat keeps its own in the pod. Without a Bote
// laib-haushalt names neither.
func TestHaushaltReachesTheAutomatOnItsBearerPort(t *testing.T) {
	st := shippedBox(t)
	sec := map[string]string{"BOTE_NOTIFY_TOKEN": "k-notify", "BOTE_AUTOMAT_TOKEN": "k-automat", "BOTE_AUTOMAT_HA_TOKEN": "k-ha-admin", HaushaltToken: "t", HaushaltKey: "k"}
	raw, err := Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	out := asContainerSees(st, sec, raw)
	h := "quadlet/laib-haushalt/laib-haushalt.container"
	env := func(unit, name string) string {
		m := regexp.MustCompile(`(?m)^Environment="?` + name + `=(.*?)"?$`).FindStringSubmatch(out[unit])
		if m == nil {
			return ""
		}
		return m[1]
	}
	if v := env(h, "BOTE_AUTOMAT_URL"); v != "http://127.0.0.1:8646" {
		t.Errorf("laib-haushalt: BOTE_AUTOMAT_URL = %q, want the Automat's bearer port:\n%s", v, out[h])
	}
	if v := env(h, "BOTE_AUTOMAT_TOKEN"); v != "k-automat" {
		t.Errorf("laib-haushalt: BOTE_AUTOMAT_TOKEN = %q, want the Automat's bearer:\n%s", v, out[h])
	}
	if strings.Contains(out[h], "k-ha-admin") {
		t.Errorf("laib-haushalt holds the Automat's HA token:\n%s", out[h])
	}
	var pod, automat string
	for k, v := range raw {
		if strings.HasSuffix(k, "/bote.pod") {
			pod = v
		}
		if strings.HasSuffix(k, "/bote-automat.container") {
			automat = v
		}
	}
	if !strings.Contains(pod, "PublishPort=127.0.0.1:8646:8646") {
		t.Errorf("the bote pod does not publish the Automat's bearer port on loopback only:\n%s", pod)
	}
	for _, w := range []string{"AUTOMAT_LISTEN=127.0.0.1:8643", "AUTOMAT_LAIB_LISTEN=0.0.0.0:8646", "AUTOMAT_TOKEN"} {
		if !strings.Contains(automat, w) {
			t.Errorf("bote-automat lacks %s:\n%s", w, automat)
		}
	}
	delete(st.Services, "bote")
	if raw, err = Render(st, sec); err != nil || strings.Contains(raw[h], "BOTE_AUTOMAT") {
		t.Errorf("without a Bote laib-haushalt names the Automat (%v):\n%s", err, raw[h])
	}
}
