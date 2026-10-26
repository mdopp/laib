package apply

import (
	"context"

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
