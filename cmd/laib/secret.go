package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
)

// cmdSecret: `laib secret set <NAME>` reads the value from stdin (one line)
// so it never lands in a shell history; `laib secret list` prints names only.
func cmdSecret(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return env.Fail(errors.New("usage: laib secret set <NAME> (value on stdin) | list | rotate <NAME>"))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return env.Fail(err)
	}
	switch args[0] {
	case "list":
		names := make([]string, 0, len(sec))
		for n := range sec {
			names = append(names, n)
		}
		sort.Strings(names)
		if env.JSON {
			return cli.Emit(env, names)
		}
		for _, n := range names {
			fmt.Fprintln(env.W(), n)
		}
		return cli.ExitOK
	case "set":
		if len(args) != 2 {
			return env.Fail(errors.New("usage: laib secret set <NAME>"))
		}
		if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "apply") {
			return cli.Refused(env, env.Actor+" may not set secrets")
		}
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		val := strings.TrimRight(line, "\r\n")
		if val == "" {
			return env.Fail(errors.New("empty value on stdin"))
		}
		if _, err := secrets.EnsureKey(env.Paths.Key); err != nil {
			return env.Fail(err)
		}
		sec[args[1]] = val
		if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
			return env.Fail(err)
		}
		journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "secret", Result: "info", Detail: "set " + args[1]})
		fmt.Fprintln(env.W(), "gesetzt:", args[1])
		return cli.ExitOK
	case "rotate":
		// #17: a new value, applied to every service that references it, so
		// the post-deploy hooks re-seed the apps. Humans only.
		if len(args) != 2 {
			return env.Fail(errors.New("usage: laib secret rotate <NAME>"))
		}
		if proposal.Hand(env.Actor) != "human" {
			return cli.Refused(env, "only a human rotates secrets")
		}
		name := args[1]
		if _, ok := sec[name]; !ok {
			return env.Fail(fmt.Errorf("no secret %s", name))
		}
		external, bound, inApp := false, false, false
		for _, d := range render.SecretNames(st) {
			if d.Name == name {
				external, bound, inApp = external || d.External, bound || d.DataBound, inApp || d.Rotate
			}
		}
		if external {
			return env.Fail(fmt.Errorf("%s is external: obtain a new value and use `laib secret set`", name))
		}
		if bound && !inApp { // nothing changes: the old value keeps working (#353)
			return env.Fail(fmt.Errorf("%s is bound to its app's data: the app reads it only when it creates its data, so a new value here locks it out. Change it inside the app first (its admin page, or ALTER ROLE for a database), then `laib secret set %s` with that same value and `laib apply`", name, name))
		}
		why := "secret rotate " + name
		set := func(v string) ([]string, int) { // #665: a data-bound one through its hook, inside the app
			if bound {
				return apply.RotateInApp(ctx, env, st, name, v, why)
			}
			return apply.SetSecret(ctx, env, st, name, v, why)
		}
		old, value := sec[name], secrets.Generate()
		touched, code := set(value)
		now, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
		switch {
		case code != cli.ExitOK && now[name] != old: // #665: red after the change rotates back, once
			fmt.Fprintf(env.Errw(), "laib: %s nach der Rotation rot (%s): zurück auf den alten Wert\n", name, strings.Join(touched, ", "))
			why = "secret rotate " + name + ": zurück"
			if _, c := set(old); c != cli.ExitOK {
				fmt.Fprintf(env.Errw(), "laib: auch zurück rot: laib diag, laib logs %s\n", strings.Join(touched, " "))
			}
			return code
		case code != cli.ExitOK:
			fmt.Fprintf(env.Errw(), "laib: %s nicht rotiert, der alte Wert gilt weiter: laib logs %s\n", name, strings.Join(touched, " "))
			return code
		}
		fmt.Fprintf(env.W(), "rotiert: %s · betroffen: %s\n", name, strings.Join(touched, ", "))
		return code
	}
	return env.Fail(fmt.Errorf("unknown secret subcommand %q", args[0]))
}
