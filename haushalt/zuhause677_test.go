package haushalt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// proposals records what a verb hands laib as `laib propose …`.
type proposals struct {
	calls   [][]string
	ceiling string
	refuse  string
}

func (p *proposals) Propose(_ context.Context, ceiling string, args ...string) (int, string, error) {
	if p.refuse != "" {
		return 0, "", errors.New(p.refuse)
	}
	p.calls, p.ceiling = append(p.calls, args), ceiling
	return len(p.calls), "human", nil
}

// Aufgabe: no verb here adds a Box-Aufgabe (wish883_test's tasks does).
func (p *proposals) Aufgabe(context.Context, string, ...string) (int, error) {
	return 0, errors.New("unexpected laib aufgabe")
}

func run677(t *testing.T, laib Laib, z *Zuhause, args ...string) (map[string]any, int, string, string) {
	t.Helper()
	var out, errb, jr bytes.Buffer
	env := &Env{Call: Call{JSON: true, Actor: "haus", Ceiling: "propose", Zuhause: z}, Laib: laib, Out: &out, Err: &errb, Journal: &jr}
	code := Run(context.Background(), env, args)
	var reply map[string]any
	json.Unmarshal(out.Bytes(), &reply)
	return reply, code, errb.String(), jr.String()
}

// #677 (ADR 0026 §4): laib zuhause moved behind the verb door. show answers
// the block the door hands over; set proposes the whole block as the value
// at box.yml:zuhause through laib propose --path, as the haushalt hand
// under the verb's ceiling, so the kernel's loader judges it and a human
// approves it. Neither the reason nor the journal carries the address.
func TestZuhauseShowsTheBlockAndProposesTheWholeOne(t *testing.T) {
	lat, lon, elev := 52.1234, 8.0456, 63
	if got, code, _, _ := run677(t, nil, nil, "zuhause", "show"); code != exitOK || len(got) != 0 {
		t.Fatalf("no zuhause block: exit %d, %v", code, got)
	}
	if got, code, _, _ := run677(t, nil, &Zuhause{City: "Musterstadt", Latitude: &lat, Longitude: &lon}, "zuhause"); code != exitOK || got["city"] != "Musterstadt" || got["latitude"] != lat {
		t.Fatalf("show: exit %d, %v", code, got)
	}
	laib := &proposals{}
	got, code, errs, journal := run677(t, laib, &Zuhause{City: "Altstadt"}, "zuhause", "set", "--street", "Muster's Weg 12", "--postcode", "01234",
		"--city", "Musterstadt", "--country", "DE", "--latitude", "52.1234", "--longitude", "8.0456", "--elevation", "63")
	if code != exitOK || got["proposal"] != 1.0 || !strings.Contains(got["message"].(string), "Freigabe") {
		t.Fatalf("set: exit %d, %v %s", code, got, errs)
	}
	if len(laib.calls) != 1 || laib.ceiling != "propose" || len(laib.calls[0]) != 3 || laib.calls[0][0] != "--path" {
		t.Fatalf("not one laib propose --path under the verb's ceiling: %q (%s)", laib.calls, laib.ceiling)
	}
	path, value, _ := strings.Cut(laib.calls[0][1], "=")
	var z Zuhause
	if err := yaml.Unmarshal([]byte(value), &z); err != nil || path != "box.yml:zuhause" || strings.Contains(value, `"`) {
		t.Fatalf("the value at box.yml:zuhause, YAML without a double quote (the args are quoted with them): %s %q %v", path, value, err)
	}
	if z.Street != "Muster's Weg 12" || z.Postcode != "01234" || z.City != "Musterstadt" || z.Country != "DE" || *z.Latitude != lat || *z.Longitude != lon || *z.Elevation != elev {
		t.Fatalf("the block as proposed: %+v", z)
	}
	if reason := laib.calls[0][2]; strings.Contains(reason, "Muster") || strings.Contains(journal, "Muster") || !strings.Contains(reason, "Adresse") {
		t.Fatalf("the reason or the journal carries the address: %q %s", reason, journal)
	}
	// what is left out is removed: the whole block, not a merge with the old one
	laib.calls = nil
	run677(t, laib, &Zuhause{City: "Altstadt", Street: "Alter Weg 1"}, "zuhause", "set", "--city", "Musterstadt")
	if len(laib.calls) != 1 || laib.calls[0][1] != "box.yml:zuhause=city: 'Musterstadt'" {
		t.Fatalf("set replaces the block: %q", laib.calls)
	}
	// refused here: no flag, a bad number, a double quote or a second line; laib's own refusal comes back
	for _, args := range [][]string{{"set"}, {"set", "--latitude", "nördlich", "--longitude", "8"}, {"set", "--elevation", "6.5"}, {"set", "--city", `Muster"stadt`}, {"set", "--city", "Muster\nstadt"}, {"weg"}} {
		laib.calls = nil
		if _, code, _, _ := run677(t, laib, nil, append([]string{"zuhause"}, args...)...); code == exitOK || len(laib.calls) != 0 {
			t.Errorf("%q: exit %d, proposed %q", args, code, laib.calls)
		}
	}
	if _, code, errs, _ := run677(t, &proposals{refuse: "box.yml zuhause: latitude and longitude go together"}, nil, "zuhause", "set", "--latitude", "52"); code == exitOK || !strings.Contains(errs, "go together") {
		t.Fatalf("laib's refusal: exit %d, %s", code, errs)
	}
}

// #677: laib person signal moved behind the door: the number becomes the
// value at box.yml:people.<user>.signal through laib propose --path; the
// kernel's loader checks the number, and the reason carries none.
func TestPersonSignalProposesTheNumberAtItsPath(t *testing.T) {
	laib := &proposals{}
	got, code, errs, journal := run677(t, laib, nil, "person_signal", "mama", "+4900000002")
	if code != exitOK || got["proposal"] != 1.0 || !strings.Contains(got["message"].(string), "mama") {
		t.Fatalf("exit %d, %v %s", code, got, errs)
	}
	if len(laib.calls) != 1 || !slices.Equal(laib.calls[0][:2], []string{"--path", "box.yml:people.mama.signal=+4900000002"}) || laib.ceiling != "propose" {
		t.Fatalf("%q under %s", laib.calls, laib.ceiling)
	}
	if strings.Contains(laib.calls[0][2], "+49") || strings.Contains(journal, "+49") {
		t.Fatalf("the number is in the reason or the journal: %q %s", laib.calls[0][2], journal)
	}
	for _, args := range [][]string{{"mama"}, {"mama", "+49", "x"}, {"ma ma", "+4900000002"}, {"mama=x", "+4900000002"}, {"mama", `+49"1`}} {
		laib.calls = nil
		if _, code, _, _ := run677(t, laib, nil, append([]string{"person_signal"}, args...)...); code == exitOK || len(laib.calls) != 0 {
			t.Errorf("%q: exit %d, proposed %q", args, code, laib.calls)
		}
	}
}
