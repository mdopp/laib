package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// The operator verbs (ADR 0013). Class 1 (look) is unbounded: inspect,
// read, exec for looking. Class 2 (correct) runs inside the envelope:
// snapshot once per service per hour, journal with output, a budget, verify
// where it applies. Class 3 (irreversible) is not here; it is a proposal.

// RepairBudget: mutating operator actions per service per hour before laib
// stops the hand and asks a human.
const RepairBudget = 8

// containerName is Quadlet's default name for a .container unit.
func containerName(unit string) string { return "systemd-" + strings.TrimSuffix(unit, ".service") }

// loadSvc loads the state and the named service in it.
func loadSvc(env *cliEnv, name string) (*state.State, *state.Service, error) {
	st, err := cli.LoadState(env)
	if err != nil {
		return nil, nil, err
	}
	svc, ok := st.Services[name]
	if !ok {
		return nil, nil, fmt.Errorf("unknown service %q", name)
	}
	return st, svc, nil
}

// dataPaths expands a service's declared data paths.
func dataPaths(st *state.State, svc *state.Service) []string {
	var out []string
	for _, d := range svc.Contract.Data {
		out = append(out, strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir))
	}
	return out
}

// snapshotPaths are the data paths worth a pre-repair snapshot: the ones
// the service backs up (backup: false marks caches and bulk media).
func snapshotPaths(st *state.State, svc *state.Service) []string {
	var out []string
	for _, d := range svc.Contract.Data {
		if d.Backup != nil && !*d.Backup {
			continue
		}
		out = append(out, strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir))
	}
	return out
}

// jail opens the data path the file lies in as an os.Root and returns the
// path inside it. Every open, mkdir and rename then goes through the root,
// so no component — a symlink planted by a container, a `..` — can leave
// it (#35: the old lexical check let a not-yet-existing file under a
// symlinked directory through). The root is the service's declared data
// path (or its whole data dir when it declares none).
func jail(st *state.State, svc *state.Service, p string) (*os.Root, string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, "", err
	}
	abs = filepath.Clean(abs)
	allowed := dataPaths(st, svc)
	if len(allowed) == 0 {
		allowed = []string{filepath.Join(st.Box.DataDir, svc.Name)}
	}
	for _, a := range allowed {
		a = filepath.Clean(a)
		if abs != a && !strings.HasPrefix(abs, a+string(filepath.Separator)) {
			continue
		}
		rel, err := filepath.Rel(a, abs)
		if err != nil {
			return nil, "", err
		}
		root, err := os.OpenRoot(a)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", a, err)
		}
		return root, rel, nil
	}
	return nil, "", fmt.Errorf("%s is outside %s's data paths (%s)", p, svc.Name, strings.Join(allowed, ", "))
}

// ownContainers are the containers the service's units run.
func ownContainers(svc *state.Service) []string {
	var out []string
	for _, u := range svc.UnitNames() {
		if strings.HasSuffix(u, ".service") && !strings.HasSuffix(u, "-pod.service") {
			out = append(out, containerName(u))
		}
	}
	return out
}

