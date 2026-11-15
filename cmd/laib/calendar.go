package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/secrets"
)

// The calendar subscriptions (#495): Homes hands an address over once, laib
// keeps it in secrets.age as KALENDER_ABOS and applies the kalender template,
// whose timer mirrors each into Radicale. The address is never answered,
// journalled or put on argv: it arrives on stdin. laib-haushalt serves laib
// calendar (#1234); this is its kernel part, the one write it may not do.
const abosSecret = "KALENDER_ABOS"

// Abo is one subscription, mirrored into <person>/google-<name> or
// <person>/abo-<name>; URL stays in secrets.age, every answer blanks it.
type Abo struct {
	Person string `json:"person"`
	Name   string `json:"name"`
	Kind   string `json:"kind"` // google: a Google calendar's secret iCal address; abo: a public ICS
	Title  string `json:"title"`
	URL    string `json:"url,omitempty"`
	Since  string `json:"since"`
	Every  string `json:"every,omitempty"` // how often it is fetched (#619): 30m, 12h, 30d; empty: Google every run, a public abo ABO_EVERY_DAYS
}

// what the kalender template makes paths and fetches of: held here, whatever laib-haushalt sent
var aboName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
var aboPerson = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var aboEvery = regexp.MustCompile(`^[1-9][0-9]{0,3}[mhd]$`)

// cmdCalendarKern: `subscribe <person> <name>` (the Abo as JSON on stdin,
// its address in it) | `every <person> <name> <30m|12h|30d>` | `unsubscribe
// <person> <name>` | `list`; answers {abos, kalender} with every address blanked.
func cmdCalendarKern(ctx context.Context, env *cliEnv, args []string) int {
	args = append(args, "", "", "", "") // padded: a missing argument is "", the checks below refuse it
	act, pos := args[0], args[1:3]
	var in Abo
	if act == "subscribe" {
		json.NewDecoder(env.Stdin()).Decode(&in)
	} else if act == "every" {
		in.Every = args[3]
	}
	u, err := url.Parse(in.URL)
	if act != "list" && (!aboPerson.MatchString(pos[0]) || !aboName.MatchString(pos[1])) || in.Every != "" && !aboEvery.MatchString(in.Every) ||
		act == "subscribe" && (in.Kind != "google" && in.Kind != "abo" || err != nil || u.Scheme != "https" || u.Host == "" || len(in.URL) > 2048) || act == "every" && in.Every == "" {
		return env.Fail(errors.New("usage: laib calendar_kern subscribe <person> <name> (the Abo as JSON on stdin) | every <person> <name> 30m|12h|30d | unsubscribe <person> <name> | list"))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return env.Fail(err)
	}
	abos, err := abosOf(sec)
	if err != nil {
		return env.Fail(err)
	}
	if pos[0] == "haus" {
		pos[0] = "homes"
	}
	switch act {
	case "list":
	case "subscribe":
		abos = slices.DeleteFunc(abos, func(a Abo) bool { return a.Person == pos[0] && a.Name == pos[1] })
		abos = append(abos, Abo{Person: pos[0], Name: pos[1], Kind: in.Kind, Title: in.Title, URL: in.URL, Since: time.Now().UTC().Format(time.RFC3339), Every: in.Every})
	case "every":
		i := slices.IndexFunc(abos, func(a Abo) bool { return a.Person == pos[0] && a.Name == pos[1] })
		if i < 0 {
			return cli.Refused(env, "Diesen Kalender gibt es nicht: "+pos[0]+"/"+pos[1])
		}
		abos[i].Every = in.Every
	case "unsubscribe":
		abos = slices.DeleteFunc(abos, func(a Abo) bool { return a.Person == pos[0] && a.Name == pos[1] })
	default:
		return env.Fail(fmt.Errorf("unknown calendar subcommand %q", act))
	}
	if act != "list" {
		b, _ := json.Marshal(abos)
		sec[abosSecret] = base64.RawURLEncoding.EncodeToString(b)
		if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
			return env.Fail(err)
		}
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "calendar", Service: "kalender", Result: "info", Detail: act + " " + pos[0] + "/" + pos[1]})
		if st.Services["kalender"] != nil {
			if c := apply.ApplyLocked(ctx, &cli.Env{Paths: env.Paths, JSON: true, Actor: env.Actor, Out: &strings.Builder{}, ErrOut: env.Errw()}, st, "kalender", "calendar "+act); c != cli.ExitOK {
				return c
			}
		}
	}
	for i := range abos {
		abos[i].URL = ""
	}
	return cli.Emit(env, map[string]any{"abos": abos, "kalender": st.Services["kalender"] != nil})
}

// abosOf reads KALENDER_ABOS. The household's calendar account was haus until
// ADR 0031 (#1021): a subscription stored for it is the account homes's, and
// the next write stores it so; goes at #1021 step 9.
func abosOf(sec map[string]string) ([]Abo, error) {
	var abos []Abo
	if raw := sec[abosSecret]; raw != "" {
		b, _ := base64.RawURLEncoding.DecodeString(raw)
		if err := json.Unmarshal(b, &abos); err != nil {
			return nil, fmt.Errorf("%s ist nicht lesbar: %v", abosSecret, err)
		}
	}
	for i := range abos {
		if abos[i].Person == "haus" {
			abos[i].Person = "homes"
		}
	}
	return abos, nil
}
