package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
	"gopkg.in/yaml.v3"
)

// Exit codes (docs/INTERFACE.md).
const (
	exitOK      = 0
	exitUsage   = 1
	exitRed     = 2
	exitRefused = 3
	exitLocked  = 4
)

func loadState(env *cliEnv) (*state.State, error) {
	st, err := state.Load(env.paths.State)
	if err != nil {
		return nil, err
	}
	if err := policy.Validate(st.Policy); err != nil {
		return nil, err
	}
	return st, nil
}

// readiness is the one state with three values (docs/DESIGN.md).
type readiness struct {
	State     string `json:"state"`
	Services  int    `json:"services"`
	Green     int    `json:"green"`
	Yellow    int    `json:"yellow"`
	Red       int    `json:"red"`
	LastHeard string `json:"lastHeard"`
	Lock      string `json:"lock,omitempty"`
	Drift     int    `json:"drift"` // files on the box that differ from the state
	Operator  string `json:"operator"` // aus | startet | läuft | stumm | antwortet nicht | fehlt
}

type serviceStatus struct {
	Name   string            `json:"name"`
	State  string            `json:"state"` // grün | gelb | rot
	Units  map[string]string `json:"units"`
	Detail string            `json:"detail,omitempty"`
}

// bootUnclean is set by serve after its first comparison when the box did
// not come up matching the state (#9); cleared by the next clean check.
var bootUnclean string

func collectStatus(ctx context.Context, env *cliEnv, st *state.State) (readiness, []serviceStatus) {
	var rows []serviceStatus
	r := readiness{State: "bereit", LastHeard: time.Now().UTC().Format(time.RFC3339)}
	r.Operator, _ = operatorState(operatorFacts(ctx, env, st))
	if holder := apply.LockHolder(env.paths); holder != "" {
		r.State, r.Lock = "eingeschränkt", holder
	}
	if sec, err := secrets.Load(env.paths.Secrets, env.paths.Key); err == nil {
		if p, err := plan.Compute(ctx, st, sec, env.paths, ""); err == nil {
			r.Drift = len(p.Changes)
		}
	}
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		row := serviceStatus{Name: name, State: "grün", Units: map[string]string{}}
		for _, u := range svc.UnitNames() {
			a := sysd.Active(ctx, u)
			row.Units[u] = a
			switch a {
			case "active":
			case "activating", "reloading":
				if row.State == "grün" {
					row.State = "gelb"
				}
			default:
				row.State = "rot"
			}
		}
		rows = append(rows, row)
		r.Services++
		switch row.State {
		case "grün":
			r.Green++
		case "gelb":
			r.Yellow++
		default:
			r.Red++
		}
	}
	if r.Red == 0 && r.Drift == 0 {
		bootUnclean = ""
	}
	if bootUnclean != "" && r.Lock == "" {
		r.State, r.Lock = "eingeschränkt", bootUnclean
	}
	return r, rows
}

func cmdStatus(ctx context.Context, env *cliEnv, args []string) int {
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	r, rows := collectStatus(ctx, env, st)
	if len(args) > 0 {
		var one []serviceStatus
		for _, row := range rows {
			if row.Name == args[0] {
				one = append(one, row)
			}
		}
		rows = one
	}
	if env.json {
		return emit(env, map[string]any{"ready": r, "services": rows})
	}
	fmt.Printf("Laib · %s · %d Dienste · %d grün · %d gelb · %d rot · Drift %d · Operator %s\n", r.State, r.Services, r.Green, r.Yellow, r.Red, r.Drift, r.Operator)
	if r.Lock != "" {
		fmt.Printf("  gesperrt durch: %s\n", r.Lock)
	}
	for _, row := range rows {
		fmt.Printf("  %-5s %-20s", row.State, row.Name)
		for u, a := range row.Units {
			if a != "active" {
				fmt.Printf(" %s=%s", u, a)
			}
		}
		fmt.Println()
	}
	if e, ok := journal.Last(env.paths.Journal); ok {
		fmt.Printf("Letzte Änderung  %s  %s  %s %s  %s\n", e.T.Local().Format("02.01. 15:04"), e.Actor, e.Verb, e.Service, e.Reason)
	}
	return exitOK
}

