package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// channelRe is a channel's target by platform (ADR 0022 §3): the shapes
// `hermes send --to <platform>:<target>` takes. A platform not listed here
// is not one a person can be reached on.
var channelRe = map[string]*regexp.Regexp{
	"signal":   e164,
	"sms":      e164,
	"whatsapp": e164,
	"telegram": regexp.MustCompile(`^-?[0-9]+$`),
	"matrix":   regexp.MustCompile(`^[@!][^:\s]+:\S+$`),
	"discord":  regexp.MustCompile(`^[0-9]+$`),
	"slack":    regexp.MustCompile(`^[A-Z0-9]+$`),
	"email":    regexp.MustCompile(`^[^@\s]+@[^@\s]+$`),
}

var channelHint = map[string]string{"signal": "E.164 (+49…)", "sms": "E.164 (+49…)", "whatsapp": "E.164 (+49…)",
	"telegram": "a numeric chat id", "matrix": "a matrix id (@user:server or !room:server)", "discord": "a numeric channel id",
	"slack": "a channel id (C0123…)", "email": "an address"}

func (st *State) validatePeople() []error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(st.Box.People)) {
		if !nameRe.MatchString(name) {
			errs = append(errs, fmt.Errorf("box.yml people: %q is not a name (lowercase, like mama)", name))
		}
		if len(st.Box.People[name]) == 0 {
			errs = append(errs, fmt.Errorf("box.yml people.%s: no channel", name))
		}
		for _, ch := range st.Box.People[name] {
			platform, target, _ := strings.Cut(ch, ":")
			re, ok := channelRe[platform]
			switch {
			case !ok:
				errs = append(errs, fmt.Errorf("box.yml people.%s: %q: platform %q is not one of %s", name, ch, platform, strings.Join(slices.Sorted(maps.Keys(channelRe)), ", ")))
			case target == "":
				errs = append(errs, fmt.Errorf("box.yml people.%s: %q has no target", name, ch))
			case !re.MatchString(target):
				errs = append(errs, fmt.Errorf("box.yml people.%s: %q: the target must be %s", name, ch, channelHint[platform]))
			}
		}
	}
	return errs
}

// PeopleJSON is render/people/people.json, what bote-notify resolves a
// person with: name → channels in preference order.
func (st *State) PeopleJSON() string {
	people := st.Box.People
	if people == nil {
		people = map[string][]string{}
	}
	b, _ := json.Marshal(people) // map keys sorted
	return string(b) + "\n"
}

// PeopleRev changes with any person or channel, so the unit that carries
// it is restarted and its hooks run again.
func (st *State) PeopleRev() string {
	h := sha256.Sum256([]byte(st.PeopleJSON()))
	return hex.EncodeToString(h[:])[:12]
}