// envelope is what wraps every class-2 action: the budget, a pre-repair
// snapshot of the service's backed-up data once per service per hour, and
// a journal line. It fails closed (#35): a journal it cannot read, a
// missing backup target or a failed snapshot refuse the action — unless
// policy.yml says `envelope.snapshot: optional`.
func envelope(ctx context.Context, env *cliEnv, st *state.State, svc *state.Service, verb, detail string) int {
	since := time.Now().Add(-time.Hour)
	recent, skipped, err := journal.ReadCounted(env.Paths.Journal, since)
	if err != nil {
		fmt.Fprintf(env.Errw(), "laib: das Journal ist nicht lesbar, deshalb kein %s: %v\n", verb, err)
		return cli.ExitRefused
	}
	if skipped > 0 {
		fmt.Fprintf(env.Errw(), "laib: %d unlesbare Journalzeilen übersprungen\n", skipped)
	}
	used, snapped := 0, false
	for _, e := range recent {
		if e.Service != svc.Name {
			continue
		}
		switch e.Verb {
		case "exec", "write", "restart":
			used++
		case "apply": // the Operator's own applies (#286); a human's never count
			if e.Actor == env.Actor {
				used++
			}
		case "snapshot":
			if e.Result == "info" {
				snapped = true
			}
		}
	}
	if used >= RepairBudget {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: verb, Service: svc.Name, Result: "red", Detail: fmt.Sprintf("Budget erschöpft (%d/h). Ein Mensch muss ran: %s", RepairBudget, detail)})
		fmt.Fprintf(env.Errw(), "laib: Budget für %s erschöpft (%d Eingriffe in der letzten Stunde). laib escalate %s \"…\" ist der Weg weiter.\n", svc.Name, RepairBudget, svc.Name)
		return cli.ExitRefused
	}
	paths := snapshotPaths(st, svc)
	optional := st.Policy.Envelope.Snapshot == "optional"
	if snapped || len(paths) == 0 {
		return cli.ExitOK
	}
	if st.Box.Backup.Repo == "" {
		if optional {
			return cli.ExitOK
		}
		fmt.Fprintf(env.Errw(), "laib: kein %s ohne Sicherung: box.yml hat kein backup.repo, also keinen Snapshot vor dem Eingriff (policy.yml envelope.snapshot: optional hebt das auf)\n", verb)
		return cli.ExitRefused
	}
	_, pw, err := backupPrep(env)
	if err == nil {
		_, err = resticRun(ctx, st, pw, append([]string{"backup", "--tag", "pre-repair", "--tag", svc.Name, "--exclude-caches"}, paths...)...)
	}
	if err != nil {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "snapshot", Service: svc.Name, Result: "red", Detail: err.Error()})
		if optional {
			return cli.ExitOK
		}
		fmt.Fprintf(env.Errw(), "laib: kein %s, der Snapshot vorher ist fehlgeschlagen: %v\n", verb, err)
		return cli.ExitRefused
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "snapshot", Service: svc.Name, Result: "info", Detail: "pre-repair: " + strings.Join(paths, " ")})
	// pre-repair snapshots are for the next hours, not for the archive
	if _, err := resticRun(ctx, st, pw, "forget", "--tag", "pre-repair", "--keep-within", "7d", "--prune"); err != nil {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "snapshot", Service: svc.Name, Result: "red", Detail: "forget pre-repair: " + err.Error()})
	}
	return cli.ExitOK
}

func cmdInspect(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return env.Fail(errors.New("usage: laib inspect <service>"))
	}
	st, svc, err := loadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	out := map[string]any{"service": svc.Name, "contract": svc.Contract}
	units := map[string]string{}
	containers := map[string]string{}
	for _, u := range svc.UnitNames() {
		show, _ := sysd.Run(ctx, "systemctl", "--user", "show", "-p", "ActiveState,SubState,ExecMainStatus,NRestarts,ActiveEnterTimestamp", u)
		units[u] = show
		if strings.HasSuffix(u, ".service") && !strings.HasSuffix(u, "-pod.service") {
			ins, _ := sysd.Run(ctx, "podman", "inspect", "--format", "{{.State.Status}} exit={{.State.ExitCode}} image={{.ImageName}} started={{.State.StartedAt}} mounts={{range .Mounts}}{{.Source}}→{{.Destination}} {{end}}", containerName(u))
			containers[containerName(u)] = ins
		}
	}
	out["units"] = units
	out["containers"] = containers
	files := map[string]string{}
	for _, p := range dataPaths(st, svc) {
		ls, _ := sysd.Run(ctx, "ls", "-lnA", "--time-style=+%F", p)
		files[p] = ls
	}
	out["data"] = files
	if h := svc.Contract.Health.HTTP; h != "" {
		client := &http.Client{Timeout: 5 * time.Second}
		if resp, err := client.Get(h); err != nil {
			out["health"] = err.Error()
		} else {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
			resp.Body.Close()
			out["health"] = resp.Status + " " + strings.TrimSpace(string(b))
		}
	}
	sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	logs := map[string]string{}
	for _, u := range svc.UnitNames() {
		text, _ := sysd.Run(ctx, "journalctl", "--user", "-u", u, "-n", "100", "--no-pager", "-o", "short-iso")
		logs[u] = render.Redact(render.Output{"l": text}, sec)["l"]
	}
	out["logs"] = logs
	if env.JSON {
		return cli.Emit(env, out)
	}
	fmt.Fprintf(env.W(), "# %s\n", svc.Name)
	for u, s := range units {
		fmt.Fprintf(env.W(), "## unit %s\n%s\n", u, s)
	}
	for c, s := range containers {
		fmt.Fprintf(env.W(), "## container %s\n%s\n", c, s)
	}
	for p, s := range files {
		fmt.Fprintf(env.W(), "## data %s\n%s\n", p, s)
	}
	if h, ok := out["health"]; ok {
		fmt.Fprintf(env.W(), "## health\n%v\n", h)
	}
	for u, s := range logs {
		fmt.Fprintf(env.W(), "## logs %s\n%s\n", u, s)
	}
	return cli.ExitOK
}

