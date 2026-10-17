package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/registry"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/run"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

func cmdPlan(ctx context.Context, env *cliEnv, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return env.Fail(err)
	}
	only := ""
	if len(args) > 0 {
		only = args[0]
	}
	p, err := plan.Compute(ctx, st, sec, env.Paths, only)
	if err != nil {
		return env.Fail(err)
	}
	if env.JSON {
		return cli.Emit(env, p)
	}
	if p.Empty() {
		fmt.Fprintln(env.W(), "Keine Änderung: die Box entspricht dem State.")
	}
	for _, c := range p.Changes {
		fmt.Fprintf(env.W(), "%-6s %s\n", c.Kind, c.Target)
		if c.Diff != "" {
			fmt.Fprintln(env.W(), c.Diff)
		}
	}
	for _, u := range p.Unmanaged {
		fmt.Fprintf(env.W(), "nicht verwaltet: %s   (laib adopt %s | laib ignore %s)\n", u, u, u)
	}
	for _, c := range p.Conflicts {
		fmt.Fprintf(env.W(), "KONFLIKT: %s   (erst die andere Quelle stoppen und entfernen, dann apply)\n", c)
	}
	for _, w := range st.Warnings {
		fmt.Fprintf(env.W(), "HINWEIS: %s\n", w)
	}
	for _, o := range st.Off {
		fmt.Fprintf(env.W(), "AUS: %s\n", o)
	}
	return cli.ExitOK
}

func cmdApply(ctx context.Context, env *cliEnv, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	hand := strings.SplitN(env.Actor, ":", 2)[0]
	if !policy.Allowed(st.Policy, hand, "apply") {
		return cli.Refused(env, fmt.Sprintf("%s may not apply (ceiling %s). Make a proposal: laib propose \"<grund>\"", env.Actor, st.Policy.Hands[hand].Ceiling))
	}
	if hand == "ops" {
		return envelope.OpsApply(ctx, env, st, args)
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
	return run.Apply(ctx, env, st, only, reason)
}

func cmdJournal(_ context.Context, env *cliEnv, args []string) int {
	since := 24 * time.Hour
	for i := 0; i < len(args); i++ {
		if args[i] == "--since" && i+1 < len(args) {
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				return env.Fail(err)
			}
			since = d
			i++
		}
	}
	es, err := journal.Read(env.Paths.Journal, time.Now().Add(-since))
	if err != nil {
		return env.Fail(err)
	}
	if env.JSON {
		return cli.Emit(env, es)
	}
	for _, e := range es {
		fmt.Fprintf(env.W(), "%s  %-14s %-8s %-12s %-5s %s %s\n", e.T.Local().Format("02.01. 15:04:05"), e.Actor, e.Verb, e.Service, e.Result, e.Reason, e.Detail)
	}
	return cli.ExitOK
}

func cmdContext(ctx context.Context, env *cliEnv, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	r, rows := pages.CollectStatus(ctx, env, st)
	es, _ := journal.Read(env.Paths.Journal, time.Now().Add(-24*time.Hour))
	if env.JSON {
		return cli.Emit(env, map[string]any{"ready": r, "services": rows, "journal": es, "domain": st.Box.Domain})
	}
	fmt.Fprintf(env.W(), "# Laib context · %s · %s\n\n", st.Box.Domain, r.State)
	fmt.Fprintln(env.W(), "## Services")
	for _, row := range rows {
		fmt.Fprintf(env.W(), "- %s: %s\n", row.Name, row.State)
	}
	fmt.Fprintln(env.W(), "\n## Journal, letzte 24h")
	for _, e := range es {
		fmt.Fprintf(env.W(), "- %s %s %s %s %s %s\n", e.T.Format("15:04"), e.Actor, e.Verb, e.Service, e.Result, e.Reason)
	}
	fmt.Fprintln(env.W(), "\n## Read next\n- docs/DESIGN.md, docs/INTERFACE.md, docs/STATE.md in the laib repo\n- `laib plan` before any change; `laib propose` if your hand may not apply")
	return cli.ExitOK
}

