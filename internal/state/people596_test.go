package state

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v} }

// #596, #598: the Signal number goes into people.<user> and nothing else of
// box.yml changes; the people block keeps its comments and its flow style.
func TestWithPathSetsOnlyThatChannel(t *testing.T) {
	box := "domain: x.home   # the domain\n# who lives here\npeople:           # the household\n  mama: [\"signal:+4900000002\", \"telegram:123\"]   # mum\n  lena: [\"matrix:!AbCdEf:example.org\"]\ntimezone: Europe/Berlin\n"
	head := "domain: x.home   # the domain\n# who lives here\npeople: # the household\n  mama: [\"signal:+4900000002\", \"telegram:123\"] # mum\n"
	for _, c := range []struct{ person, number, want string }{
		{"mama", "+4900000009", strings.Replace(head, "02", "09", 1) + "  lena: [\"matrix:!AbCdEf:example.org\"]\ntimezone: Europe/Berlin\n"},
		{"lena", "+4900000003", head + "  lena: [\"matrix:!AbCdEf:example.org\", \"signal:+4900000003\"]\ntimezone: Europe/Berlin\n"},
		{"oma", "+4900000004", head + "  lena: [\"matrix:!AbCdEf:example.org\"]\n  oma: [\"signal:+4900000004\"]\ntimezone: Europe/Berlin\n"},
	} {
		got, err := WithPath([]byte(box), "box.yml", "people."+c.person+".signal", scalar(c.number))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want {
			t.Errorf("%s:\ngot\n%s\nwant\n%s", c.person, got, c.want)
		}
	}
	// no people block yet: appended, a new person's channels a list as box.yml's schema has them
	got, err := WithPath([]byte("domain: x.home\n"), "box.yml", "people.mama.signal", scalar("+4900000002"))
	if err != nil || string(got) != "domain: x.home\npeople:\n  mama: [\"signal:+4900000002\"]\n" {
		t.Fatalf("%v: %q", err, got)
	}
	// nil removes: the channel, then a key, then a whole block
	got, _ = WithPath([]byte(box), "box.yml", "people.mama.signal", nil)
	if !strings.Contains(string(got), "  mama: [\"telegram:123\"] # mum\n") {
		t.Fatalf("channel removed: %s", got)
	}
	got, _ = WithPath([]byte(box), "box.yml", "people", nil)
	if string(got) != "domain: x.home   # the domain\n# who lives here\ntimezone: Europe/Berlin\n" {
		t.Fatalf("block removed: %q", got)
	}
}

// #598: a key box.yml does not know is refused before any proposal; another
// file has no schema, and a missing one starts empty (a new values.yml)
func TestWithPathRefusesUnknownBoxKeys(t *testing.T) {
	for _, at := range []string{"zuhause.strasse", "peoples.mama.signal", "domain.x"} {
		if got, err := WithPath([]byte("domain: x.home\n"), "box.yml", at, scalar("1")); err == nil {
			t.Errorf("%s: accepted: %s", at, got)
		}
	}
	got, err := WithPath(nil, "values.yml", "SCANNER", &yaml.Node{Kind: yaml.ScalarNode, Value: "true", Tag: "!!str"})
	if err != nil || string(got) != "SCANNER: \"true\"\n" {
		t.Fatalf("%v: %q", err, got)
	}
}