func cmdPlan(ctx context.Context, env *cliEnv, args []string) int {
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	sec, err := secrets.Load(env.paths.Secrets, env.paths.Key)
	if err != nil {
		return fail(err)
	}
	only := ""
	if len(args) > 0 {
		only = args[0]
	}
	p, err := plan.Compute(ctx, st, sec, env.paths, only)
	if err != nil {
		return fail(err)
	}
	if env.json {
		return emit(env, p)
	}
	if p.Empty() {
		fmt.Println("Keine Änderung: die Box entspricht dem State.")
	}
	for _, c := range p.Changes {
		fmt.Printf("%-6s %s\n", c.Kind, c.Target)
		if c.Diff != "" {
			fmt.Println(c.Diff)
		}
	}
	for _, u := range p.Unmanaged {
		fmt.Printf("nicht verwaltet: %s   (laib adopt %s | laib ignore %s)\n", u, u, u)
	}
	for _, c := range p.Conflicts {
		fmt.Printf("KONFLIKT: %s   (erst die andere Quelle stoppen und entfernen, dann apply)\n", c)
	}
	for _, w := range st.Warnings {
		fmt.Printf("HINWEIS: %s\n", w)
	}
	return exitOK
}

func cmdApply(ctx context.Context, env *cliEnv, args []string) int {
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	hand := strings.SplitN(env.actor, ":", 2)[0]
	if !policy.Allowed(st.Policy, hand, "apply") {
		fmt.Fprintf(os.Stderr, "laib: %s may not apply (ceiling %s). Make a proposal: laib propose \"<grund>\"\n", env.actor, st.Policy.Hands[hand].Ceiling)
		return exitRefused
	}
	only, reason := "", ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--reason" && i+1 < len(args):
			reason = args[i+1]
			i++
		default:
			only = args[i]
		}
	}
	return runApply(ctx, env, st, only, reason)
}

func cmdJournal(_ context.Context, env *cliEnv, args []string) int {
	since := 24 * time.Hour
	for i := 0; i < len(args); i++ {
		if args[i] == "--since" && i+1 < len(args) {
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				return fail(err)
			}
			since = d
			i++
		}
	}
	es, err := journal.Read(env.paths.Journal, time.Now().Add(-since))
	if err != nil {
		return fail(err)
	}
	if env.json {
		return emit(env, es)
	}
	for _, e := range es {
		fmt.Printf("%s  %-14s %-8s %-12s %-5s %s %s\n", e.T.Local().Format("02.01. 15:04:05"), e.Actor, e.Verb, e.Service, e.Result, e.Reason, e.Detail)
	}
	return exitOK
}

func cmdContext(ctx context.Context, env *cliEnv, args []string) int {
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	r, rows := collectStatus(ctx, env, st)
	es, _ := journal.Read(env.paths.Journal, time.Now().Add(-24*time.Hour))
	if env.json {
		return emit(env, map[string]any{"ready": r, "services": rows, "journal": es, "domain": st.Box.Domain})
	}
	fmt.Printf("# Laib context · %s · %s\n\n", st.Box.Domain, r.State)
	fmt.Println("## Services")
	for _, row := range rows {
		fmt.Printf("- %s: %s\n", row.Name, row.State)
	}
	fmt.Println("\n## Journal, letzte 24h")
	for _, e := range es {
		fmt.Printf("- %s %s %s %s %s %s\n", e.T.Format("15:04"), e.Actor, e.Verb, e.Service, e.Result, e.Reason)
	}
	fmt.Println("\n## Read next\n- docs/DESIGN.md, docs/INTERFACE.md, docs/STATE.md in the laib repo\n- `laib plan` before any change; `laib propose` if your hand may not apply")
	return exitOK
}

func cmdTemplate(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return fail(errors.New("usage: laib template lint <dir> | list | sync | update <service>"))
	}
	switch args[0] {
	case "lint":
		if len(args) < 2 {
			return fail(errors.New("usage: laib template lint <dir>"))
		}
		return lintTemplate(args[1])
	case "list":
		st, err := loadState(env)
		if err != nil {
			return fail(err)
		}
		avail := availableTemplates(env)
		if env.json {
			return emit(env, map[string]any{"available": avail, "installed": st.ServiceNames()})
		}
		fmt.Println("installiert:")
		for _, name := range st.ServiceNames() {
			t := st.Services[name].Contract.Template
			fmt.Printf("  %-20s %s/%s v%d\n", name, t.Registry, t.Name, t.Version)
		}
		fmt.Println("verfügbar:")
		for _, name := range sortedKeys(avail) {
			fmt.Printf("  %-20s %s\n", name, strings.Join(avail[name], ", "))
		}
		return exitOK
	case "sync":
		st, err := loadState(env)
		if err != nil {
			return fail(err)
		}
		lines, err := syncRegistries(ctx, env, st)
		for _, l := range lines {
			fmt.Println("registry:", l)
		}
		if err != nil {
			return fail(err)
		}
		journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "template", Result: "info", Detail: "sync " + strings.Join(lines, "; ")})
		if len(args) > 1 && args[1] == "--propose" {
			// Every installed service whose template changed gets an update
			// proposal; the dial decides whether laib applies it itself.
			code := exitOK
			for _, name := range st.ServiceNames() {
				if c := templateUpdate(ctx, env, st, name); c != exitOK && c != exitUsage {
					code = c
				}
			}
			return code
		}
		return exitOK
	case "update":
		if len(args) < 2 {
			return fail(errors.New("usage: laib template update <service>"))
		}
		st, err := loadState(env)
		if err != nil {
			return fail(err)
		}
		if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "propose") {
			return exitRefusedMsg(env, env.actor+" may not propose")
		}
		return templateUpdate(ctx, env, st, args[1])
	}
	return fail(fmt.Errorf("unknown template subcommand %q", args[0]))
}

