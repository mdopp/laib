package haushalt

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Abo is one calendar subscription as the kernel answers it (#495): its
// address stays in secrets.age and is never in an answer.
type Abo struct {
	Person string `json:"person"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	URL    string `json:"url,omitempty"`
	Since  string `json:"since"`
	Every  string `json:"every,omitempty"`
}

var aboName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
var aboPerson = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var aboEvery = regexp.MustCompile(`^[1-9][0-9]{0,3}[mhd]$`)

// cmdCalendar: `laib calendar subscribe <person> <name> google|abo [<title>]`
// (the address on stdin) | every <person> <name> | unsubscribe <person> <name>
// | list; subscribe and every take --every <30m|12h|30d>, per calendar (#619).
// Here the address is checked and the answer worded (#1234); the kernel's
// part, calendar_kern, keeps KALENDER_ABOS in secrets.age and applies the
// kalender template, which mirrors each into Radicale.
func cmdCalendar(ctx context.Context, env *Env, args []string) int {
	every := ""
	if i := slices.Index(args, "--every"); i >= 0 && i+1 < len(args) {
		every, args = args[i+1], slices.Delete(slices.Clone(args), i, i+2)
	}
	// padded: a missing argument is "", the checks below refuse it
	args = append(args, "", "", "", "", "")
	act, pos, kind, title := args[0], args[1:3], args[3], args[4]
	if act != "list" && (!aboPerson.MatchString(pos[0]) || !aboName.MatchString(pos[1])) || act == "subscribe" && kind != "google" && kind != "abo" ||
		every != "" && !aboEvery.MatchString(every) || act == "every" && every == "" {
		return env.fail(errors.New("usage: laib calendar subscribe <person> <name> google|abo [<title>] [--every 30d] (address on stdin) | every <person> <name> --every 30m|12h|30d | unsubscribe <person> <name> | list"))
	}
	var reply struct {
		Abos     []Abo `json:"abos"`
		Kalender bool  `json:"kalender"`
	}
	kern := func(stdin string, a ...string) error {
		return env.Laib.Kern(ctx, env.Auftrag, "laib_calendar_kern", stdin, &reply, a...)
	}
	msg, err := "", error(nil)
	switch act {
	case "list":
		err = kern("", "list")
	case "subscribe":
		line, _ := bufio.NewReader(env.stdin()).ReadString('\n')
		u, perr := url.Parse(strings.Replace(strings.TrimSpace(line), "webcal://", "https://", 1))
		switch {
		case perr != nil || u.Scheme != "https" || u.Host == "" || len(line) > 2048:
			return env.refused("Das ist keine Kalender-Adresse (https:// oder webcal://).")
		case kind == "google" && (u.Host != "calendar.google.com" || !strings.HasSuffix(u.Path, ".ics")):
			return env.refused("Das ist nicht Googles „Geheime Adresse im iCal-Format“ (https://calendar.google.com/…/basic.ics).")
		}
		b, _ := json.Marshal(Abo{Kind: kind, Title: title, URL: u.String(), Every: every})
		err = kern(string(b), "subscribe", pos[0], pos[1])
		msg = "„" + cmp.Or(title, pos[1]) + "“ ist verbunden. Die Termine erscheinen in den nächsten 15 Minuten."
	case "every":
		err = kern("", "every", pos[0], pos[1], every)
		i := slices.IndexFunc(reply.Abos, func(a Abo) bool {
			return a.Name == pos[1] && (a.Person == pos[0] || pos[0] == "haus" && a.Person == "homes")
		})
		msg = "„" + cmp.Or(at(reply.Abos, i).Title, pos[1]) + "“ wird ab jetzt alle " + every + " abgeglichen."
	case "unsubscribe":
		err = kern("", "unsubscribe", pos[0], pos[1])
		msg = "Getrennt. Beim nächsten Abgleich verschwindet die Kopie aus Radicale."
	default:
		return env.fail(errors.New("unknown calendar subcommand " + act))
	}
	if err != nil {
		return env.kernFailed(err)
	}
	if act != "list" && !reply.Kalender {
		msg += " (Der Abgleich läuft, sobald das Template kalender installiert ist.)"
	}
	return env.emit(map[string]any{"message": msg, "abos": reply.Abos})
}

// at is list[i], or the zero value when i is not in it.
func at[T any](list []T, i int) T {
	var zero T
	if i < 0 || i >= len(list) {
		return zero
	}
	return list[i]
}