func cmdTemplate(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return env.Fail(errors.New("usage: laib template lint <dir> | list | sync | update <service>"))
	}
	switch args[0] {
	case "lint":
		if len(args) < 2 {
			return env.Fail(errors.New("usage: laib template lint <dir>"))
		}
		return lintTemplate(env, args[1])
	case "list":
		st, err := cli.LoadState(env)
		if err != nil {
			return env.Fail(err)
		}
		avail := registry.Available(env)
		if env.JSON {
			return cli.Emit(env, map[string]any{"available": avail, "installed": st.ServiceNames()})
		}
		fmt.Fprintln(env.W(), "installiert:")
		for _, name := range st.ServiceNames() {
			t := st.Services[name].Contract.Template
			fmt.Fprintf(env.W(), "  %-20s %s/%s v%d\n", name, t.Registry, t.Name, t.Version)
		}
		fmt.Fprintln(env.W(), "verfügbar:")
		for _, name := range registry.SortedKeys(avail) {
			fmt.Fprintf(env.W(), "  %-20s %s\n", name, strings.Join(avail[name], ", "))
		}
		return cli.ExitOK
	case "sync":
		st, err := cli.LoadState(env)
		if err != nil {
			return env.Fail(err)
		}
		lines, err := registry.Sync(ctx, env, st)
		for _, l := range lines {
			fmt.Fprintln(env.W(), "registry:", l)
		}
		if err != nil {
			return env.Fail(err)
		}
		journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "template", Result: "info", Detail: "sync " + strings.Join(lines, "; ")})
		if len(args) > 1 && args[1] == "--propose" {
			// Every installed service whose template changed gets an update
			// proposal; the dial decides whether laib applies it itself.
			code := cli.ExitOK
			for _, name := range st.ServiceNames() {
				if c := registry.Update(ctx, env, st, name); c != cli.ExitOK && c != cli.ExitUsage {
					code = c
				}
			}
			return code
		}
		return cli.ExitOK
	case "update":
		if len(args) < 2 {
			return env.Fail(errors.New("usage: laib template update <service>"))
		}
		st, err := cli.LoadState(env)
		if err != nil {
			return env.Fail(err)
		}
		if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "propose") {
			return cli.Refused(env, env.Actor+" may not propose")
		}
		return registry.Update(ctx, env, st, args[1])
	}
	return env.Fail(fmt.Errorf("unknown template subcommand %q", args[0]))
}

