// Package envelope is the hands' verbs on one service (ADR 0013, #309):
// class 1 looks (inspect, read, look) unbounded; class 2 corrects (write,
// exec, restart, the Operator's own apply) inside the envelope — the
// hourly budget, a pre-repair snapshot, the journal with the output,
// verify where it applies, fail closed. And escalate, the hand-over.
// Trust kernel (ADR 0019 §2c).
package envelope

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
	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
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

// LoadSvc loads the state and the named service in it.
func LoadSvc(env *cli.Env, name string) (*state.State, *state.Service, error) {
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
	allowed := state.DataPaths(st, svc)
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
func envelope(ctx context.Context, env *cli.Env, st *state.State, svc *state.Service, verb, detail string) int {
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
		case "exec", "write", "restart", "start", "stop":
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
	if snapped {
		return cli.ExitOK
	}
	return backup.Pre(ctx, env, st, svc, verb, "pre-repair", false)
}

func Inspect(ctx context.Context, env *cli.Env, args []string) int {
	if len(args) == 0 {
		return env.Fail(errors.New("usage: laib inspect <service>"))
	}
	st, svc, err := LoadSvc(env, args[0])
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
	for _, p := range state.DataPaths(st, svc) {
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

func Read(_ context.Context, env *cli.Env, args []string) int {
	if len(args) < 2 {
		return env.Fail(errors.New("usage: laib read <service> <path>"))
	}
	st, svc, err := LoadSvc(env, args[0])
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

func Write(ctx context.Context, env *cli.Env, args []string) int {
	if len(args) < 2 {
		return env.Fail(errors.New("usage: laib write <service> <path>   (content on stdin)"))
	}
	st, svc, err := LoadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "apply") {
		return cli.Refused(env, env.Actor+" may not write")
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

func Exec(ctx context.Context, env *cli.Env, args []string) int {
	// laib exec <service> [--container name] -- cmd args…
	if len(args) < 1 {
		return env.Fail(errors.New("usage: laib exec <service> [--container <name>] -- <cmd…>"))
	}
	st, svc, err := LoadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "apply") {
		return cli.Refused(env, env.Actor+" may not exec")
	}
	container, cmd := containerArgs(args[1:])
	if len(cmd) == 0 {
		return env.Fail(errors.New("no command after --"))
	}
	container, err = pickContainer(svc, container)
	if err != nil {
		return cli.Refused(env, err.Error())
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

func Restart(ctx context.Context, env *cli.Env, args []string) int {
	return units(ctx, env, "restart", args)
}

// Start is `laib start <service>` (#441: the chat's interface): it lifts a
// pause a stop left, starts the units and verifies, inside the envelope.
func Start(ctx context.Context, env *cli.Env, args []string) int {
	return units(ctx, env, "start", args)
}

// Flash is `laib flash <service>` (#759, destroy, humans only; laib repair
// proposes it): it starts the service's <service>-flash.service, which
// writes firmware onto a device, after a snapshot of the service, and
// answers at once. The unit's result is laib diag's and laib logs'; what
// the device runs afterwards, the template's status measures.
func Flash(ctx context.Context, env *cli.Env, args []string) int {
	return oneshot(ctx, env, args, "flash", "spielt Firmware auf ein Gerät", "hat nichts aufzuspielen",
		"%s spielt auf (%s): das dauert einige Minuten, das Gerät startet dabei neu. Wie es ausging: laib diag, laib logs %s\n")
}

// Retrain is `laib retrain <service>` (#828, destroy, humans only; laib
// repair proposes it): it starts the service's <service>-retrain.service,
// which moves the trained model aside (the copy stays) so its trainer
// trains again, after a snapshot, and answers at once.
func Retrain(ctx context.Context, env *cli.Env, args []string) int {
	return oneshot(ctx, env, args, "retrain", "lässt ein Modell neu trainieren", "hat nichts neu zu trainieren",
		"%s trainiert neu (%s): das dauert Stunden, bis dahin bleibt das alte Modell auf dem Gerät. Wie es steht: laib status %s\n")
}

// oneshot starts a service's <service>-<verb>.service for a human (flash,
// retrain): the unit does the work, laib answers at once.
func oneshot(ctx context.Context, env *cli.Env, args []string, verb, what, none, started string) int {
	if len(args) != 1 {
		return env.Fail(errors.New("usage: laib " + verb + " <service>   (laib repair schlägt es vor, ein Mensch gibt es frei)"))
	}
	if proposal.Hand(env.Actor) != "human" {
		return cli.Refused(env, "nur ein Mensch "+what+": laib repair schlägt es in der Inbox vor")
	}
	st, svc, err := LoadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	unit := svc.Name + "-" + verb + ".service"
	if !slices.Contains(svc.Oneshots(), unit) {
		return env.Fail(fmt.Errorf("%s %s (kein %s)", svc.Name, none, unit))
	}
	if code := envelope(ctx, env, st, svc, verb, unit); code != cli.ExitOK {
		return code
	}
	if _, err := sysd.Run(ctx, "systemctl", "--user", "start", "--no-block", unit); err != nil {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: verb, Service: svc.Name, Result: "red", Detail: unit + ": " + err.Error()})
		return env.Fail(err)
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: verb, Service: svc.Name, Result: "info", Detail: unit + " gestartet"})
	fmt.Fprintf(env.W(), started, svc.Name, unit, svc.Name)
	return cli.ExitOK
}

// Stop is `laib stop <service> [--until 2h] ["grund"]` (#441): a pause (the
// service is the caller's; converge leaves it alone, at most a day) and the
// stop, inside the envelope. A service off for good is `laib remove`.
func Stop(ctx context.Context, env *cli.Env, args []string) int { return units(ctx, env, "stop", args) }

func units(ctx context.Context, env *cli.Env, verb string, args []string) int {
	if len(args) < 1 {
		return env.Fail(errors.New("usage: laib " + verb + " <service>"))
	}
	st, svc, err := LoadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "apply") {
		return cli.Refused(env, env.Actor+" may not "+verb)
	}
	if code := envelope(ctx, env, st, svc, verb, verb); code != cli.ExitOK {
		return code
	}
	if verb == "stop" {
		until, why := time.Now().Add(2*time.Hour), strings.Join(args[1:], " ")
		if len(args) > 2 && args[1] == "--until" {
			d, err := time.ParseDuration(args[2])
			if err != nil || d > 24*time.Hour {
				return env.Fail(errors.New("--until: a duration of at most a day (2h); a service off for good is laib remove"))
			}
			until, why = time.Now().Add(d), strings.Join(args[3:], " ")
		}
		if err := apply.WritePause(env.Paths, apply.Pause{Service: svc.Name, By: env.Actor, Until: until.UTC(), Reason: why}); err != nil {
			return env.Fail(err)
		}
		for _, u := range apply.PodRestartSet(svc.UnitNames()) {
			sysd.Stop(ctx, u)
		}
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "stop", Service: svc.Name, Reason: why, Result: "info", Detail: "gestoppt und pausiert bis " + until.Local().Format("02.01. 15:04")})
		fmt.Fprintf(env.W(), "%s gestoppt bis %s (laib start %s startet ihn früher)\n", svc.Name, until.Local().Format("02.01. 15:04"), svc.Name)
		return cli.ExitOK
	}
	todo := svc.UnitNames()
	if verb == "start" {
		apply.Resume(env.Paths, svc.Name)
		todo = apply.PodRestartSet(todo)
	}
	for _, u := range todo {
		run := sysd.Restart
		if verb == "start" {
			run = func(ctx context.Context, u string) error {
				_, err := sysd.Run(ctx, "systemctl", "--user", "start", u)
				return err
			}
		}
		if err := run(ctx, u); err != nil {
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: verb, Service: svc.Name, Result: "red", Detail: u + ": " + err.Error()})
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
	entry := journal.Entry{Actor: env.Actor, Verb: verb, Service: svc.Name, Result: "green"}
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

// Escalate hands a service to the next hand: a context bundle under
// /mnt/data/laib/escalations and a red journal line that reaches the
// webhook, so the Werkstatt's claude hand (or the human) picks it up.
func Escalate(ctx context.Context, env *cli.Env, args []string) int {
	if len(args) < 2 {
		return env.Fail(errors.New("usage: laib escalate <service> \"<was ich weiß>\""))
	}
	st, svc, err := LoadSvc(env, args[0])
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
	Inspect(ctx, &cli.Env{Paths: env.Paths, Actor: env.Actor, MCP: env.MCP, Out: &ins, ErrOut: env.ErrOut}, []string{svc.Name})
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
	// with an intake the bundle becomes a ticket something reads (#56); the
	// Bote's goes to the owner as a Box-Aufgabe, never to a repo (#863)
	detail, out := p, map[string]any{"file": p, "issue": ""}
	if proposal.Hand(env.Actor) == "bote" {
		var a aufgaben.Aufgabe
		err = aufgaben.Update(ctx, env.Paths.State, env.Actor, func(list []aufgaben.Aufgabe) ([]aufgaben.Aufgabe, string, error) {
			list, a = aufgaben.Add(list, aufgaben.Aufgabe{Titel: "Frage aus dem Boten: " + firstLine(strings.Join(args[1:], " "), 150),
				Warum: "Der Bote kam bei " + svc.Name + " nicht weiter; was er wusste und der Stand liegen in " + p}, env.Actor, time.Now())
			return list, fmt.Sprintf("aufgabe add #%d %s", a.ID, a.Titel), nil
		})
		if detail, out["aufgabe"] = fmt.Sprintf("Box-Aufgabe #%d (%s)", a.ID, p), a.ID; err != nil {
			detail, out["aufgabe"] = p+" (Box-Aufgabe rot: "+err.Error()+")", nil
		}
	} else if url, ok, err := inbox.Issue(ctx, env, st, "Eskalation "+svc.Name+": "+cli.Tail(strings.Join(args[1:], " "), 80), b.String(), "needs-claude"); err != nil {
		detail = p + " (Intake rot: " + err.Error() + ")"
	} else if ok {
		detail, out["issue"] = url+" ("+p+")", url
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "escalate", Service: svc.Name, Result: "red", Reason: strings.Join(args[1:], " "), Detail: detail})
	if env.JSON {
		return cli.Emit(env, out)
	}
	fmt.Fprintln(env.W(), "eskaliert:", detail)
	return cli.ExitOK
}

