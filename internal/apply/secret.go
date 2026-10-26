package apply

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

// SetSecret is a new value for a secret under the apply lock (#17, #696):
// into secrets.age, committed on main (#328), journaled by its name —
// never its value — and every service that uses it applied, which is the
// restart that hands it over. `laib secret rotate` and the box page's
// „Schlüssel hinterlegen“ both go this way.
func SetSecret(ctx context.Context, env *cli.Env, st *state.State, name, value, why string) ([]string, int) {
	unlock, code := TakeLock(env, st, "secret")
	if code != cli.ExitOK {
		return nil, code
	}
	defer unlock()
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err == nil {
		_, err = secrets.EnsureKey(env.Paths.Key)
	}
	if err == nil {
		sec[name] = value
		err = secrets.Save(env.Paths.Secrets, env.Paths.Key, sec)
	}
	if err == nil {
		err = proposal.CommitSecrets(ctx, env.Paths.State, why)
	}
	if err != nil {
		return nil, env.Fail(err)
	}
	journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "secret", Result: "info", Detail: why})
	users := render.SecretUsers(st, name)
	for _, svc := range users {
		if c := ApplyHeld(ctx, env, st, svc, why); c != cli.ExitOK {
			code = c
		}
	}
	return users, code
}

// RotateInApp (#665) is a rotation the app takes first: a dataBound secret
// its template marks rotate. The new value waits in secrets.age as
// <NAME>.next; the services that declare it apply (their hooks run though
// nothing they render changed) and the hook gets it as LAIB_ROTATE_<NAME>,
// changes it inside the app and hands it back, which is the value taken
// (ApplyPlan). Without a hand-back the old value stays and the pending one
// goes. Then the other services that use it apply.
func RotateInApp(ctx context.Context, env *cli.Env, st *state.State, name, value, why string) ([]string, int) {
	unlock, code := TakeLock(env, st, "secret")
	if code != cli.ExitOK {
		return nil, code
	}
	defer unlock()
	var owners, users []string
	for _, svc := range render.SecretUsers(st, name) {
		if slices.ContainsFunc(st.Services[svc].Contract.Secrets, func(s state.Secret) bool { return s.Name == name && s.Rotate }) {
			owners = append(owners, svc)
		} else {
			users = append(users, svc)
		}
	}
	if err := pend(ctx, env, name, &value, why); err != nil {
		return nil, env.Fail(err)
	}
	journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "secret", Result: "info", Detail: why + " (in der App: " + strings.Join(owners, ", ") + ")"})
	WritePending(env.Paths, owners, why)
	for _, svc := range owners {
		if c := ApplyHeld(ctx, env, st, svc, why); c != cli.ExitOK {
			code = c
		}
	}
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return owners, env.Fail(err)
	}
	if _, left := sec[name+".next"]; left {
		if err := pend(ctx, env, name, nil, why+": von der App nicht übernommen"); err != nil {
			return owners, env.Fail(err)
		}
		return owners, cmp.Or(code, cli.ExitRed)
	}
	for _, svc := range users {
		if c := ApplyHeld(ctx, env, st, svc, why); c != cli.ExitOK {
			code = c
		}
	}
	return append(owners, users...), code
}

// pend sets <name>.next to value, or drops it (nil), committed on main.
func pend(ctx context.Context, env *cli.Env, name string, value *string, why string) error {
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return err
	}
	if delete(sec, name+".next"); value != nil {
		sec[name+".next"] = *value
	}
	if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
		return err
	}
	return proposal.CommitSecrets(ctx, env.Paths.State, why)
}
