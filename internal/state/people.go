package state

import (
	"cmp"
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
	"matrix":   regexp.MustCompile(`^![^:\s]+:[^\s$]+$`), // a room (#189): Hermes posts with RoomID(chat_id); no <room>:$<event>, which it reads as a thread
	"discord":  regexp.MustCompile(`^[0-9]+$`),
	"slack":    regexp.MustCompile(`^[A-Z0-9]+$`),
	"email":    regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`), // Hermes' _EMAIL_TARGET_RE
}

var channelHint = map[string]string{"signal": "E.164 (+49…)", "sms": "E.164 (+49…)", "whatsapp": "E.164 (+49…)",
	"telegram": "a numeric chat id", "matrix": "a room, not a user (Hermes posts into a room): the room id of the direct chat with the Bote, !…:server (Element: Room settings → Advanced → Internal room ID)", "discord": "a numeric channel id",
	"slack": "a channel id (C0123…)", "email": "an address with a domain (name@example.org)"}

func (st *State) validatePeople() []error {
	var errs []error
	// the channels some service delivers on (#184): with any declared, a
	// person's target must be on one that is set
	on, off := map[string]bool{}, map[string]string{}
	declared := false
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		var vals []string
		for _, platform := range slices.Sorted(maps.Keys(svc.Contract.Channels)) {
			v := svc.Contract.Channels[platform]
			declared, vals = true, append(vals, v)
			if _, ok := channelRe[platform]; !ok {
				errs = append(errs, fmt.Errorf("services/%s/laib.yml: channels: %q is not a platform people are reached on", name, platform))
			} else if svc.Set(v) {
				on[platform] = true
			} else {
				off[platform] = fmt.Sprintf("services/%s: %s is empty", name, v)
			}
		}
		if len(vals) > 0 && !slices.ContainsFunc(vals, svc.Set) {
			errs = append(errs, fmt.Errorf("services/%s: no channel is set (one of %s)", name, strings.Join(vals, ", ")))
		}
	}
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
			case declared && !on[platform]:
				errs = append(errs, fmt.Errorf("box.yml people.%s: %q: no service delivers on %s (%s)", name, ch, platform, cmp.Or(off[platform], "none declares it")))
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