func cmdRead(_ context.Context, env *cliEnv, args []string) int {
	if len(args) < 2 {
		return env.Fail(errors.New("usage: laib read <service> <path>"))
	}
	st, svc, err := loadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	root, rel, err := jail(st, svc, args[1])
	if err != nil {
		return env.Fail(err)
	}
	defer root.Close()
	b, err := root.ReadFile(rel)
	if err != nil {
		return env.Fail(err)
	}
	p := args[1]
	if len(b) > 1<<20 {
		b = b[:1<<20]
	}
	sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	text := render.Redact(render.Output{"f": string(b)}, sec)["f"]
	if env.JSON {
		return cli.Emit(env, map[string]string{"path": p, "content": text})
	}
	fmt.Fprint(env.W(), text)
	return cli.ExitOK
}

func cmdWrite(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 2 {
		return env.Fail(errors.New("usage: laib write <service> <path>   (content on stdin)"))
	}
	st, svc, err := loadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "apply") {
		return exitRefusedMsg(env, env.Actor+" may not write")
	}
	root, rel, err := jail(st, svc, args[1])
	if err != nil {
		return env.Fail(err)
	}
	defer root.Close()
	p := args[1]
	content, err := io.ReadAll(env.Stdin())
	if err != nil {
		return env.Fail(err)
	}
	if code := envelope(ctx, env, st, svc, "write", p); code != cli.ExitOK {
		return code
	}
	old, _ := root.ReadFile(rel)
	if dir := filepath.Dir(rel); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return env.Fail(fmt.Errorf("%s: %w", p, err))
		}
	}
	if err := boxfs.WriteAtomicIn(root, rel+".laib-tmp", rel, content, 0o644); err != nil {
		return env.Fail(fmt.Errorf("%s: %w", p, err))
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "write", Service: svc.Name, Result: "info", Detail: fmt.Sprintf("%s (%d → %d Bytes)", p, len(old), len(content))})
	fmt.Fprintln(env.W(), "geschrieben:", p)
	return cli.ExitOK
}

func cmdExec(ctx context.Context, env *cliEnv, args []string) int {
	// laib exec <service> [--container name] -- cmd args…
	if len(args) < 1 {
		return env.Fail(errors.New("usage: laib exec <service> [--container <name>] -- <cmd…>"))
	}
	st, svc, err := loadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "apply") {
		return exitRefusedMsg(env, env.Actor+" may not exec")
	}
	container, cmd := containerArgs(args[1:])
	if len(cmd) == 0 {
		return env.Fail(errors.New("no command after --"))
	}
	container, err = pickContainer(svc, container)
	if err != nil {
		return exitRefusedMsg(env, err.Error())
	}
	if code := envelope(ctx, env, st, svc, "exec", strings.Join(cmd, " ")); code != cli.ExitOK {
		return code
	}
	out, entry, err := runIn(ctx, env, svc, "exec", container, cmd)
	cli.Record(env, st, entry)
	if env.JSON {
		return cli.Emit(env, map[string]any{"container": container, "output": out, "ok": err == nil})
	}
	fmt.Fprintln(env.W(), out)
	if err != nil {
		return cli.ExitRed
	}
	return cli.ExitOK
}

func cmdRestart(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 1 {
		return env.Fail(errors.New("usage: laib restart <service>"))
	}
	st, svc, err := loadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "apply") {
		return exitRefusedMsg(env, env.Actor+" may not restart")
	}
	if code := envelope(ctx, env, st, svc, "restart", "restart"); code != cli.ExitOK {
		return code
	}
	for _, u := range svc.UnitNames() {
		if err := sysd.Restart(ctx, u); err != nil {
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "restart", Service: svc.Name, Result: "red", Detail: u + ": " + err.Error()})
			return cli.ExitRed
		}
	}
	checks := apply.Verify(ctx, st, []string{svc.Name})
	green := true
	var bad []string
	for _, c := range checks {
		if !c.OK {
			green = false
			bad = append(bad, c.What+" "+c.Detail)
		}
	}
	entry := journal.Entry{Actor: env.Actor, Verb: "restart", Service: svc.Name, Result: "green"}
	if !green {
		entry.Result, entry.Detail = "red", strings.Join(bad, "; ")
	}
	cli.Record(env, st, entry)
	if env.JSON {
		return cli.Emit(env, map[string]any{"checks": checks, "green": green})
	}
	if green {
		fmt.Fprintln(env.W(), "grün:", svc.Name)
		return cli.ExitOK
	}
	fmt.Fprintln(env.W(), "rot:", entry.Detail)
	return cli.ExitRed
}

