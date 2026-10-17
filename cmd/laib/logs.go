package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/sysd"
)

// cmdLogs reads journald for every unit of a service, secrets redacted
// (ADR 0003: journald is the agent).
func cmdLogs(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return env.Fail(errors.New("usage: laib logs <service> [--since 1h] [--lines 200]"))
	}
	_, svc, err := envelope.LoadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	since, lines := "1h", "200"
	for i := 1; i+1 < len(args); i += 2 {
		switch args[i] {
		case "--since":
			since = args[i+1]
		case "--lines":
			lines = args[i+1]
		}
	}
	sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	out := map[string]string{}
	units := append(svc.UnitNames(), svc.Oneshots()...) // a timer's failed run is read here (#195)
	for _, unit := range units {
		text, _ := sysd.Run(ctx, "journalctl", "--user", "-u", unit, "--since", "-"+since, "-n", lines, "--no-pager", "-o", "short-iso")
		out[unit] = render.Redact(render.Output{"l": text}, sec)["l"]
	}
	if env.JSON {
		return cli.Emit(env, out)
	}
	for _, unit := range units {
		fmt.Fprintf(env.W(), "── %s ──\n%s\n", unit, out[unit])
	}
	return cli.ExitOK
}
