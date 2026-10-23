package state

import (
	"strings"
	"testing"
)

// a fictitious household: no real address in the tree (#561)
const musterZuhause = "zuhause:\n  street: Musterweg 12\n  postcode: \"12345\"\n  city: Musterstadt\n  country: DE\n  latitude: 52.1234\n  longitude: 8.0456\n  elevation: 63\n"

// #561: box.yml holds the household's address once, next to timezone, and
// every template reads it as the ZUHAUSE_* builtins.
func TestZuhauseIsReadOnceAndTemplatesGetItAsBuiltins(t *testing.T) {
	dir := minimal(t)
	write(t, dir, "box.yml", "domain: x.home\ntimezone: Europe/Berlin\ndataDir: /d\nroles: {sso: auth}\n"+musterZuhause)
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	v := st.ZuhauseVars()
	for k, want := range map[string]string{"ZUHAUSE_STREET": "Musterweg 12", "ZUHAUSE_POSTCODE": "12345", "ZUHAUSE_CITY": "Musterstadt",
		"ZUHAUSE_COUNTRY": "DE", "ZUHAUSE_LATITUDE": "52.1234", "ZUHAUSE_LONGITUDE": "8.0456", "ZUHAUSE_ELEVATION": "63"} {
		if v[k] != want {
			t.Errorf("%s = %q, want %q", k, v[k], want)
		}
	}
	for k := range v {
		if !strings.Contains(strings.Join(Builtins, " "), k) {
			t.Errorf("%s is not a builtin, so a unit naming it fails lint", k)
		}
	}
	rev := v["ZUHAUSE_REV"]
	st.Box.Zuhause.City = "Beispielheim"
	if rev == "" || st.ZuhauseVars()["ZUHAUSE_REV"] == rev {
		t.Fatalf("ZUHAUSE_REV must change with the address, so a unit carrying it is applied again: %q", rev)
	}
}

// without the block nothing is set, and no template gets a guess
func TestNoZuhauseIsEmptyValues(t *testing.T) {
	st, err := Load(minimal(t))
	if err != nil {
		t.Fatal(err)
	}
	for k, val := range st.ZuhauseVars() {
		if val != "" && k != "ZUHAUSE_REV" {
			t.Errorf("%s = %q without a zuhause block", k, val)
		}
	}
}

func TestZuhauseIsValidatedOnLoad(t *testing.T) {
	for block, want := range map[string]string{
		"zuhause: {latitude: 52.1}\n":                               "latitude and longitude",
		"zuhause: {longitude: 8.1}\n":                               "latitude and longitude",
		"zuhause: {latitude: 91, longitude: 8}\n":                   "latitude",
		"zuhause: {latitude: 52, longitude: 181}\n":                 "longitude",
		"zuhause: {country: Deutschland}\n":                         "country",
		"zuhause: {postcode: \"12345; rm -rf\"}\n":                  "postcode",
		"zuhause: {street: \"Musterweg 1\\nX=1\"}\n":                "one line",
		"zuhause: {latitude: 52, longitude: 8, elevation: 99999}\n": "elevation",
	} {
		dir := minimal(t)
		write(t, dir, "box.yml", "domain: x.home\ndataDir: /d\nroles: {sso: auth}\n"+block)
		_, err := Load(dir)
		if err == nil || !strings.Contains(err.Error(), "box.yml zuhause") || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want an error about %q, got %v", block, want, err)
		}
	}
	dir := minimal(t)
	write(t, dir, "box.yml", "domain: x.home\ndataDir: /d\nroles: {sso: auth}\nzuhause: {city: Musterstadt, postcode: D-12345}\n")
	if _, err := Load(dir); err != nil {
		t.Fatalf("an address without coordinates is fine: %v", err)
	}
}

// an address without coordinates while a service reads them: plan says so,
// Laib looks nothing up (#561)
func TestAnAddressWithoutCoordinatesIsAWarningWhenAServiceReadsThem(t *testing.T) {
	dir := minimal(t)
	write(t, dir, "box.yml", "domain: x.home\ndataDir: /d\nroles: {sso: auth}\nzuhause: {city: Musterstadt}\n")
	write(t, dir, "services/auth/auth.container", "[Container]\nImage=x\nEnvironment=ZUHAUSE_REV=${ZUHAUSE_REV}\n")
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if w := strings.Join(st.Warnings, "\n"); !strings.Contains(w, "zuhause") || !strings.Contains(w, "auth") {
		t.Fatalf("no warning naming the service: %q", st.Warnings)
	}
}

// laib zuhause set writes the block and nothing else of box.yml (#561)
func TestWithBlockReplacesOnlyTheBlock(t *testing.T) {
	box := "domain: x.home   # the domain\ntimezone: Europe/Berlin\nzuhause:\n  city: Altstadt   # old\n  latitude: 1\n  longitude: 2\npeople:\n  mama: [\"signal:+4917000000000\"]\n"
	lat, lon := 52.1234, 8.0456
	z := Zuhause{Street: "Musterweg 12", Postcode: "01234", City: "Musterstadt", Latitude: &lat, Longitude: &lon}
	got, err := WithBlock([]byte(box), "zuhause", z)
	if err != nil {
		t.Fatal(err)
	}
	want := "domain: x.home   # the domain\ntimezone: Europe/Berlin\nzuhause:\n  street: Musterweg 12\n  postcode: \"01234\"\n  city: Musterstadt\n  latitude: 52.1234\n  longitude: 8.0456\npeople:\n  mama: [\"signal:+4917000000000\"]\n"
	if string(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// missing: appended, and a file without a final newline stays YAML
	got, _ = WithBlock([]byte("domain: x.home"), "zuhause", Zuhause{City: "Musterstadt"})
	if string(got) != "domain: x.home\nzuhause:\n  city: Musterstadt\n" {
		t.Fatalf("appended: %q", got)
	}
}