// cmdEscalate hands a service to the next hand: a context bundle under
// /mnt/data/laib/escalations and a red journal line that reaches the
// webhook, so the Werkstatt's claude hand (or the human) picks it up.
func cmdEscalate(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 2 {
		return env.Fail(errors.New("usage: laib escalate <service> \"<was ich weiß>\""))
	}
	st, svc, err := loadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	dir := filepath.Join(env.Paths.Home, "escalations")
	os.MkdirAll(dir, 0o755)
	name := time.Now().UTC().Format("2006-01-02T15-04-05") + "-" + svc.Name + ".md"
	var b strings.Builder
	fmt.Fprintf(&b, "# Eskalation %s · von %s\n\n%s\n\n## inspect\n\n", svc.Name, env.Actor, strings.Join(args[1:], " "))
	// the bundle through a buffer (#43): a pipe nobody reads blocked here
	// at 64 KiB while mcpMu was held, and every agent hung with it
	var ins bytes.Buffer
	cmdInspect(ctx, &cliEnv{Paths: env.Paths, Actor: env.Actor, MCP: env.MCP, Out: &ins, ErrOut: env.ErrOut}, []string{svc.Name})
	b.Write(ins.Bytes())
	es, _ := journal.Read(env.Paths.Journal, time.Now().Add(-24*time.Hour))
	b.WriteString("\n## Journal 24h\n\n")
	for _, e := range es {
		if e.Service == svc.Name || e.Service == "" {
			fmt.Fprintf(&b, "- %s %s %s %s %s %s\n", e.T.Format("15:04"), e.Actor, e.Verb, e.Result, e.Reason, e.Detail)
		}
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		return env.Fail(err)
	}
	// with an intake the bundle becomes a ticket something reads (#56)
	detail := p
	if url, ok, err := githubIssue(ctx, env, st, "Eskalation "+svc.Name+": "+cli.Tail(strings.Join(args[1:], " "), 80), b.String(), []string{"needs-claude"}); err != nil {
		detail = p + " (Intake rot: " + err.Error() + ")"
	} else if ok {
		detail = url + " (" + p + ")"
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "escalate", Service: svc.Name, Result: "red", Reason: strings.Join(args[1:], " "), Detail: detail})
	if env.JSON {
		return cli.Emit(env, map[string]string{"file": p, "issue": strings.TrimSuffix(strings.Split(detail, " (")[0], p)})
	}
	fmt.Fprintln(env.W(), "eskaliert:", detail)
	return cli.ExitOK
}

// pickContainer resolves --container to one of the service's own containers,
// or the first one; anything else is refused (#35: exec reached any
// container on the box while budget and snapshot were booked elsewhere).
func pickContainer(svc *state.Service, want string) (string, error) {
	own := ownContainers(svc)
	if len(own) == 0 {
		return "", fmt.Errorf("%s runs no container", svc.Name)
	}
	if want == "" {
		return own[0], nil
	}
	for _, c := range own {
		if c == want {
			return c, nil
		}
	}
	return "", fmt.Errorf("%s is not one of %s's containers (%s)", want, svc.Name, strings.Join(own, ", "))
}

// containerArgs parses `[--container <name>] [--] <cmd…>`, what exec and
// look take after the service.
func containerArgs(args []string) (container string, cmd []string) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--container" && i+1 < len(args):
			container = args[i+1]
			i++
		case args[i] == "--":
			cmd = args[i+1:]
			i = len(args)
		default:
			cmd = args[i:]
			i = len(args)
		}
	}
	return container, cmd
}

// runIn runs cmd in one of the service's containers and returns its output,
// secrets redacted, with the journal entry for it — red when cmd failed.
// The caller books the entry: exec through record, so a red reaches the
// webhook; look straight into the journal.
func runIn(ctx context.Context, env *cliEnv, svc *state.Service, verb, container string, cmd []string) (string, journal.Entry, error) {
	start := time.Now()
	out, err := sysd.Run(ctx, "podman", append([]string{"exec", container}, cmd...)...)
	sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	out = render.Redact(render.Output{"o": out}, sec)["o"]
	entry := journal.Entry{Actor: env.Actor, Verb: verb, Service: svc.Name, Reason: strings.Join(cmd, " "), Result: "info", Detail: cli.Tail(out, 1500), MS: time.Since(start).Milliseconds()}
	if err != nil {
		entry.Result = "red"
		entry.Detail = err.Error() + "\n" + cli.Tail(out, 1500)
	}
	return out, entry, err
}

