package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
)

// served is a verb that a program beside laib serves (ADR 0026, #598):
// /var/opt/laib/laib-<prog>, LAIB_<PROG> in tests. By the time it runs,
// the kernel has checked hand, tier and verb (mcp.Call) and cli.Guard has
// taken --help. The program gets "<verb> <args>", the actor, the paths,
// stdin; its output and exit code are the verb's.
func served(prog, verb, tier string) func(context.Context, *cliEnv, []string) int {
	return func(ctx context.Context, env *cliEnv, args []string) int {
		return door(ctx, env, prog, tier, append([]string{verb}, args...))
	}
}

// door runs the program. It holds no token: what it may call back is laib
// itself as the actor, up to propose or the verb's own lower tier
// (LAIB_CEILING), and the dial applies nothing it proposes. What it would
// journal it hands back on fd 3, one journal.Entry per line; the kernel
// writes it as the caller and under the verb's name.
func door(ctx context.Context, env *cliEnv, prog, tier string, argv []string) int {
	bin := cmp.Or(os.Getenv("LAIB_"+strings.ToUpper(prog)), filepath.Join(binDir(), "laib-"+prog))
	ceiling := "propose"
	if policy.Index(tier) < policy.Index(ceiling) {
		ceiling = tier
	}
	self, _ := os.Executable()
	verb := argv[0]
	if env.JSON {
		argv = append([]string{"--json"}, argv...)
	}
	r, w, err := os.Pipe()
	if err != nil {
		return env.Fail(err)
	}
	defer r.Close()
	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Env = append(os.Environ(), "LAIB_ACTOR="+env.Actor, "LAIB_CEILING="+ceiling, "LAIB_KERNEL="+self, "LAIB_JOURNAL_FD=3",
		"LAIB_HOME="+env.Paths.Home, "LAIB_STATE="+env.Paths.State, "LAIB_QUADLET="+env.Paths.Quadlet, "LAIB_SYSTEMD="+env.Paths.Systemd)
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.ExtraFiles = env.Stdin(), env.W(), env.Errw(), []*os.File{w}
	var lines []journal.Entry
	read := make(chan struct{})
	go func() {
		defer close(read)
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			var e journal.Entry
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				lines = append(lines, e)
			}
		}
	}()
	err = cmd.Run()
	w.Close()
	<-read
	if len(lines) > 0 {
		st, _ := cli.LoadState(env)
		for _, e := range lines {
			e.Actor, e.Verb = env.Actor, verb
			cli.Record(env, st, e)
		}
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return env.Fail(err)
	}
	return cli.ExitOK
}
