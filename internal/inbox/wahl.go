// Choice cards (ADR 0036): one card for one problem, its ways each a
// proposal, so approving one is the choice. The Operator files it with laib
// wahl anlegen; nachdenken and ticket are the repairs two of its ways run.

package inbox

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
)

const wahlUsage = `usage: laib wahl anlegen --problem "<was los ist>" [--ursache "<Ursache>"] --empfehlung "<welcher Weg, warum>" -- "<Name>|<was daraus folgt>|<Vorschlag-Nr | nachdenken>" …`

// Wahl is `laib wahl`: anlegen files a card; nachdenken and ticket <karte> are what approving those two ways runs.
func Wahl(ctx context.Context, env *cli.Env, args []string) int {
	if len(args) > 0 && args[0] == "anlegen" {
		return wahlAnlegen(ctx, env, args[1:])
	} else if len(args) == 2 && (args[0] == "nachdenken" || args[0] == "ticket") && policy.IsHuman(proposal.Hand(env.Actor)) {
		return wahlWeiter(ctx, env, args[0], args[1])
	}
	return env.Fail(errors.New(wahlUsage))
}

// parseWege reads each way "<Name>|<Folge>|<Nr | nachdenken>"; nachdenken is Vorschlag 0 until it is filed.
func parseWege(dir string, raw []string) (ways []proposal.Weg, err error) {
	for _, r := range raw {
		f := strings.SplitN(r, "|", 3)
		if len(f) != 3 || strings.TrimSpace(f[0]) == "" || strings.TrimSpace(f[1]) == "" || utf8.RuneCountInString(f[0]) > 40 || proposal.Technical(f[0]+f[1]) {
			return nil, fmt.Errorf("Weg %q: <Name bis 40 Zeichen>|<was daraus folgt, für Menschen>|<Vorschlag-Nr | nachdenken>", r)
		}
		w, kind := proposal.Weg{Label: strings.TrimSpace(f[0]), Folge: strings.TrimSpace(f[1])}, strings.TrimSpace(f[2])
		if n, e := strconv.Atoi(kind); e == nil {
			if q, e := proposal.Get(dir, n); e != nil || q.Status != "open" {
				return nil, fmt.Errorf("Weg %q: Vorschlag #%d ist nicht offen", r, n)
			}
			w.Vorschlag = n
		} else if kind != "nachdenken" || slices.ContainsFunc(ways, func(o proposal.Weg) bool { return o.Vorschlag == 0 }) {
			return nil, fmt.Errorf("Weg %q: eine Vorschlag-Nr oder nachdenken, das höchstens einmal", r)
		}
		ways = append(ways, w)
	}
	if len(ways) < 1 || len(ways) > 3 {
		return nil, errors.New("eine Wahl hat einen bis drei Wege, dazu legt Laib „Als Aufgabe weitergeben“")
	}
	return ways, nil
}

func wahlAnlegen(ctx context.Context, env *cli.Env, args []string) int {
	flags, raw := map[string]string{}, []string(nil)
	if i := slices.Index(args, "--"); i >= 0 {
		args, raw = args[:i], args[i+1:]
	}
	for i := 0; i+1 < len(args); i += 2 { // the verb's Flags refuse any other
		flags[strings.TrimPrefix(args[i], "--")] = strings.TrimSpace(args[i+1])
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "propose") {
		return cli.Refused(env, env.Actor+" may not propose")
	}
	dir := cli.InboxDir(env)
	ways, err := parseWege(dir, raw)
	if err == nil && (len(args)%2 != 0 || flags["problem"] == "" || flags["empfehlung"] == "" || proposal.Technical(flags["problem"]+flags["ursache"]+flags["empfehlung"])) {
		err = errors.New(wahlUsage + "\n(die Felder für Menschen: ein bis zwei Sätze, ohne Befehl, Unit oder Fehlertext)")
	}
	if err != nil {
		return env.Fail(err)
	}
	// the card itself is the last way: approved, it becomes a Box-Aufgabe (laib wahl ticket); its words are on the card as facts
	ways = append(ways, proposal.Weg{Label: "Als Aufgabe weitergeben", Folge: "Die Karte wird eine Aufgabe für die Admins; auf der Box ändert sich nichts."})
	var facts []string
	for _, w := range ways {
		facts = append(facts, w.Label+": "+w.Folge)
	}
	facts = append(facts, "Empfehlung: "+flags["empfehlung"])
	next, _ := proposal.NextID(dir) // the card names itself in its repair
	h, err := fileRepair(ctx, env, st, flags["problem"], "Wahl: "+flags["problem"], "Die Karte ändert selbst nichts; was ein Weg tut, steht bei ihm.", facts, []string{"laib wahl ticket " + strconv.Itoa(next)})
	if err == nil && h.ID != next {
		proposal.Reject(ctx, env.Paths.State, dir, h, "laib", "eine andere Karte kam dazwischen")
		err = errors.New("eine andere Karte kam dazwischen: noch einmal")
	}
	if i := slices.IndexFunc(ways, func(w proposal.Weg) bool { return w.Vorschlag == 0 }); err == nil && i >= 0 {
		var think *proposal.Proposal
		if think, err = fileRepair(ctx, env, st, "Noch einmal gründlich nachdenken", "Wahl: noch einmal nachdenken", "Nichts zu tun: es ändert nichts an der Box.", nil, []string{"laib wahl nachdenken " + strconv.Itoa(h.ID)}); err == nil {
			ways[i].Vorschlag = think.ID
		}
	}
	if err == nil {
		ways[len(ways)-1].Vorschlag = h.ID
		h.Wahl, h.Ursache, h.Empfehlung, h.Reasons = ways, cmp.Or(flags["ursache"], "nicht gemessen"), flags["empfehlung"], nil // no word on the card about a command
		err = proposal.Save(dir, h)
	}
	if err != nil {
		return env.Fail(err)
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "wahl", Result: "info", Reason: h.Reason, Detail: fmt.Sprintf("#%d wartet auf die Wahl zwischen %d Wegen", h.ID, len(ways))})
	fmt.Fprintf(env.W(), "Karte #%d angelegt: ein Mensch wählt einen von %d Wegen, die anderen schließen sich damit.\n", h.ID, len(ways))
	return cli.ExitOK
}