// lintTemplate loads a template folder as if it were the only service of a
// throw-away state, so the same validation applies (ADR 0004).
func lintTemplate(dir string) int {
	tmp, err := os.MkdirTemp("", "laib-lint")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(tmp)
	name := filepath.Base(filepath.Clean(dir))
	os.WriteFile(filepath.Join(tmp, "box.yml"), []byte("domain: lint.example\ndataDir: /tmp/lint\nlanIp: 10.0.0.2\ngpu: {present: true}\nhostNetwork: ["+name+"]\nroles: {sso: "+name+"}\n"), 0o644)
	os.WriteFile(filepath.Join(tmp, "policy.yml"), []byte("tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n"), 0o644)
	dst := filepath.Join(tmp, "services", name)
	os.MkdirAll(dst, 0o755)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fail(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644)
	}
	if _, err := os.Stat(filepath.Join(dst, "values.yml")); err != nil {
		// A template ships values.example.yml; lint with it.
		if b, err := os.ReadFile(filepath.Join(dir, "values.example.yml")); err == nil {
			os.WriteFile(filepath.Join(dst, "values.yml"), b, 0o644)
		}
	}
	// after: names services the template expects to exist; stub them so the
	// reference itself is checked, not the neighbour's presence.
	if b, err := os.ReadFile(filepath.Join(dir, "laib.yml")); err == nil {
		var c struct {
			After []string `yaml:"after"`
		}
		if yaml.Unmarshal(b, &c) == nil {
			for _, dep := range c.After {
				if dep == name {
					continue
				}
				stub := filepath.Join(tmp, "services", dep)
				os.MkdirAll(stub, 0o755)
				os.WriteFile(filepath.Join(stub, "laib.yml"), []byte("name: "+dep+"\n"), 0o644)
				os.WriteFile(filepath.Join(stub, dep+".container"), []byte("[Container]\nImage=stub\n"), 0o644)
			}
		}
	}
	st, err := state.Load(tmp)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	if _, err := render.Render(st, nil); err != nil {
		return fail(err)
	}
	fmt.Printf("ok: %s (%d unit files, %d routes, %d secrets)\n", name, len(st.Services[name].Units), len(st.Services[name].Contract.Routes), len(st.Services[name].Contract.Secrets))
	return exitOK
}

func cmdAdopt(_ context.Context, env *cliEnv, args []string) int {
	if len(args) != 1 {
		return fail(errors.New("usage: laib adopt <quadlet folder>"))
	}
	name := args[0]
	src := filepath.Join(env.paths.Quadlet, name)
	dst := filepath.Join(env.paths.State, "services", name)
	if _, err := os.Stat(dst); err == nil {
		return fail(fmt.Errorf("services/%s exists already", name))
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fail(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(src, e.Name()))
		os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644)
	}
	contract := fmt.Sprintf("name: %s\ntemplate: {registry: adopted, name: %s, version: 1}\nroutes: []\nsecrets: []\ndata: []\nports: []\n", name, name)
	os.WriteFile(filepath.Join(dst, "laib.yml"), []byte(contract), 0o644)
	fmt.Printf("adopted %s → services/%s. Fill laib.yml (routes, secrets, data), then laib plan %s.\n", name, name, name)
	return exitOK
}

func cmdIgnore(_ context.Context, env *cliEnv, args []string) int {
	if len(args) != 1 {
		return fail(errors.New("usage: laib ignore <unit>"))
	}
	p := filepath.Join(env.paths.State, "ignore.yml")
	b, _ := os.ReadFile(p)
	content := string(b)
	if content == "" {
		content = "units:\n"
	}
	if strings.Contains(content, "- "+args[0]+"\n") {
		fmt.Println("already ignored:", args[0])
		return exitOK
	}
	content += "  - " + args[0] + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return fail(err)
	}
	fmt.Println("ignored:", args[0])
	return exitOK
}

