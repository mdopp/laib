package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

// personTool is the identity template's tool (templates/auth/person.py,
// #360): it knows LLDAP and Authelia; laib runs it in the hooks image and
// owns the verbs, the policy and the journal.
const personTool = "person.py"

// Invite is what person_invite returns (haus#56 codes against it).
type Invite struct {
	User     string `json:"user"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
	Mailed   bool   `json:"mailed"`
	Link     string `json:"link,omitempty"`
	Expires  string `json:"expires"`
	Message  string `json:"message"`
	Proposal int    `json:"proposal,omitempty"` // the Signal number's, with --signal (#596)
}

// runPerson runs the tool and decodes its last output line; a refusal is
// the tool's German sentence.
func runPerson(ctx context.Context, env *cliEnv, v any, args ...string) error {
	st, err := cli.LoadState(env)
	if err != nil {
		return err
	}
	svc := st.Services[st.Box.Roles.Identity]
	if svc == nil {
		return errors.New("kein Identitätsdienst (box.yml roles.identity): Personen lädt erst auth ein")
	}
	if _, err := os.Stat(svc.Dir + "/" + personTool); err != nil {
		return fmt.Errorf("%s kann keine Personen einladen (kein %s; laib template update %s)", svc.Name, personTool, svc.Name)
	}
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return err
	}
	out, runErr := apply.RunTool(ctx, st, svc, env.Paths, sec, personTool, args...)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := lines[len(lines)-1]
	var refusal struct{ Error string }
	if json.Unmarshal([]byte(last), &refusal) == nil && refusal.Error != "" {
		return errors.New(refusal.Error)
	}
	if err := json.Unmarshal([]byte(last), v); err != nil {
		return fmt.Errorf("%s: keine Antwort (%v): %s", personTool, runErr, last)
	}
	return nil
}

// cmdPersonInvite: `laib person invite <user> --first F --last L --email E
// [--group family|admins] [--resend] [--signal +49…]`. The LLDAP user without a password, in
// the group; Authelia mails the set-password link, or, without SMTP, the
// link comes back to be passed on. Admins are invited by a human only.
func cmdPersonInvite(ctx context.Context, env *cliEnv, args []string) int {
	fs := flag.NewFlagSet("person invite", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	first, last, email := fs.String("first", "", ""), fs.String("last", "", ""), fs.String("email", "", "")
	group, resend, signal := fs.String("group", "", ""), fs.Bool("resend", false, ""), fs.String("signal", "", "")
	var user string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		user, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil || user == "" || fs.NArg() > 0 || *first == "" || *email == "" {
		return env.Fail(errors.New("usage: laib person invite <user> --first F --last L --email E [--group family|admins] [--resend] [--signal +49…]"))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if *group == "admins" && !policy.IsHuman(proposal.Hand(env.Actor)) {
		return cli.Refused(env, "Admins lädt nur ein Mensch an der Konsole ein (laib person invite "+user+" … --group admins): ein Admin-Zugang ist mehr als ein neuer Bewohner")
	}
	g, r := *group, "0"
	if g == "" {
		g = "-" // a new user goes to family; a resend keeps the group it has
	}
	if *resend {
		r = "1"
	}
	var inv Invite
	if *signal != "" { // the number first: one the loader refuses stops before the account
		if inv.Proposal, err = proposeSignal(ctx, env, st, user, *signal); err != nil {
			return env.Fail(err)
		}
	}
	if err := runPerson(ctx, env, &inv, "invite", user, *first, *last, *email, g, r); err != nil {
		return env.Fail(err)
	}
	until := inv.Expires
	if t, err := time.Parse(time.RFC3339, inv.Expires); err == nil {
		loc, _ := time.LoadLocation(st.Box.Timezone)
		until = t.In(loc).Format("02.01. 15:04")
	}
	switch {
	case inv.Mailed && *resend:
		inv.Message = fmt.Sprintf("Neuer Link für %s ist an %s unterwegs; er gilt bis %s.", inv.Name, inv.Email, until)
	case inv.Mailed:
		inv.Message = fmt.Sprintf("%s ist eingeladen: die Mail mit dem Link zum Passwort ist an %s unterwegs. Der Link gilt bis %s.", inv.Name, inv.Email, until)
	default:
		inv.Message = fmt.Sprintf("%s ist angelegt, aber die Box verschickt keine Mails: gib %s den Link weiter, etwa über Signal. Er gilt bis %s, danach „erneut senden“.", inv.Name, *first, until)
	}
	if inv.Proposal > 0 {
		inv.Message += fmt.Sprintf(" Die Signal-Nummer gilt nach der Freigabe von Vorschlag #%d durch einen Menschen.", inv.Proposal)
	}
	what := "invite"
	if *resend {
		what = "resend"
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "person", Service: st.Box.Roles.Identity, Result: "info", Detail: fmt.Sprintf("%s %s (mailed %v)", what, user, inv.Mailed)})
	if env.JSON {
		return cli.Emit(env, inv)
	}
	fmt.Fprintln(env.W(), inv.Message)
	if inv.Link != "" {
		fmt.Fprintln(env.W(), inv.Link)
	}
	return cli.ExitOK
}

// proposeSignal is people.<user>.signal as a proposal (#598), for invite
// --signal; the loader checks the number. `laib person signal` files the same
// path from laib-haushalt, behind the door (#677).
func proposeSignal(ctx context.Context, env *cliEnv, st *state.State, user, number string) (int, error) {
	return inbox.ProposeAt(ctx, env, st, "person", "Die Signal-Nummer von "+user+" eintragen (box.yml people)",
		map[string]*yaml.Node{"box.yml:people." + user + ".signal": {Kind: yaml.ScalarNode, Value: number}})
}

// Pending is one invited person who has not used the link yet.
type Pending struct {
	User      string `json:"user"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	InvitedAt string `json:"invited_at"`
}

// cmdPersonPending: `laib person pending`, the invited without a password yet.
func cmdPersonPending(ctx context.Context, env *cliEnv, args []string) int {
	var rows []Pending
	if err := runPerson(ctx, env, &rows, "pending"); err != nil {
		return env.Fail(err)
	}
	if rows == nil {
		rows = []Pending{}
	}
	if env.JSON {
		return cli.Emit(env, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(env.W(), "Niemand wartet auf sein Passwort.")
	}
	for _, p := range rows {
		fmt.Fprintf(env.W(), "%-16s %-24s %-32s eingeladen %s\n", p.User, p.Name, p.Email, p.InvitedAt)
	}
	return cli.ExitOK
}

// personArgs: `laib person invite|pending|signal …` is the verb person_invite,
// person_pending or person_signal, the names the MCP tools carry (laib_person_invite).
func personArgs(rest []string) []string {
	if len(rest) > 1 && rest[0] == "person" {
		return append([]string{"person_" + rest[1]}, rest[2:]...)
	}
	return rest
}
