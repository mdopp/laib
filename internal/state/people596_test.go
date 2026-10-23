package state

import "testing"

// #596: the Signal number goes into people.<user> and nothing else of box.yml
// changes; the people block keeps its comments and its flow style.
func TestWithSignalSetsOnlyThatChannel(t *testing.T) {
	box := "domain: x.home   # the domain\npeople:           # the household\n  mama: [\"signal:+4900000002\", \"telegram:123\"]   # mum\n  lena: [\"matrix:!AbCdEf:example.org\"]\ntimezone: Europe/Berlin\n"
	for _, c := range []struct{ person, number, want string }{
		{"mama", "+4900000009", "domain: x.home   # the domain\npeople: # the household\n  mama: [\"signal:+4900000009\", \"telegram:123\"] # mum\n  lena: [\"matrix:!AbCdEf:example.org\"]\ntimezone: Europe/Berlin\n"},
		{"lena", "+4900000003", "domain: x.home   # the domain\npeople: # the household\n  mama: [\"signal:+4900000002\", \"telegram:123\"] # mum\n  lena: [\"matrix:!AbCdEf:example.org\", \"signal:+4900000003\"]\ntimezone: Europe/Berlin\n"},
		{"oma", "+4900000004", "domain: x.home   # the domain\npeople: # the household\n  mama: [\"signal:+4900000002\", \"telegram:123\"] # mum\n  lena: [\"matrix:!AbCdEf:example.org\"]\n  oma: [\"signal:+4900000004\"]\ntimezone: Europe/Berlin\n"},
	} {
		got, err := WithSignal([]byte(box), c.person, c.number)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want {
			t.Errorf("%s:\ngot\n%s\nwant\n%s", c.person, got, c.want)
		}
	}
	// no people block yet: appended
	got, err := WithSignal([]byte("domain: x.home\n"), "mama", "+4900000002")
	if err != nil || string(got) != "domain: x.home\npeople:\n  mama: [\"signal:+4900000002\"]\n" {
		t.Fatalf("%v: %q", err, got)
	}
	if IsE164("0151 2345") || IsE164("+49 0000") || !IsE164("+4900000002") {
		t.Fatal("IsE164 takes only +<country><number>")
	}
}
