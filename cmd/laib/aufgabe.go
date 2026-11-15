package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

// cmdAufgabeKern is laib aufgabe's kernel part (#667, #878, #1234):
// laib-haushalt serves the verb, its words and its checks of the input; the
// box's to-do list is aufgaben.yml in the state repo, which its container
// never mounts (ADR 0027), and who sees and changes which task is decided
// here, by the caller the Auftrag names. `list` (every task the caller sees,
// done ones too, and the owner's keys the box waits for) | `add` (the task as
// JSON on stdin) | `erledigt <id>` | `zurueckstellen <id> <JJJJ-MM-TT>` |
// `befund <id> "<probe> [<dienst|thema>]"`, each with `--von <login>` for
// the person Homes, the Bote or laib-haushalt act for. Bookkeeping, not a
// change of the box: no proposal, one commit and one journal line per change.
// ops, claude, Homes, humans and laib-haushalt (the Bote's wish, #883) add;
// humans, claude and Homes check off and defer.
func cmdAufgabeKern(ctx context.Context, env *cliEnv, args []string) int {
	von := ""
	if i := slices.Index(args, "--von"); i >= 0 && i+1 < len(args) {
		von, args = args[i+1], slices.Delete(slices.Clone(args), i, i+2)
	}
	if len(args) == 0 {
		return env.Fail(errors.New("usage: laib aufgabe_kern list | add (the task as JSON on stdin) | erledigt <id> | zurueckstellen <id> <JJJJ-MM-TT> | befund <id> <befund> [--von <login>]"))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	hand, now, act := proposal.Hand(env.Actor), time.Now(), args[0]
	by, sees := aufgabeCaller(env.Actor, von)
	switch {
	case act == "list":
		list, err := aufgaben.Load(env.Paths.State)
		if err != nil {
			return env.Fail(err)
		}
		list = slices.DeleteFunc(list, func(a aufgaben.Aufgabe) bool { return !sees(a) })
		linkKeys(st, list)
		return cli.Emit(env, map[string]any{"aufgaben": append([]aufgaben.Aufgabe{}, list...), "schluessel": missingKeys(env, st, list)})
	case act == "add" && !slices.Contains([]string{"ops", "claude", "homes", "haushalt"}, hand) && !policy.IsHuman(hand):
		return cli.Refused(env, hand+" darf keine Box-Aufgaben anlegen: das dürfen ops, claude, Homes und Menschen")
	case act != "add" && hand != "claude" && hand != "homes" && !policy.IsHuman(hand):
		return cli.Refused(env, hand+" darf Box-Aufgaben nicht abhaken oder zurückstellen: das dürfen Menschen, claude und Homes")
	}
	a, code := kernTask(env, st, args)
	if code != cli.ExitOK {
		return code
	}
	var detail string
	err = aufgaben.Update(ctx, env.Paths.State, by, func(list []aufgaben.Aufgabe) ([]aufgaben.Aufgabe, string, error) {
		var err error
		if i := slices.IndexFunc(list, func(x aufgaben.Aufgabe) bool { return x.ID == a.ID }); act != "add" && i >= 0 && !sees(list[i]) {
			return list, "", fmt.Errorf("Box-Aufgabe #%d gibt es nicht", a.ID) // another person's: as if it were not there
		}
		switch act {
		case "add":
			list, a = aufgaben.Add(list, a, by, now)
			detail = fmt.Sprintf("add #%d %s", a.ID, a.Titel)
			if strings.HasPrefix(a.Fuer, "person:") { // the journal says only that it exists (#1168)
				detail += " für eine Person"
			} else if a.Fuer != aufgaben.Admins {
				detail += " für " + a.Fuer
			}
		case "erledigt":
			err, detail = aufgaben.Done(list, a.ID, by, "", now), fmt.Sprintf("erledigt #%d", a.ID)
		case "befund":
			err, detail = aufgaben.Link(list, a.ID, a.FertigWenn), fmt.Sprintf("befund #%d %s", a.ID, a.FertigWenn)
		case "zurueckstellen":
			err, detail = aufgaben.Defer(list, a.ID, a.Erinnern), fmt.Sprintf("zurueckstellen #%d bis %s", a.ID, a.Erinnern)
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
	linkKeys(st, []aufgaben.Aufgabe{a})
	k, key := strings.CutPrefix(a.FertigWenn, "schluessel ")
	reply := map[string]any{"aufgabe": a, "selbst": a.FertigWenn != "" && aufgaben.ClosesItself(a.FertigWenn)}
	if key {
		reply["link"] = render.KeyLink(st, k)
	}
	return cli.Emit(env, reply)
}

// aufgabeCaller: who a change is recorded as, and which tasks the caller sees.
func aufgabeCaller(actor, von string) (string, func(aufgaben.Aufgabe) bool) {
	hand, by := proposal.Hand(actor), actor
	if von != "" && hand == "homes" {
		by = "homes:" + von // the person Homes acts for
	} else if von != "" && hand == "haushalt" {
		by = von // the caller laib-haushalt acts for, as the door named it: the Bote's wish (#883)
	}
	// #878: a person's tasks are theirs; Homes and the Bote see them only for
	// the person they act for, every other hand short of the whole list none
	whole, person := policy.IsHuman(hand) || slices.Contains([]string{"ops", "claude", "laib"}, hand), ""
	if hand == "homes" || hand == "bote" {
		person = von
	}
	return by, func(a aufgaben.Aufgabe) bool { return aufgaben.Sees(a, whole, person) }
}

// kernTask is the task an action names: add's from stdin, checked, or the id
// and the day or befund of the others.
func kernTask(env *cliEnv, st *state.State, args []string) (aufgaben.Aufgabe, int) {
	var a aufgaben.Aufgabe
	switch act := args[0]; act {
	case "add":
		if err := json.NewDecoder(env.Stdin()).Decode(&a); err != nil || strings.TrimSpace(a.Titel) == "" {
			return a, env.Fail(fmt.Errorf("add: die Aufgabe als JSON auf stdin, mit titel (%v)", err))
		}
		a.Fuer = cmp.Or(a.Fuer, aufgaben.Admins)
		if err := aufgaben.Fuer(a.Fuer); err != nil {
			return a, env.Fail(err)
		}
		if err := keyWeg(st, &a); err != nil {
			return a, cli.Refused(env, err.Error())
		}
	case "erledigt", "zurueckstellen", "befund":
		id, err := strconv.Atoi(strings.TrimPrefix(append(args, "")[1], "#"))
		if want := map[bool]int{true: 2, false: 3}[act == "erledigt"]; err != nil || len(args) != want || want == 3 && args[2] == "" {
			return a, env.Fail(fmt.Errorf("usage: laib aufgabe_kern %s <id>", act))
		}
		a.ID = id
		if act != "erledigt" {
			a.Erinnern, a.FertigWenn = args[2], args[2]
		}
	default:
		return a, env.Fail(fmt.Errorf("unknown aufgabe subcommand %q", act))
	}
	return a, cli.ExitOK
}

// Schluessel is an owner's key something on the box waits for (homes#362):
// its name, what the template tells the owner, and its form on the box page.
type Schluessel struct {
	Name    string `json:"name"`
	Dienst  string `json:"dienst,omitempty"`
	Label   string `json:"label"`
	Warum   string `json:"warum,omitempty"`
	Hilfe   string `json:"hilfe,omitempty"`
	Link    string `json:"link"`
	Aufgabe int    `json:"aufgabe,omitempty"` // an open task that waits for it
}

// keyWeg: a task's way is checked here, whoever composed it; one whose way
// is a key names an owner's key, and closes itself once that key is set (homes#362).
func keyWeg(st *state.State, a *aufgaben.Aufgabe) error {
	if a.Weg == nil {
		return nil
	}
	if err := aufgaben.CheckWeg(*a.Weg); err != nil {
		return err
	}
	k := a.Weg.Schluessel
	if k != "" && !slices.ContainsFunc(render.OwnerSecrets(st), func(o render.OwnerSecret) bool { return o.Name == k }) {
		return errors.New(k + " ist kein Schlüssel, den der Besitzer auf der Box-Seite einträgt")
	} else if k != "" {
		a.FertigWenn = cmp.Or(a.FertigWenn, "schluessel "+k)
	}
	return nil
}

// linkKeys fills in each key way's form on the box page.
func linkKeys(st *state.State, list []aufgaben.Aufgabe) {
	for _, a := range list {
		if a.Weg != nil && a.Weg.Schluessel != "" {
			a.Weg.Link = render.KeyLink(st, a.Weg.Schluessel)
		}
	}
}

// missingKeys: the keys diag's schluessel probe finds missing, never a
// value; nil (JSON null) when secrets.age cannot be read, [] for none.
func missingKeys(env *cliEnv, st *state.State, tasks []aufgaben.Aufgabe) []Schluessel {
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return nil
	}
	out := []Schluessel{}
	for _, k := range diag.MissingKeys(st, sec) {
		i := slices.IndexFunc(tasks, func(a aufgaben.Aufgabe) bool { return a.Open() && a.FertigWenn == "schluessel "+k.Name })
		s := Schluessel{Name: k.Name, Dienst: k.Service, Label: k.Owner.Label, Warum: k.Owner.Warum, Hilfe: k.Owner.Hilfe, Link: render.KeyLink(st, k.Name)}
		if i >= 0 {
			s.Aufgabe = tasks[i].ID
		}
		out = append(out, s)
	}
	return out
}
