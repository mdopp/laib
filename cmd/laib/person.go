package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
)

// personTool is the identity template's tool (templates/auth/person.py,
// #360): it knows LLDAP and Authelia; laib runs it in the hooks image with
// that service's secrets. laib-haushalt serves laib person invite and
// pending (#1234); these are their kernel parts, the account and the read
// that need LLDAP's admin password, which the door never hands over.
const personTool = "person.py"

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

// cmdPersonInviteKern: `<user> <first> <last> <email> <family|admins|-> <0|1>`,
// the LLDAP user without a password, or with 1 its link again; the tool's
// answer as it gave it. Admins are invited by a human only, whoever asks
// laib-haushalt: the caller is the Auftrag's.
func cmdPersonInviteKern(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) != 6 || args[0] == "" || args[1] == "" || args[3] == "" || args[5] != "0" && args[5] != "1" {
		return env.Fail(errors.New("usage: laib person_invite_kern <user> <first> <last> <email> <family|admins|-> <0|1>"))
	}
	if args[4] == "admins" && !policy.IsHuman(proposal.Hand(env.Actor)) {
		return cli.Refused(env, "Admins lädt nur ein Mensch an der Konsole ein (laib person invite "+args[0]+" … --group admins): ein Admin-Zugang ist mehr als ein neuer Bewohner")
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	var inv map[string]any
	if err := runPerson(ctx, env, &inv, append([]string{"invite"}, args...)...); err != nil {
		return env.Fail(err)
	}
	what := map[string]string{"0": "invite", "1": "resend"}[args[5]]
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "person", Service: st.Box.Roles.Identity, Result: "info", Detail: fmt.Sprintf("%s %s (mailed %v)", what, args[0], inv["mailed"] == true)})
	return cli.Emit(env, inv)
}

// cmdPersonPendingKern: the invited without a password yet, as the tool lists them.
func cmdPersonPendingKern(ctx context.Context, env *cliEnv, _ []string) int {
	var rows []map[string]any
	if err := runPerson(ctx, env, &rows, "pending"); err != nil {
		return env.Fail(err)
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return cli.Emit(env, rows)
}

// personArgs: `laib person invite|pending|signal …` is the verb person_invite,
// person_pending or person_signal, the names the MCP tools carry (laib_person_invite).
func personArgs(rest []string) []string {
	if len(rest) > 1 && rest[0] == "person" {
		return append([]string{"person_" + rest[1]}, rest[2:]...)
	}
	return rest
}
