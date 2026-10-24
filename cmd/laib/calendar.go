package main

import (
	"bufio"
	"cmp"
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

// The calendar subscriptions (#495): Haus hands an address over once, laib
// keeps it in secrets.age as KALENDER_ABOS and applies the kalender template,
// whose timer mirrors each into Radicale. The address is never answered,
// journalled or put on argv: it arrives on stdin.
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

var aboName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
var aboPerson = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var aboEvery = regexp.MustCompile(`^[1-9][0-9]{0,3}[mhd]$`)

// cmdCalendar: `laib calendar subscribe <person> <name> google|abo [<title>]`
// (the address on stdin) | every <person> <name> | unsubscribe <person> <name>
// | list; subscribe and every take --every <30m|12h|30d>, per calendar (#619).
func cmdCalendar(ctx context.Context, env *cliEnv, args []string) int {
	every := ""
	if i := slices.Index(args, "--every"); i >= 0 && i+1 < len(args) {
		every, args = args[i+1], slices.Delete(slices.Clone(args), i, i+2)
	}
	// padded: a missing argument is "", the checks below refuse it
	args = append(args, "", "", "", "", "")
	act, pos, kind, title := args[0], args[1:3], args[3], args[4]
	if act != "list" && (!aboPerson.MatchString(pos[0]) || !aboName.MatchString(pos[1])) || act == "subscribe" && kind != "google" && kind != "abo" ||
		every != "" && !aboEvery.MatchString(every) || act == "every" && every == "" {
		return env.Fail(errors.New("usage: laib calendar subscribe <person> <name> google|abo [<title>] [--every 30d] (address on stdin) | every <person> <name> --every 30m|12h|30d | unsubscribe <person> <name> | list"))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return env.Fail(err)
	}
	var abos []Abo
	if raw := sec[abosSecret]; raw != "" {
		b, _ := base64.RawURLEncoding.DecodeString(raw)
		if err := json.Unmarshal(b, &abos); err != nil {
			return env.Fail(fmt.Errorf("%s ist nicht lesbar: %v", abosSecret, err))
		}
	}
	msg := ""
	switch act {
	case "list":
	case "subscribe":
		line, _ := bufio.NewReader(env.Stdin()).ReadString('\n')
		u, err := url.Parse(strings.Replace(strings.TrimSpace(line), "webcal://", "https://", 1))
		switch {
		case err != nil || u.Scheme != "https" || u.Host == "" || len(line) > 2048:
			return cli.Refused(env, "Das ist keine Kalender-Adresse (https:// oder webcal://).")
		case kind == "google" && (u.Host != "calendar.google.com" || !strings.HasSuffix(u.Path, ".ics")):
			return cli.Refused(env, "Das ist nicht Googles „Geheime Adresse im iCal-Format“ (https://calendar.google.com/…/basic.ics).")
		}
		abos = slices.DeleteFunc(abos, func(a Abo) bool { return a.Person == pos[0] && a.Name == pos[1] })
		abos = append(abos, Abo{Person: pos[0], Name: pos[1], Kind: kind, Title: title, URL: u.String(), Since: time.Now().UTC().Format(time.RFC3339), Every: every})
		msg = "„" + cmp.Or(title, pos[1]) + "“ ist verbunden. Die Termine erscheinen in den nächsten 15 Minuten."
	case "every":
		i := slices.IndexFunc(abos, func(a Abo) bool { return a.Person == pos[0] && a.Name == pos[1] })
		if i < 0 {
			return cli.Refused(env, "Diesen Kalender gibt es nicht: "+pos[0]+"/"+pos[1])
		}
		abos[i].Every = every
		msg = "„" + cmp.Or(abos[i].Title, pos[1]) + "“ wird ab jetzt alle " + every + " abgeglichen."
	case "unsubscribe":
		abos = slices.DeleteFunc(abos, func(a Abo) bool { return a.Person == pos[0] && a.Name == pos[1] })
		msg = "Getrennt. Beim nächsten Abgleich verschwindet die Kopie aus Radicale."
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
		if st.Services["kalender"] == nil {
			msg += " (Der Abgleich läuft, sobald das Template kalender installiert ist.)"
		} else if c := apply.ApplyLocked(ctx, &cli.Env{Paths: env.Paths, JSON: true, Actor: env.Actor, Out: &strings.Builder{}, ErrOut: env.Errw()}, st, "kalender", "calendar "+act); c != cli.ExitOK {
			return c
		}
	}
	for i := range abos {
		abos[i].URL = ""
	}
	return cli.Emit(env, map[string]any{"message": msg, "abos": abos})
}
