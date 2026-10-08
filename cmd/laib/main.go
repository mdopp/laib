// Command laib is the one binary: CLI and daemon. The verb table below is the
// single source for `laib help`, the MCP tool list and the tier check.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/mdopp/laib/internal/boxfs"
)

// Version is set by the release build.
var Version = "dev"

// verb is one CLI command. tier is the policy tier it needs (docs/STATE.md).
type verb struct {
	name string
	tier string
	help string
	run  func(ctx context.Context, env *cliEnv, args []string) int
}

// cliEnv is what every verb gets.
type cliEnv struct {
	paths boxfs.Paths
	json  bool
	actor string
}

var verbs = []verb{
	{"status", "read", "one line per service; first line is the readiness state", cmdStatus},
	{"plan", "read", "render the state and diff it against the box", cmdPlan},
	{"apply", "apply", "snapshot, write, reload, restart, verify", cmdApply},
	{"journal", "read", "show journal entries (--since 24h)", cmdJournal},
	{"context", "read", "starter bundle for an agent session", cmdContext},
	{"template", "read", "lint <dir> | list", cmdTemplate},
	{"propose", "propose", "commit the working tree to a proposal branch and an inbox entry", cmdPropose},
	{"approve", "apply", "merge a proposal and apply it (never your own)", cmdApprove},
	{"reject", "apply", "drop a proposal", cmdReject},
	{"adopt", "propose", "adopt an unmanaged Quadlet folder into services/", cmdAdopt},
	{"ignore", "propose", "record an unmanaged unit as deliberately unmanaged", cmdIgnore},
	{"secret", "apply", "set <NAME> (value on stdin) | list", cmdSecret},
	{"init", "apply", "first setup: key, state repo skeleton", cmdInit},
	{"serve", "read", "daemon: /ready, the three pages", cmdServe},
	{"version", "read", "print the version", cmdVersion},
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	env := &cliEnv{paths: boxfs.Default(), actor: os.Getenv("LAIB_ACTOR")}
	if env.actor == "" {
		env.actor = "human:" + os.Getenv("USER")
	}
	var rest []string
	for _, a := range args {
		if a == "--json" {
			env.json = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help" {
		usage()
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for _, v := range verbs {
		if v.name == rest[0] {
			return v.run(ctx, env, rest[1:])
		}
	}
	fmt.Fprintf(os.Stderr, "laib: unknown verb %q\n\n", rest[0])
	usage()
	return 1
}

func usage() {
	fmt.Fprintln(os.Stderr, "laib — the control plane for one home box\n\nVerbs (tier in brackets):")
	names := make([]string, 0, len(verbs))
	for _, v := range verbs {
		names = append(names, v.name)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, v := range verbs {
			if v.name == n {
				fmt.Fprintf(os.Stderr, "  %-10s [%s]  %s\n", v.name, v.tier, v.help)
			}
		}
	}
	fmt.Fprintln(os.Stderr, "\nFlags: --json on every verb. Env: LAIB_HOME, LAIB_STATE, LAIB_QUADLET, LAIB_ACTOR.")
}

func cmdVersion(_ context.Context, env *cliEnv, _ []string) int {
	if env.json {
		return emit(env, map[string]string{"version": Version})
	}
	fmt.Println("laib", Version)
	return 0
}

// emit prints JSON and returns 0.
func emit(env *cliEnv, v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail(err)
	}
	return 0
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "laib:", strings.TrimSpace(err.Error()))
	return 1
}