func cmdInit(ctx context.Context, env *cliEnv, args []string) int {
	domain, dataDir, lanIP := "", "/mnt/data/stacks", ""
	modelURL, modelLease, gpu := "", "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--gpu":
			gpu = true
			continue
		}
		if i+1 >= len(args) {
			break
		}
		switch args[i] {
		case "--domain":
			domain = args[i+1]
		case "--data-dir":
			dataDir = args[i+1]
		case "--lan-ip":
			lanIP = args[i+1]
		case "--model-url":
			modelURL = args[i+1]
		case "--model-lease":
			modelLease = args[i+1]
		}
		i++
	}
	if domain == "" {
		return fail(errors.New("usage: laib init --domain <domain> [--lan-ip <ip>] [--data-dir /mnt/data/stacks] [--gpu | --model-url <openai-compatible /v1> [--model-lease <url>]]"))
	}
	if !gpu && modelURL == "" {
		fmt.Fprintln(os.Stderr, "laib: no --gpu and no --model-url: the Operator, Bote and Frage will have no model until box.yml model.url is set")
	}
	for _, d := range []string{env.paths.Home, env.paths.State, env.paths.Journal, env.paths.Render, filepath.Join(env.paths.State, "services")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fail(err)
		}
	}
	recipient, err := secrets.EnsureKey(env.paths.Key)
	if err != nil {
		return fail(err)
	}
	write := func(name, content string) error {
		p := filepath.Join(env.paths.State, name)
		if _, err := os.Stat(p); err == nil {
			return nil
		}
		return os.WriteFile(p, []byte(content), 0o644)
	}
	model := ""
	if modelURL != "" {
		model = fmt.Sprintf("model:                    # no GPU here: the LLM is remote (a peer's llama proxy or a cloud API)\n  url: %s\n", modelURL)
		if strings.HasPrefix(modelURL, "https://") {
			// A cloud API wants a key; a peer's LAN proxy does not.
			model += "  keySecret: MODEL_API_KEY  # laib secret set MODEL_API_KEY before the first apply\n"
		}
		if modelLease != "" {
			model += fmt.Sprintf("  lease: %s\n", modelLease)
		}
	}
	backup := fmt.Sprintf("backup:\n  repo: %s          # restic, on the data disk: protects against mistakes and bad updates from day one; a NAS or second repo protects against the disk\n  schedule: \"03:00\"\n", filepath.Join(filepath.Dir(dataDir), "backup", "restic"))
	if err := write("box.yml", backup+fmt.Sprintf("domain: %s\ntimezone: Europe/Berlin\ndataDir: %s\nlanIp: %s\nroles: {}\ngpu: {present: %v}\n# the closed carve-out list (ADR 0007): built-ins that need the host network\nhostNetwork: [caddy, adguard, home-assistant, file-share, stimme, llama, esphome, music-assistant]\n%s", domain, dataDir, lanIP, gpu, model)); err != nil {
		return fail(err)
	}
	if err := write("policy.yml", "tiers: [read, propose, apply, destroy]\nhands:\n  resident: {ceiling: read}\n  ops: {ceiling: apply}          # the operator (ADR 0013): observe unbounded, correct inside the envelope\n  pi: {ceiling: propose}\n  bote: {ceiling: propose}       # the Bote on Signal (ADR 0016): reads, proposes, notes; the admin's own token decides\n  claude: {ceiling: apply, ttl: 8h}\n  human: {ceiling: destroy}\napprovals:\n  destroy: {by: human, snapshot: required}\nautonomy:\n  converge: on            # the ten-minute loop re-applies drift and restarts units\n  values: auto            # laib applies, rolls back on red\n  template-update: auto\n  patch: auto\n  new-service: proposal   # adds surface: a human decides\n  # remove is never auto; a public route is always a human's call\n"); err != nil {
		return fail(err)
	}
	if err := write(".gitignore", "secrets.age.tmp\n"); err != nil {
		return fail(err)
	}
	if err := proposal.EnsureRepo(ctx, env.paths.State); err != nil {
		return fail(err)
	}
	if err := journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "init", Result: "info", Detail: "state repo created"}); err != nil {
		return fail(err)
	}
	fmt.Printf("state repo: %s\nage recipient: %s\nage key: %s  ← print it and keep it outside the box\n", env.paths.State, recipient, env.paths.Key)
	return exitOK
}
