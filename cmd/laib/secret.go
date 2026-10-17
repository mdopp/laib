package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/run"
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
		external := false
		for _, d := range render.SecretNames(st) {
			if d.Name == name && d.External {
				external = true
			}
		}
		if external {
			return env.Fail(fmt.Errorf("%s is external: obtain a new value and use `laib secret set`", name))
		}
		sec[name] = secrets.Generate()
		if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, sec); err != nil {
			return env.Fail(err)
		}
		journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "secret", Result: "info", Detail: "rotate " + name})
		// Every service that references the secret, or declares it, gets applied.
		var touched []string
		for _, svcName := range st.ServiceNames() {
			svc := st.Services[svcName]
			uses := false
			for _, v := range svc.Values {
				if v.Secret == name {
					uses = true
				}
			}
			for _, d := range svc.Contract.Secrets {
				if d.Name == name {
					uses = true
				}
			}
			if svc.Contract.OIDC != nil && strings.HasPrefix(name, "OIDC_") {
				uses = true
			}
			if uses {
				touched = append(touched, svcName)
			}
		}
		fmt.Fprintf(env.W(), "rotiert: %s · betroffen: %s\n", name, strings.Join(touched, ", "))
		code := cli.ExitOK
		for _, svcName := range st.Order(touched) {
			if c := run.Apply(ctx, env, st, svcName, "secret rotate "+name); c != cli.ExitOK {
				code = c
			}
		}
		return code
	}
	return env.Fail(fmt.Errorf("unknown secret subcommand %q", args[0]))
}
