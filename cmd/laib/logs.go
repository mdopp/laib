package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

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
		return env.Fail(errors.New("usage: laib logs <service> [--since 1h] [--lines 200] [--priority warning] [--grep muster]"))
	}
	_, svc, err := envelope.LoadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	since, lines, prio, grep := "1h", "200", "", ""
	for i := 1; i+1 < len(args); i += 2 {
		switch args[i] {
		case "--since":
			since = args[i+1]
		case "--lines":
			lines = args[i+1]
		case "--priority": // journalctl -p: this level and worse (#551, the log review)
			prio = args[i+1]
		case "--grep": // #916: filtered before the cut, so --lines counts matches
			grep = args[i+1]
		}
	}
	if prio != "" && prio != "err" && prio != "warning" {
		return env.Fail(errors.New("--priority: err oder warning"))
	}
	n, err := strconv.Atoi(lines)
	if err != nil || n < 1 {
		return env.Fail(errors.New("--lines: eine Zahl ab 1"))
	}
	var re *regexp.Regexp
	if grep != "" {
		if strings.ToLower(grep) == grep { // journalctl's smart case: all lower-case matches any case
			grep = "(?i)" + grep
		}
		if re, err = regexp.Compile(grep); err != nil {
			return env.Fail(fmt.Errorf("--grep: %w", err))
		}
	}
	sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	out := map[string]string{}
	units := append(svc.UnitNames(), svc.Oneshots()...) // a timer's failed run is read here (#195)
	for _, unit := range units {
		jargs := []string{"--user", "-u", unit, "--since", "-" + since, "--no-pager", "-o", "short-iso"}
		if re == nil {
			jargs = append(jargs, "-n", lines)
		}
		if prio != "" {
			jargs = append(jargs, "-p", prio)
		}
		text, _ := sysd.Run(ctx, "journalctl", jargs...)
		text = render.Redact(render.Output{"l": text}, sec)["l"] // before the filter: a pattern cannot probe a secret
		if re != nil {                                           // the whole window, filtered here, then the newest n matches
			var hits []string
			for _, l := range strings.Split(text, "\n") {
				if re.MatchString(l) {
					hits = append(hits, l)
				}
			}
			text = strings.Join(hits[max(0, len(hits)-n):], "\n")
		}
		out[unit] = text
	}
	if env.JSON {
		return cli.Emit(env, out)
	}
	for _, unit := range units {
		fmt.Fprintf(env.W(), "── %s ──\n%s\n", unit, out[unit])
	}
	return cli.ExitOK
}