// lintTemplate loads a template folder as if it were the only service of a
// throw-away state, so the same validation applies (ADR 0004).
func lintTemplate(env *cliEnv, dir string) int {
	tmp, err := os.MkdirTemp("", "laib-lint")
	if err != nil {
		return env.Fail(err)
	}
	defer os.RemoveAll(tmp)
	name := filepath.Base(filepath.Clean(dir))
	os.WriteFile(filepath.Join(tmp, "box.yml"), []byte("domain: lint.example\ndataDir: /mnt/data/stacks\nlanIp: 10.0.0.2\ngpu: {present: true}\nhostNetwork: ["+name+"]\nroles: {sso: "+name+"}\nlaib: {listen: 0.0.0.0}\n"), 0o644)
	os.WriteFile(filepath.Join(tmp, "policy.yml"), []byte("tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n"), 0o644)
	dst := filepath.Join(tmp, "services", name)
	os.MkdirAll(dst, 0o755)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return env.Fail(err)
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
		fmt.Fprintln(env.Errw(), err)
		return cli.ExitUsage
	}
	out, err := render.Render(st, nil)
	if err != nil {
		return env.Fail(err)
	}
	// the after: neighbours' declared data paths (their apply creates them)
	// may be mounted too: the file share's folders in media, immich, paperless
	var neighbours []string
	for dep := range st.Services {
		if dep == name {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Clean(dir)), dep, "laib.yml")); err == nil {
			var c struct {
				Data []state.DataPath `yaml:"data"`
			}
			if yaml.Unmarshal(b, &c) == nil {
				for _, d := range c.Data {
					neighbours = append(neighbours, d.Path)
				}
			}
		}
	}
	if errs := lintMounts(st, name, out, neighbours); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(env.Errw(), e)
		}
		return cli.ExitUsage
	}
	for _, phase := range []string{"pre-start", "post-deploy"} {
		if b, err := os.ReadFile(filepath.Join(dir, phase)); err == nil {
			first := strings.SplitN(string(b), "\n", 2)[0]
			if !hookInterpreters[first] {
				fmt.Fprintf(env.Errw(), "%s/%s: interpreter %q is not in the hooks image (use one of: %s)\n", dir, phase, first, strings.Join(slices.Sorted(maps.Keys(hookInterpreters)), ", "))
				return cli.ExitUsage
			}
		}
	}
	// a health declaration (#46): an http probe, or a reason why there is none
	if h := st.Services[name].Contract.Health; h.HTTP == "" && h.None == "" {
		fmt.Fprintf(env.Errw(), "%s: laib.yml declares no health (health: {http: …} or health: {none: \"why\"})\n", dir)
		return cli.ExitUsage
	}
	// a host port another template of the same registry publishes (#46: whoami and bote on 8080)
	mine := map[string]bool{}
	for _, content := range st.Services[name].Units {
		for _, hp := range state.PublishedHostPorts(content) {
			mine[hp] = true
		}
	}
	siblings, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Clean(dir)), "*", "*.container"))
	pods, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Clean(dir)), "*", "*.pod"))
	for _, f := range append(siblings, pods...) {
		other := filepath.Base(filepath.Dir(f))
		if other == name {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, hp := range state.PublishedHostPorts(string(b)) {
			if mine[hp] {
				fmt.Fprintf(env.Errw(), "%s: publishes host port %s like template %s — two services on one port never both start\n", dir, hp, other)
				return cli.ExitUsage
			}
		}
	}
	fmt.Printf("ok: %s (%d unit files, %d routes, %d secrets)\n", name, len(st.Services[name].Units), len(st.Services[name].Contract.Routes), len(st.Services[name].Contract.Secrets))
	return cli.ExitOK
}

