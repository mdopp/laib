package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
)

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
	if err := write("policy.yml", "tiers: [read, propose, apply, destroy]\nhands:\n  resident: {ceiling: read}\n  ops: {ceiling: apply}          # the operator (ADR 0013): observe unbounded, correct inside the envelope\n  laib: {ceiling: propose, verbs: [backup, diag]}   # laib's own timers (#444): the nightly backup, its check, the snapshot before an image update\n  notify: {ceiling: propose, verbs: [diag, updates, inbox, note]}   # laib-notify (#443, #441): tells the owner through the Bote, mail as fallback; reads, journals a failed send\n  pi: {ceiling: propose}\n  bote: {ceiling: propose}       # the Bote on Signal (ADR 0016): reads, proposes, notes; never approves\n  approver: {ceiling: apply, human: true, verbs: [approve, reject, inbox, status, note]}   # the Signal relay (#48): acts only on the admin's verified number, can only decide\n  haus: {ceiling: apply, verbs: [status, inbox, journal, restart, person_invite, person_pending, calendar]}   # the family's pages (haus#1): looks, restarts, invites residents (admins only on the Haus side, #360), connects calendars (#495), decides nothing\n  claude: {ceiling: apply, ttl: 8h}\n  werkstatt: {ceiling: read, verbs: [session], session: claude}   # the Werkstatt (#380): gets a claude token per session, with claude's ttl; nothing else\n  human: {ceiling: destroy}\napprovals:\n  destroy: {by: human, snapshot: required}\nautonomy:\n  converge: on            # the ten-minute loop re-applies drift and restarts units\n  values: auto            # laib applies, rolls back on red\n  template-update: proposal\n  patch: proposal\n  new-service: proposal   # adds surface: a human decides\n  # whatever the dial says: a change the content review marks (a script on the box, a public route,\n  # policy.yml, a token, a host folder …) waits for a human (#34)\n  # remove is never auto; a public route is always a human's call\n"); err != nil {
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
