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
	"github.com/mdopp/laib/internal/secrets"
)

// cmdSecret: `laib secret set <NAME>` reads the value from stdin (one line)
// so it never lands in a shell history; `laib secret list` prints names only.
func cmdSecret(_ context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return fail(errors.New("usage: laib secret set <NAME> (value on stdin) | list"))
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
			fmt.Println(n)
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
		fmt.Println("gesetzt:", args[1])
		return exitOK
	}
	return fail(fmt.Errorf("unknown secret subcommand %q", args[0]))
}
