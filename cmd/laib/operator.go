package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
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

func serviceOf(st *state.State, name string) (*state.Service, error) {
	svc, ok := st.Services[name]
	if !ok {
		return nil, fmt.Errorf("unknown service %q", name)
	}
	return svc, nil
}

// dataPaths expands a service's declared data paths.
func dataPaths(st *state.State, svc *state.Service) []string {
	var out []string
	for _, d := range svc.Contract.Data {
		out = append(out, strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir))
	}
	return out
}

// jail resolves a path and requires it inside one of the service's data
// paths (or the service's whole data dir when it declares none).
func jail(st *state.State, svc *state.Service, p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	allowed := dataPaths(st, svc)
	if len(allowed) == 0 {
		allowed = []string{filepath.Join(st.Box.DataDir, svc.Name)}
	}
	for _, a := range allowed {
		if ra, err := filepath.EvalSymlinks(a); err == nil {
			a = ra
		}
		if abs == a || strings.HasPrefix(abs, a+string(filepath.Separator)) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("%s is outside %s's data paths (%s)", p, svc.Name, strings.Join(allowed, ", "))
}

// envelope is what wraps every class-2 action: budget check, a pre-repair
// snapshot once per service per hour when a backup repo exists, and a
// journal line. Returns exitRefused when the budget is spent.
func envelope(ctx context.Context, env *cliEnv, st *state.State, svc *state.Service, verb, detail string) int {
	since := time.Now().Add(-time.Hour)
	recent, _ := journal.Read(env.paths.Journal, since)
	used, snapped := 0, false
	for _, e := range recent {
		if e.Service != svc.Name {
			continue
		}
		switch e.Verb {
		case "exec", "write", "restart":
			used++
		case "snapshot":
			snapped = true
		}
	}
	if used >= RepairBudget {
		record(env, st, journal.Entry{Actor: env.actor, Verb: verb, Service: svc.Name, Result: "red", Detail: fmt.Sprintf("Budget erschöpft (%d/h). Ein Mensch muss ran: %s", RepairBudget, detail)})
		fmt.Fprintf(os.Stderr, "laib: Budget für %s erschöpft (%d Eingriffe in der letzten Stunde). laib escalate %s \"…\" ist der Weg weiter.\n", svc.Name, RepairBudget, svc.Name)
		return exitRefused
	}
	if !snapped && st.Box.Backup.Repo != "" && len(dataPaths(st, svc)) > 0 {
		if _, pw, err := backupPrep(env); err == nil {
			if _, err := resticRun(ctx, st, pw, append([]string{"backup", "--tag", "pre-repair", "--tag", svc.Name}, dataPaths(st, svc)...)...); err != nil {
				record(env, st, journal.Entry{Actor: env.actor, Verb: "snapshot", Service: svc.Name, Result: "red", Detail: err.Error()})
			} else {
				record(env, st, journal.Entry{Actor: env.actor, Verb: "snapshot", Service: svc.Name, Result: "info", Detail: "pre-repair"})
			}
		}
	}
	return exitOK
}

func cmdInspect(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return fail(errors.New("usage: laib inspect <service>"))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	svc, err := serviceOf(st, args[0])
	if err != nil {
		return fail(err)
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
	sec, _ := secrets.Load(env.paths.Secrets, env.paths.Key)
	logs := map[string]string{}
	for _, u := range svc.UnitNames() {
		text, _ := sysd.Run(ctx, "journalctl", "--user", "-u", u, "-n", "100", "--no-pager", "-o", "short-iso")
		logs[u] = render.Redact(render.Output{"l": text}, sec)["l"]
	}
	out["logs"] = logs
	if env.json {
		return emit(env, out)
	}
	fmt.Printf("# %s\n", svc.Name)
	for u, s := range units {
		fmt.Printf("## unit %s\n%s\n", u, s)
	}
	for c, s := range containers {
		fmt.Printf("## container %s\n%s\n", c, s)
	}
	for p, s := range files {
		fmt.Printf("## data %s\n%s\n", p, s)
	}
	if h, ok := out["health"]; ok {
		fmt.Printf("## health\n%v\n", h)
	}
	for u, s := range logs {
		fmt.Printf("## logs %s\n%s\n", u, s)
	}
	return exitOK
}

func cmdRead(_ context.Context, env *cliEnv, args []string) int {
	if len(args) < 2 {
		return fail(errors.New("usage: laib read <service> <path>"))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	svc, err := serviceOf(st, args[0])
	if err != nil {
		return fail(err)
	}
	p, err := jail(st, svc, args[1])
	if err != nil {
		return fail(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return fail(err)
	}
	if len(b) > 1<<20 {
		b = b[:1<<20]
	}
	sec, _ := secrets.Load(env.paths.Secrets, env.paths.Key)
	text := render.Redact(render.Output{"f": string(b)}, sec)["f"]
	if env.json {
		return emit(env, map[string]string{"path": p, "content": text})
	}
	fmt.Print(text)
	return exitOK
}

func cmdWrite(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 2 {
		return fail(errors.New("usage: laib write <service> <path>   (content on stdin)"))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	svc, err := serviceOf(st, args[0])
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "apply") {
		return exitRefusedMsg(env, env.actor+" may not write")
	}
	p, err := jail(st, svc, args[1])
	if err != nil {
		return fail(err)
	}
	content, err := io.ReadAll(env.stdin())
	if err != nil {
		return fail(err)
	}
	if code := envelope(ctx, env, st, svc, "write", p); code != exitOK {
		return code
	}
	old, _ := os.ReadFile(p)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fail(err)
	}
	tmp := p + ".laib-tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return fail(err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fail(err)
	}
	record(env, st, journal.Entry{Actor: env.actor, Verb: "write", Service: svc.Name, Result: "info", Detail: fmt.Sprintf("%s (%d → %d Bytes)", p, len(old), len(content))})
	fmt.Println("geschrieben:", p)
	return exitOK
}

func cmdExec(ctx context.Context, env *cliEnv, args []string) int {
	// laib exec <service> [--container name] -- cmd args…
	if len(args) < 1 {
		return fail(errors.New("usage: laib exec <service> [--container <name>] -- <cmd…>"))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	svc, err := serviceOf(st, args[0])
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "apply") {
		return exitRefusedMsg(env, env.actor+" may not exec")
	}
	container := ""
	var cmd []string
	for i := 1; i < len(args); i++ {
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
	if len(cmd) == 0 {
		return fail(errors.New("no command after --"))
	}
	if container == "" {
		for _, u := range svc.UnitNames() {
			if strings.HasSuffix(u, ".service") && !strings.HasSuffix(u, "-pod.service") {
				container = containerName(u)
				break
			}
		}
	}
	if code := envelope(ctx, env, st, svc, "exec", strings.Join(cmd, " ")); code != exitOK {
		return code
	}
	start := time.Now()
	out, err := sysd.Run(ctx, "podman", append([]string{"exec", container}, cmd...)...)
	sec, _ := secrets.Load(env.paths.Secrets, env.paths.Key)
	out = render.Redact(render.Output{"o": out}, sec)["o"]
	entry := journal.Entry{Actor: env.actor, Verb: "exec", Service: svc.Name, Reason: strings.Join(cmd, " "), Result: "info", Detail: tail(out, 1500), MS: time.Since(start).Milliseconds()}
	code := exitOK
	if err != nil {
		entry.Result, code = "red", exitRed
		entry.Detail = err.Error() + "\n" + tail(out, 1500)
	}
	record(env, st, entry)
	if env.json {
		return emit(env, map[string]any{"container": container, "output": out, "ok": err == nil})
	}
	fmt.Println(out)
	return code
}

func cmdRestart(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 1 {
		return fail(errors.New("usage: laib restart <service>"))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	svc, err := serviceOf(st, args[0])
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "apply") {
		return exitRefusedMsg(env, env.actor+" may not restart")
	}
	if code := envelope(ctx, env, st, svc, "restart", "restart"); code != exitOK {
		return code
	}
	for _, u := range svc.UnitNames() {
		if err := sysd.Restart(ctx, u); err != nil {
			record(env, st, journal.Entry{Actor: env.actor, Verb: "restart", Service: svc.Name, Result: "red", Detail: u + ": " + err.Error()})
			return exitRed
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
	entry := journal.Entry{Actor: env.actor, Verb: "restart", Service: svc.Name, Result: "green"}
	if !green {
		entry.Result, entry.Detail = "red", strings.Join(bad, "; ")
	}
	record(env, st, entry)
	if env.json {
		return emit(env, map[string]any{"checks": checks, "green": green})
	}
	if green {
		fmt.Println("grün:", svc.Name)
		return exitOK
	}
	fmt.Println("rot:", entry.Detail)
	return exitRed
}

// cmdEscalate hands a service to the next hand: a context bundle under
// /mnt/data/laib/escalations and a red journal line that reaches the
// webhook, so the Werkstatt's claude hand (or the human) picks it up.
func cmdEscalate(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 2 {
		return fail(errors.New("usage: laib escalate <service> \"<was ich weiß>\""))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	svc, err := serviceOf(st, args[0])
	if err != nil {
		return fail(err)
	}
	dir := filepath.Join(env.paths.Home, "escalations")
	os.MkdirAll(dir, 0o755)
	name := time.Now().UTC().Format("2006-01-02T15-04-05") + "-" + svc.Name + ".md"
	var b strings.Builder
	fmt.Fprintf(&b, "# Eskalation %s · von %s\n\n%s\n\n## inspect\n\n", svc.Name, env.actor, strings.Join(args[1:], " "))
	r, w, _ := os.Pipe()
	saved := os.Stdout
	os.Stdout = w
	cmdInspect(ctx, env, []string{svc.Name})
	w.Close()
	os.Stdout = saved
	ins, _ := io.ReadAll(r)
	b.Write(ins)
	es, _ := journal.Read(env.paths.Journal, time.Now().Add(-24*time.Hour))
	b.WriteString("\n## Journal 24h\n\n")
	for _, e := range es {
		if e.Service == svc.Name || e.Service == "" {
			fmt.Fprintf(&b, "- %s %s %s %s %s %s\n", e.T.Format("15:04"), e.Actor, e.Verb, e.Result, e.Reason, e.Detail)
		}
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		return fail(err)
	}
	// with an intake the bundle becomes a ticket something reads (#56)
	detail := p
	if url, ok, err := githubIssue(ctx, env, st, "Eskalation "+svc.Name+": "+tail(strings.Join(args[1:], " "), 80), b.String(), []string{"needs-claude"}); err != nil {
		detail = p + " (Intake rot: " + err.Error() + ")"
	} else if ok {
		detail = url + " (" + p + ")"
	}
	record(env, st, journal.Entry{Actor: env.actor, Verb: "escalate", Service: svc.Name, Result: "red", Reason: strings.Join(args[1:], " "), Detail: detail})
	if env.json {
		return emit(env, map[string]string{"file": p, "issue": strings.TrimSuffix(strings.Split(detail, " (")[0], p)})
	}
	fmt.Println("eskaliert:", detail)
	return exitOK
}
