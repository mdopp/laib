package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
)

// cmdAufgabe: `laib aufgabe list [--alle] [--faellig] | zeigen <id> | add "<titel>"
// [--warum W] [--erinnern JJJJ-MM-TT] [--fertig-wenn "<probe> [<dienst|thema>]"]
// [--fuer admins|alle|person:<login>] [--weg /seite|gespraech] | erledigt <id>
// | zurueckstellen <id> <JJJJ-MM-TT> | befund <id> "<probe> [<dienst|thema>]"`
// (#667, #759, #878): the box's to-do list in the state repo's
// aufgaben.yml. Bookkeeping, not a change of the box: no proposal, one
// commit and one journal line per change. ops, claude, Haus, humans and
// laib-haushalt (the Bote's wish, #883) add; humans, claude and Haus (for
// the logged-in person, --von) check off and defer. Haus and the Bote act for one person (--von): they see the admins'
// and everyone's tasks and only that person's own (#878). In the kernel,
// not behind the verb door (ADR 0026): it writes the state repo, which
// laib-haushalt's container never mounts (ADR 0027), and diag reads it to
// defer and close findings.
func cmdAufgabe(ctx context.Context, env *cliEnv, args []string) int {
	val, pos := map[string]string{}, []string{}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--alle" || a == "--faellig":
			val[a] = "1"
		case strings.HasPrefix(a, "--") && i+1 < len(args):
			val[a], i = args[i+1], i+1
		default:
			pos = append(pos, a)
		}
	}
	usage := errors.New(`usage: laib aufgabe list [--alle] [--faellig] | zeigen <id> | add "<titel>" [--warum W] [--erinnern JJJJ-MM-TT] [--fertig-wenn "<probe> [<dienst|thema>]"] [--fuer admins|alle|person:<login>] [--weg /seite|gespraech] | erledigt <id> | zurueckstellen <id> <JJJJ-MM-TT> | befund <id> "<probe> [<dienst|thema>]"`)
	if len(pos) == 0 {
		return env.Fail(usage)
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	hand, now := proposal.Hand(env.Actor), time.Now()
	by := env.Actor
	if v := val["--von"]; v != "" && hand == "haus" {
		by = "haus:" + v // the person Haus acts for
	} else if v != "" && hand == "haushalt" {
		by = v // the caller laib-haushalt acts for, as the door named it: the Bote's wish (#883)
	}
	// #878: a person's tasks are theirs; Haus and the Bote see them only for
	// the person they act for, every other hand short of the whole list none
	whole, person := policy.IsHuman(hand) || slices.Contains([]string{"ops", "claude", "laib"}, hand), ""
	if hand == "haus" || hand == "bote" {
		person = val["--von"]
	}
	sees := func(a aufgaben.Aufgabe) bool { return aufgaben.Sees(a, whole, person) }
	act, today := pos[0], now.Format(time.DateOnly)
	switch {
	case act == "list" || act == "zeigen":
		list, err := aufgaben.Load(env.Paths.State)
		if err != nil {
			return env.Fail(err)
		}
		list = slices.DeleteFunc(list, func(a aufgaben.Aufgabe) bool { return !sees(a) })
		if act == "zeigen" {
			id, err := strconv.Atoi(strings.TrimPrefix(append(pos, "")[1], "#"))
			i := slices.IndexFunc(list, func(a aufgaben.Aufgabe) bool { return a.ID == id })
			if err != nil || len(pos) != 2 {
				return env.Fail(usage)
			}
			if i < 0 {
				return cli.Refused(env, fmt.Sprintf("Box-Aufgabe #%d gibt es nicht", id))
			}
			if env.JSON {
				return cli.Emit(env, map[string]any{"message": list[i].Titel, "aufgabe": list[i]})
			}
			fmt.Fprintf(env.W(), "#%d %s\n%s\n", id, list[i].Titel, list[i].Warum)
			return cli.ExitOK
		}
		open, due := aufgaben.Counts(list, today)
		list = slices.DeleteFunc(list, func(a aufgaben.Aufgabe) bool {
			return val["--faellig"] != "" && !a.Due(today) || val["--alle"] == "" && !a.Open()
		})
		if env.JSON {
			return cli.Emit(env, map[string]any{"offen": open, "faellig": due, "aufgaben": append([]aufgaben.Aufgabe{}, list...)})
		}
		fmt.Fprintf(env.W(), "Box-Aufgaben: %d offen, %d fällig\n", open, due)
		for _, a := range list {
			line := fmt.Sprintf("#%-3d %s", a.ID, a.Titel)
			if a.Erledigt != nil {
				line += fmt.Sprintf("  ✓ %s %s %s", a.Erledigt.Am, a.Erledigt.Von, a.Erledigt.Wie)
			} else if a.Erinnern != "" {
				line += "  erinnern " + a.Erinnern
			}
			if a.FertigWenn != "" {
				line += "  [" + a.FertigWenn + "]"
			}
			if a.Fuer != aufgaben.Admins {
				line += "  für " + a.Fuer
			}
			fmt.Fprintln(env.W(), strings.TrimRight(line, " "))
		}
		return cli.ExitOK
	case act == "add" && !slices.Contains([]string{"ops", "claude", "haus", "haushalt"}, hand) && !policy.IsHuman(hand):
		return cli.Refused(env, hand+" darf keine Box-Aufgaben anlegen: das dürfen ops, claude, Haus und Menschen")
	case act != "add" && hand != "claude" && hand != "haus" && !policy.IsHuman(hand):
		return cli.Refused(env, hand+" darf Box-Aufgaben nicht abhaken oder zurückstellen: das dürfen Menschen, claude und Haus")
	}
	var a aufgaben.Aufgabe
	var msg, detail string
	switch act {
	case "add":
		if len(pos) != 2 || strings.TrimSpace(pos[1]) == "" || len(pos[1]) > 200 || strings.ContainsAny(pos[1]+val["--warum"], "\n\r") {
			return env.Fail(usage)
		}
		if d := val["--erinnern"]; d != "" && aufgaben.Day(d) != nil {
			return env.Fail(aufgaben.Day(d))
		}
		fw := cmp.Or(val["--fertig-wenn"], val["--befund"]) // --befund: its name until #878
		if b := val["--befund"]; b != "" && val["--fertig-wenn"] != "" && b != val["--fertig-wenn"] {
			return env.Fail(errors.New("--befund ist der alte Name von --fertig-wenn: nur eines von beiden"))
		}
		if fw != "" && aufgaben.Befund(fw) != nil {
			return env.Fail(aufgaben.Befund(fw))
		}
		a = aufgaben.Aufgabe{Titel: strings.TrimSpace(pos[1]), Warum: val["--warum"], Erinnern: val["--erinnern"], FertigWenn: fw, Fuer: cmp.Or(val["--fuer"], aufgaben.Admins)}
		if err := aufgaben.Fuer(a.Fuer); err != nil {
			return env.Fail(err)
		}
		if w := val["--weg"]; w != "" {
			if a.Weg, err = aufgaben.ParseWeg(w); err != nil {
				return env.Fail(err)
			}
		}
	case "erledigt", "zurueckstellen", "befund":
		pos = append(pos, "", "")
		id, err := strconv.Atoi(strings.TrimPrefix(pos[1], "#"))
		day := cmp.Or(pos[2], val["--erinnern"])
		if err != nil || pos[3] != "" || act == "erledigt" && day != "" || act != "erledigt" && day == "" {
			return env.Fail(usage)
		}
		if act == "befund" && aufgaben.Befund(pos[2]) != nil { // #759: a task added without one, linked afterwards
			return env.Fail(aufgaben.Befund(pos[2]))
		}
		a.ID, a.Erinnern, a.FertigWenn = id, day, pos[2]
	default:
		return env.Fail(usage)
	}
	err = aufgaben.Update(ctx, env.Paths.State, by, func(list []aufgaben.Aufgabe) ([]aufgaben.Aufgabe, string, error) {
		var err error
		if i := slices.IndexFunc(list, func(x aufgaben.Aufgabe) bool { return x.ID == a.ID }); act != "add" && i >= 0 && !sees(list[i]) {
			return list, "", fmt.Errorf("Box-Aufgabe #%d gibt es nicht", a.ID) // another person's: as if it were not there
		}
		switch act {
		case "add":
			list, a = aufgaben.Add(list, a, by, now)
			msg, detail = fmt.Sprintf("Box-Aufgabe #%d angelegt: %s", a.ID, a.Titel), fmt.Sprintf("add #%d %s", a.ID, a.Titel)
			if a.Fuer != aufgaben.Admins {
				detail += " für " + a.Fuer
			}
			if a.FertigWenn != "" && aufgaben.ClosesItself(a.FertigWenn) {
				msg += fmt.Sprintf(". Laib hakt sie selbst ab, wenn der Befund %s weg ist.", a.FertigWenn)
			} else if a.FertigWenn != "" {
				msg += fmt.Sprintf(". Laib hakt sie nicht selbst ab: %s misst nicht bei jedem diag, abhaken tut ein Mensch.", a.FertigWenn)
			}
		case "erledigt":
			err = aufgaben.Done(list, a.ID, by, "", now)
			msg, detail = fmt.Sprintf("Box-Aufgabe #%d erledigt.", a.ID), fmt.Sprintf("erledigt #%d", a.ID)
		case "befund":
			err = aufgaben.Link(list, a.ID, a.FertigWenn)
			msg, detail = fmt.Sprintf("Box-Aufgabe #%d schließt Laib, wenn der Befund %s weg ist.", a.ID, a.FertigWenn), fmt.Sprintf("befund #%d %s", a.ID, a.FertigWenn)
		case "zurueckstellen":
			err = aufgaben.Defer(list, a.ID, a.Erinnern)
			msg, detail = fmt.Sprintf("Box-Aufgabe #%d zurückgestellt bis %s.", a.ID, a.Erinnern), fmt.Sprintf("zurueckstellen #%d bis %s", a.ID, a.Erinnern)
		}
		if i := slices.IndexFunc(list, func(x aufgaben.Aufgabe) bool { return x.ID == a.ID }); i >= 0 && err == nil {
			a = list[i]
		}
		return list, "aufgabe " + detail, err
	})
	if err != nil {
		return cli.Refused(env, err.Error())
	}
	cli.Record(env, st, journal.Entry{Actor: by, Verb: "aufgabe", Result: "info", Detail: detail})
	if env.JSON {
		return cli.Emit(env, map[string]any{"message": msg, "aufgabe": a})
	}
	fmt.Fprintln(env.W(), msg)
	return cli.ExitOK
}