// lookCommands are read-only programs `laib look` may run: looking is
// unbounded (ADR 0013), changing is budgeted.
var lookCommands = map[string]bool{"cat": true, "ls": true, "head": true, "tail": true, "grep": true, "ps": true, "df": true, "du": true, "env": true, "id": true, "stat": true, "find": true, "wc": true, "sort": true, "uniq": true, "date": true, "uptime": true, "hostname": true, "echo": true, "test": true, "readlink": true, "file": true, "ss": true, "ip": true, "nproc": true, "free": true, "true": true, "printenv": true, "md5sum": true, "sha256sum": true}

// cmdLook runs a read-only command in one of the service's containers:
// no budget, no snapshot, journaled.
func cmdLook(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 1 {
		return env.Fail(errors.New("usage: laib look <service> [--container <name>] -- <cat|ls|grep|ps|…>"))
	}
	_, svc, err := loadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	container, cmd := containerArgs(args[1:])
	if len(cmd) == 0 {
		return env.Fail(errors.New("no command after --"))
	}
	if !lookCommands[filepath.Base(cmd[0])] {
		return exitRefusedMsg(env, cmd[0]+" is not a read-only command; laib exec is the budgeted way to change something")
	}
	for _, a := range cmd[1:] {
		if strings.ContainsAny(a, ";&|><`$") {
			return exitRefusedMsg(env, "look takes one plain command, no shell")
		}
	}
	container, err = pickContainer(svc, container)
	if err != nil {
		return exitRefusedMsg(env, err.Error())
	}
	out, entry, err := runIn(ctx, env, svc, "look", container, cmd)
	journal.Append(env.Paths.Journal, entry)
	if env.JSON {
		return cli.Emit(env, map[string]any{"container": container, "output": out, "ok": err == nil})
	}
	fmt.Println(out)
	if err != nil {
		return cli.ExitRed
	}
	return cli.ExitOK
}

// opsApply: the Operator applies a fix itself (owner, #286, 2026-10-17) —
// only its own open proposal, never one only a human may free, and inside
// the envelope of every service it touches: the hourly budget, a snapshot
// first; then merge, apply, verify and back on red (autoApprove).
func opsApply(ctx context.Context, env *cliEnv, st *state.State, args []string) int {
	id := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--proposal" {
			id = strings.TrimPrefix(args[i+1], "#")
		}
	}
	n, err := strconv.Atoi(id)
	if err != nil {
		return exitRefusedMsg(env, "der Operator wendet nur seinen eigenen Vorschlag an: laib apply --proposal <id>")
	}
	p, err := proposal.Get(inboxDir(env), n)
	if err != nil {
		return env.Fail(err)
	}
	switch {
	case p.Actor != env.Actor || p.Status != "open":
		return exitRefusedMsg(env, fmt.Sprintf("#%d ist nicht dein offener Vorschlag (%s, %s)", n, p.Actor, p.Status))
	case policy.NeedsHuman(st.Policy, p.Tier, p.Class):
		return exitRefusedMsg(env, fmt.Sprintf("#%d gibt nur ein Mensch frei: %s", n, strings.Join(p.Reasons, "; ")))
	}
	var svcs []string
	for _, f := range p.Files {
		if parts := strings.Split(f, "/"); len(parts) > 2 && parts[0] == "services" && !slices.Contains(svcs, parts[1]) {
			svcs = append(svcs, parts[1])
		}
	}
	if len(svcs) == 0 {
		return exitRefusedMsg(env, fmt.Sprintf("#%d ändert keinen Dienst", n))
	}
	for _, name := range svcs {
		svc, ok := st.Services[name]
		if !ok {
			return exitRefusedMsg(env, fmt.Sprintf("#%d fügt %s hinzu: das gibt ein Mensch frei", n, name))
		}
		if code := envelope(ctx, env, st, svc, "apply", fmt.Sprintf("#%d %s", n, p.Reason)); code != cli.ExitOK {
			return code
		}
	}
	code := autoApprove(ctx, env, p)
	result := "green"
	if code != cli.ExitOK {
		result = "red"
	}
	for _, name := range svcs {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "apply", Service: name, Result: result, Reason: p.Reason, Detail: fmt.Sprintf("#%d selbst angewendet", n)})
	}
	return code
}
