package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

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
		return fail(errors.New("usage: laib secret set <NAME> (value on stdin) | list | rotate <NAME>"))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	sec, err := secrets.Load(env.paths.Secrets, env.paths.Key)
	if err != nil {
		return fail(err)
	}
	switch args[0] {
	case "list":
		names := make([]string, 0, len(sec))
		for n := range sec {
			names = append(names, n)
		}
		sort.Strings(names)
		if env.json {
			return emit(env, names)
		}
		for _, n := range names {
			fmt.Fprintln(env.w(), n)
		}
		return exitOK
	case "set":
		if len(args) != 2 {
			return fail(errors.New("usage: laib secret set <NAME>"))
		}
		if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "apply") {
			fmt.Fprintf(os.Stderr, "laib: %s may not set secrets\n", env.actor)
			return exitRefused
		}
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		val := strings.TrimRight(line, "\r\n")
		if val == "" {
			return fail(errors.New("empty value on stdin"))
		}
		if _, err := secrets.EnsureKey(env.paths.Key); err != nil {
			return fail(err)
		}
		sec[args[1]] = val
		if err := secrets.Save(env.paths.Secrets, env.paths.Key, sec); err != nil {
			return fail(err)
		}
		journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "secret", Result: "info", Detail: "set " + args[1]})
		fmt.Fprintln(env.w(), "gesetzt:", args[1])
		return exitOK
	case "rotate":
		// #17: a new value, applied to every service that references it, so
		// the post-deploy hooks re-seed the apps. Humans only.
		if len(args) != 2 {
			return fail(errors.New("usage: laib secret rotate <NAME>"))
		}
		if proposal.Hand(env.actor) != "human" {
			return exitRefusedMsg(env, "only a human rotates secrets")
		}
		name := args[1]
		if _, ok := sec[name]; !ok {
			return fail(fmt.Errorf("no secret %s", name))
		}
		external := false
		for _, d := range render.SecretNames(st) {
			if d.Name == name && d.External {
				external = true
			}
		}
		if external {
			return fail(fmt.Errorf("%s is external: obtain a new value and use `laib secret set`", name))
		}
		sec[name] = secrets.Generate()
		if err := secrets.Save(env.paths.Secrets, env.paths.Key, sec); err != nil {
			return fail(err)
		}
		journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "secret", Result: "info", Detail: "rotate " + name})
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
		fmt.Fprintf(env.w(), "rotiert: %s · betroffen: %s\n", name, strings.Join(touched, ", "))
		code := exitOK
		for _, svcName := range st.Order(touched) {
			if c := runApply(ctx, env, st, svcName, "secret rotate "+name); c != exitOK {
				code = c
			}
		}
		return code
	}
	return fail(fmt.Errorf("unknown secret subcommand %q", args[0]))
}