func cmdAdopt(_ context.Context, env *cliEnv, args []string) int {
	if len(args) != 1 {
		return env.Fail(errors.New("usage: laib adopt <quadlet folder>"))
	}
	name := args[0]
	src := filepath.Join(env.Paths.Quadlet, name)
	dst := filepath.Join(env.Paths.State, "services", name)
	if _, err := os.Stat(dst); err == nil {
		return env.Fail(fmt.Errorf("services/%s exists already", name))
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return env.Fail(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return env.Fail(err)
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
	fmt.Fprintf(env.W(), "adopted %s → services/%s. Fill laib.yml (routes, secrets, data), then laib plan %s.\n", name, name, name)
	return cli.ExitOK
}

func cmdIgnore(_ context.Context, env *cliEnv, args []string) int {
	if len(args) != 1 {
		return env.Fail(errors.New("usage: laib ignore <unit>"))
	}
	p := filepath.Join(env.Paths.State, "ignore.yml")
	b, _ := os.ReadFile(p)
	content := string(b)
	if content == "" {
		content = "units:\n"
	}
	if strings.Contains(content, "- "+args[0]+"\n") {
		fmt.Fprintln(env.W(), "already ignored:", args[0])
		return cli.ExitOK
	}
	content += "  - " + args[0] + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return env.Fail(err)
	}
	fmt.Fprintln(env.W(), "ignored:", args[0])
	return cli.ExitOK
}

func cmdInit(ctx context.Context, env *cliEnv, args []string) int {
	domain, dataDir, lanIP := "", "/mnt/data/stacks", ""
	modelURL, modelLease, gpu := "", "", false
	dnsProvider, tlsMode := "", ""
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
		case "--dns-provider":
			dnsProvider = args[i+1]
		case "--tls":
			tlsMode = args[i+1]
		}
		i++
	}
	if domain == "" {
		return env.Fail(errors.New("usage: laib init --domain <domain> [--lan-ip <ip>] [--data-dir /mnt/data/stacks] [--dns-provider desec|cloudflare|…] [--tls internal] [--gpu | --model-url <openai-compatible /v1> [--model-lease <url>]]"))
	}
	if tlsMode != "" && tlsMode != "internal" {
		return env.Fail(fmt.Errorf("--tls %q: only \"internal\" (Caddy's own CA, a laptop-only trial) or nothing (Let's Encrypt; DNS-01 with --dns-provider)", tlsMode))
	}
	if !gpu && modelURL == "" {
		fmt.Fprintln(env.Errw(), "laib: no --gpu and no --model-url: the Operator, Bote and Frage will have no model until box.yml model.url is set")
	}
	for _, d := range []string{env.Paths.Home, env.Paths.State, env.Paths.Journal, env.Paths.Render, filepath.Join(env.Paths.State, "services")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return env.Fail(err)
		}
	}
	recipient, err := secrets.EnsureKey(env.Paths.Key)
	if err != nil {
		return env.Fail(err)
	}
	write := func(name, content string) error {
		p := filepath.Join(env.Paths.State, name)
		if _, err := os.Stat(p); err == nil {
			return nil
		}
		return os.WriteFile(p, []byte(content), 0o644)
	}
	model := ""
	if modelURL != "" {
		model = fmt.Sprintf("model:                    # no GPU here: the LLM is remote (a peer's llama proxy or a cloud API)\n  url: %s\n", modelURL)
		// A cloud API wants its key; a peer's proxy wants that box's model key
		// (the lease API refuses without it, #42): laib secret set MODEL_API_KEY
		// with the value the GPU box's `laib secret list` names.
		model += "  keySecret: MODEL_API_KEY  # laib secret set MODEL_API_KEY before the first apply (cloud: the API key; a peer box: its MODEL_API_KEY)\n"
		if modelLease != "" {
			model += fmt.Sprintf("  lease: %s\n", modelLease)
		}
	}
	// certificates (ADR 0007, #45): DNS-01 through the provider's API — the
	// token is a secret (laib secret set DNS_API_TOKEN); tls: internal only
	// for a trial nobody's phone has to trust
	certs := ""
	if dnsProvider != "" {
		certs += fmt.Sprintf("# certificates: DNS-01 through the provider's API; the token is the secret DNS_API_TOKEN\ndns:\n  provider: %s\n  secretRef: DNS_API_TOKEN\n", dnsProvider)
	}
	if tlsMode != "" {
		certs += fmt.Sprintf("# Caddy's own CA: a laptop-only trial; phones and their widgets need a real certificate (#45)\ntls: %s\n", tlsMode)
	}
	backup := fmt.Sprintf("backup:\n  repo: %s          # restic, on the data disk: protects against mistakes and bad updates from day one; a NAS or second repo protects against the disk\n  schedule: \"03:00\"\n", filepath.Join(filepath.Dir(dataDir), "backup", "restic"))
	if err := write("box.yml", backup+fmt.Sprintf("domain: %s\ntimezone: Europe/Berlin\ndataDir: %s\nlanIp: %s\nroles: {}\ngpu: {present: %v}\n# the closed carve-out list (ADR 0007): built-ins that need the host network\nhostNetwork: [caddy, adguard, home-assistant, file-share, stimme, llama, esphome, music-assistant]\n%s%s", domain, dataDir, lanIP, gpu, certs, model)); err != nil {
		return env.Fail(err)
	}
	if err := write("policy.yml", "tiers: [read, propose, apply, destroy]\nhands:\n  resident: {ceiling: read}\n  ops: {ceiling: apply}          # the operator (ADR 0013): observe unbounded, correct inside the envelope\n  pi: {ceiling: propose}\n  bote: {ceiling: propose}       # the Bote on Signal (ADR 0016): reads, proposes, notes; never approves\n  approver: {ceiling: apply, human: true, verbs: [approve, reject, inbox, status, note]}   # the Signal relay (#48): acts only on the admin's verified number, can only decide\n  haus: {ceiling: apply, verbs: [status, inbox, journal, restart]}   # the family's pages (haus#1): looks, restarts, decides nothing\n  claude: {ceiling: apply, ttl: 8h}\n  human: {ceiling: destroy}\napprovals:\n  destroy: {by: human, snapshot: required}\nautonomy:\n  converge: on            # the ten-minute loop re-applies drift and restarts units\n  values: auto            # laib applies, rolls back on red\n  template-update: proposal\n  patch: proposal\n  new-service: proposal   # adds surface: a human decides\n  # whatever the dial says: a change the content review marks (a script on the box, a public route,\n  # policy.yml, a token, a host folder …) waits for a human (#34)\n  # remove is never auto; a public route is always a human's call\n"); err != nil {
		return env.Fail(err)
	}
	if err := write(".gitignore", "secrets.age.tmp\n"); err != nil {
		return env.Fail(err)
	}
	if err := proposal.EnsureRepo(ctx, env.Paths.State); err != nil {
		return env.Fail(err)
	}
	if err := journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "init", Result: "info", Detail: "state repo created"}); err != nil {
		return env.Fail(err)
	}
	fmt.Fprintf(env.W(), "state repo: %s\nage recipient: %s\nage key: %s\n\nJetzt: laib recovery-sheet  → eine Seite mit Schlüssel, Repo und Passwort; ausdrucken oder in den Passwortmanager, dann --confirm\n", env.Paths.State, recipient, env.Paths.Key)
	return cli.ExitOK
}