// firstLine is s on one line, cut after n runes: a Box-Aufgabe's title.
func firstLine(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
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
func runIn(ctx context.Context, env *cli.Env, svc *state.Service, verb, container string, cmd []string) (string, journal.Entry, error) {
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

// Look runs a read-only command in one of the service's containers:
// no budget, no snapshot, journaled.
func Look(ctx context.Context, env *cli.Env, args []string) int {
	if len(args) < 1 {
		return env.Fail(errors.New("usage: laib look <service> [--container <name>] -- <cat|ls|grep|ps|…>"))
	}
	_, svc, err := LoadSvc(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	container, cmd := containerArgs(args[1:])
	if len(cmd) == 0 {
		return env.Fail(errors.New("no command after --"))
	}
	if !lookCommands[filepath.Base(cmd[0])] {
		return cli.Refused(env, cmd[0]+" is not a read-only command; laib exec is the budgeted way to change something")
	}
	for _, a := range cmd[1:] {
		if strings.ContainsAny(a, ";&|><`$") {
			return cli.Refused(env, "look takes one plain command, no shell")
		}
	}
	container, err = pickContainer(svc, container)
	if err != nil {
		return cli.Refused(env, err.Error())
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

// OpsApply: the Operator applies a fix itself (owner, #286, 2026-10-17) —
// only its own open proposal, never one only a human may free, and inside
// the envelope of every service it touches: the hourly budget, a snapshot
// first; then merge, apply, verify and back on red (autoApprove).
func OpsApply(ctx context.Context, env *cli.Env, st *state.State, args []string) int {
	id := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--proposal" {
			id = strings.TrimPrefix(args[i+1], "#")
		}
	}
	n, err := strconv.Atoi(id)
	if err != nil {
		return cli.Refused(env, "der Operator wendet nur seinen eigenen Vorschlag an: laib apply --proposal <id>")
	}
	p, err := proposal.Get(cli.InboxDir(env), n)
	if err != nil {
		return env.Fail(err)
	}
	switch {
	case p.Actor != env.Actor || p.Status != "open":
		return cli.Refused(env, fmt.Sprintf("#%d ist nicht dein offener Vorschlag (%s, %s)", n, p.Actor, p.Status))
	case policy.NeedsHuman(st.Policy, p.Tier, p.Class):
		return cli.Refused(env, fmt.Sprintf("#%d gibt nur ein Mensch frei: %s", n, strings.Join(p.Reasons, "; ")))
	}
	var svcs []string
	for _, f := range p.Files {
		if parts := strings.Split(f, "/"); len(parts) > 2 && parts[0] == "services" && !slices.Contains(svcs, parts[1]) {
			svcs = append(svcs, parts[1])
		}
	}
	if len(svcs) == 0 {
		return cli.Refused(env, fmt.Sprintf("#%d ändert keinen Dienst", n))
	}
	for _, name := range svcs {
		svc, ok := st.Services[name]
		if !ok {
			return cli.Refused(env, fmt.Sprintf("#%d fügt %s hinzu: das gibt ein Mensch frei", n, name))
		}
		if code := envelope(ctx, env, st, svc, "apply", fmt.Sprintf("#%d %s", n, p.Reason)); code != cli.ExitOK {
			return code
		}
	}
	code := inbox.AutoApprove(ctx, env, p)
	result := "green"
	if code != cli.ExitOK {
		result = "red"
	}
	for _, name := range svcs {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "apply", Service: name, Result: result, Reason: p.Reason, Detail: fmt.Sprintf("#%d selbst angewendet", n)})
	}
	return code
}
