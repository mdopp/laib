package haushalt

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

// Invite is what person_invite answers (haus#56 codes against it).
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

// cmdPersonInvite: `laib person invite <user> --first F --last L --email E
// [--group family|admins] [--resend] [--signal +49…]`. The LLDAP user
// without a password, in the group; Authelia mails the set-password link,
// or, without SMTP, the link comes back to be passed on. The account is the
// kernel's part (person_invite_kern, #1234), which holds LLDAP's admin
// password and lets only a human invite an admin.
func cmdPersonInvite(ctx context.Context, env *Env, args []string) int {
	fs := flag.NewFlagSet("person invite", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	first, last, email := fs.String("first", "", ""), fs.String("last", "", ""), fs.String("email", "", "")
	group, resend, signal := fs.String("group", "", ""), fs.Bool("resend", false, ""), fs.String("signal", "", "")
	var user string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		user, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil || user == "" || fs.NArg() > 0 || *first == "" || *email == "" {
		return env.fail(errors.New("usage: laib person invite <user> --first F --last L --email E [--group family|admins] [--resend] [--signal +49…]"))
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
		if strings.ContainsAny(*signal, "\" \n") {
			return env.fail(fmt.Errorf("%q ist keine Signal-Nummer (+49…)", *signal))
		}
		id, _, err := env.Laib.Propose(ctx, env.Ceiling, "--path", "box.yml:people."+user+".signal="+*signal, "Die Signal-Nummer von "+user+" eintragen (box.yml people)")
		if err != nil {
			return env.fail(err)
		}
		inv.Proposal = id
	}
	if err := env.Laib.Kern(ctx, env.Auftrag, "laib_person_invite_kern", "", &inv, user, *first, *last, *email, g, r); err != nil {
		return env.kernFailed(err)
	}
	until := inv.Expires
	if t, err := time.Parse(time.RFC3339, inv.Expires); err == nil {
		loc, _ := time.LoadLocation(env.TZ)
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
	if env.JSON {
		return env.emit(inv)
	}
	fmt.Fprintln(env.Out, inv.Message)
	if inv.Link != "" {
		fmt.Fprintln(env.Out, inv.Link)
	}
	return exitOK
}

// Pending is one invited person who has not used the link yet.
type Pending struct {
	User      string `json:"user"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	InvitedAt string `json:"invited_at"`
}

// cmdPersonPending: `laib person pending`, the invited without a password
// yet; the kernel's part (person_pending_kern) asks LLDAP.
func cmdPersonPending(ctx context.Context, env *Env, _ []string) int {
	rows := []Pending{}
	if err := env.Laib.Kern(ctx, env.Auftrag, "laib_person_pending_kern", "", &rows); err != nil {
		return env.kernFailed(err)
	}
	if env.JSON {
		return env.emit(rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(env.Out, "Niemand wartet auf sein Passwort.")
	}
	for _, p := range rows {
		fmt.Fprintf(env.Out, "%-16s %-24s %-32s eingeladen %s\n", p.User, p.Name, p.Email, p.InvitedAt)
	}
	return exitOK
}