// wahlWeiter is what approving the card (ticket) or its repair (nachdenken) runs: a Box-Aufgabe for the
// admins with the card's words, or a red line the Operator opens an incident on, with more steps (operator/loop.go).
func wahlWeiter(ctx context.Context, env *cli.Env, kind, id string) int {
	n, _ := strconv.Atoi(id)
	h, err := proposal.Get(cli.InboxDir(env), n)
	st, serr := cli.LoadState(env)
	if err != nil || serr != nil || len(h.Wahl) == 0 {
		return env.Fail(fmt.Errorf("keine Karte mit Wegen: #%s", id))
	}
	words := h.Does + " Ursache: " + h.Ursache + ". " + strings.Join(h.Facts, " · ") // the problem, the cause, each way and the recommendation
	if kind == "nachdenken" {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "wahl", Result: "red", Reason: h.Reason, Detail: fmt.Sprintf("nachdenken: Karte #%d zurückgegeben, es soll gründlicher nachgesehen werden: %s Dann eine neue Karte mit Wegen anlegen.", n, words)})
		fmt.Fprintln(env.W(), "Der Operator sieht noch einmal genauer nach und legt eine neue Karte an.")
		return cli.ExitOK
	}
	task, _ := json.Marshal(map[string]any{"titel": h.Does, "warum": words, "weg": map[string]bool{"laib": true}})
	run := *env
	run.In, run.Out = strings.NewReader(string(task)), io.Discard
	if c := RunVerb(ctx, &run, []string{"aufgabe_kern", "add"}); c != cli.ExitOK {
		return c
	}
	fmt.Fprintln(env.W(), "Als Box-Aufgabe vermerkt; auf der Box ändert sich nichts.")
	return cli.ExitOK
}

// closeWays runs after p was decided. Approved, p is the way chosen: its card is closed, its other ways rejected;
// rejected, a card says no to all. Only a card still open excludes: a red run's two ways (#853) stay both.
func closeWays(ctx context.Context, env *cli.Env, p *proposal.Proposal, approved bool) {
	dir := cli.InboxDir(env)
	closing := func(id int, why string) {
		if q, err := proposal.Get(dir, id); err == nil && q.Status == "open" && proposal.Reject(ctx, env.Paths.State, dir, q, env.Actor, why) == nil {
			journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "reject", Reason: q.Reason, Result: "info", Detail: fmt.Sprintf("#%d: %s", id, why)})
		}
	}
	if !approved {
		for _, w := range p.Wahl {
			closing(w.Vorschlag, "Karte abgelehnt, kein Weg gewählt")
		}
		return
	}
	all, _ := proposal.List(dir, false)
	for _, h := range all {
		if i := slices.IndexFunc(h.Wahl, func(w proposal.Weg) bool { return w.Vorschlag == p.ID }); i >= 0 && (h.Status == "open" || h.ID == p.ID) {
			closing(h.ID, "Weg gewählt: "+h.Wahl[i].Label)
			for _, w := range h.Wahl {
				closing(w.Vorschlag, "anderer Weg gewählt: "+h.Wahl[i].Label)
			}
		}
	}
}