// hookInterpreters is what ghcr.io/mdopp/laib-hooks provides (#36).
var hookInterpreters = map[string]bool{"#!/usr/bin/env python3": true, "#!/usr/bin/env bash": true, "#!/bin/bash": true, "#!/bin/sh": true, "#!/usr/bin/env sh": true}

// lintMounts: every Volume= source of the rendered units must be a declared
// data path (or under one), a rendered file, a named volume or a tmpfs —
// apply creates only declared paths, and podman refuses a missing bind
// source (#36; the failure afc1115 fixed for declared paths).
func lintMounts(st *state.State, name string, out render.Output, neighbours []string) []string {
	allowed := state.DataPaths(st, st.Services[name])
	for _, p := range neighbours {
		allowed = append(allowed, strings.ReplaceAll(p, "${DATA_DIR}", st.Box.DataDir))
	}
	var errs []string
	for key, text := range out {
		if !strings.HasPrefix(key, "quadlet/"+name+"/") {
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "Volume=") {
				continue
			}
			src, _, _ := strings.Cut(strings.TrimPrefix(line, "Volume="), ":")
			if !strings.HasPrefix(src, "/") {
				continue // a named volume
			}
			// rendered files, and what the host itself provides (devices, dbus, sysfs)
			ok := strings.HasPrefix(src, "/mnt/data/laib/render/") || strings.HasPrefix(src, "/dev/") || strings.HasPrefix(src, "/run/") || strings.HasPrefix(src, "/sys/") || strings.HasPrefix(src, "/proc/")
			for _, a := range allowed {
				if src == a || strings.HasPrefix(src, strings.TrimSuffix(a, "/")+"/") {
					ok = true
				}
			}
			if !ok {
				errs = append(errs, fmt.Sprintf("%s: %s mounts %s, which is not a declared data path (apply creates only declared paths; podman refuses a missing bind source)", key, line, src))
			}
			// the shared tree is never relabelled (#230): :z walks it on every start and fails on an entry core does not own
			share, opts := st.Box.DataDir+"/file-share/data", strings.Split(line, ":")
			relabel := len(opts) > 2 && slices.ContainsFunc(strings.Split(opts[2], ","), func(o string) bool { return o == "z" || o == "Z" })
			if (src == share || strings.HasPrefix(src, share+"/")) && (relabel || !strings.Contains(text, "\nSecurityLabelDisable=true")) {
				errs = append(errs, fmt.Sprintf("%s: %s relabels the shared file-share tree: mount it without :z/:Z and set SecurityLabelDisable=true", key, line))
			}
		}
	}
	return errs
}
