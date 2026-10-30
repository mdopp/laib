package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

func cmdIgnore(ctx context.Context, env *cliEnv, args []string) int {
	log := len(args) == 4 && args[0] == "--log" // #915: a log shape answered as harmless
	if len(args) != 1 && !log {
		return env.Fail(errors.New("usage: laib ignore <unit> | laib ignore --log <service> <muster> <grund>"))
	}
	p := filepath.Join(env.Paths.State, "ignore.yml")
	b, _ := os.ReadFile(p)
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return env.Fail(fmt.Errorf("ignore.yml: %w", err))
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	what, item := args[0], &yaml.Node{Kind: yaml.ScalarNode, Value: args[0]}
	list := yamlList(doc.Content[0], "units")
	if log {
		svc, match, reason := args[1], args[2], strings.TrimSpace(args[3])
		if st, err := cli.LoadState(env); err != nil || st.Services[svc] == nil {
			return env.Fail(fmt.Errorf("--log: Dienst %q gibt es nicht", svc))
		}
		if _, err := regexp.Compile(match); err != nil || match == "" || reason == "" {
			return env.Fail(errors.New("--log: ein Muster, das RE2 versteht, und ein Grund, warum es harmlos ist"))
		}
		what = svc + " log " + match + ": " + reason
		item = &yaml.Node{Kind: yaml.MappingNode}
		for _, kv := range [][2]string{{"service", svc}, {"match", match}, {"reason", reason}} {
			item.Content = append(item.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: kv[0]}, &yaml.Node{Kind: yaml.ScalarNode, Value: kv[1]})
		}
		list = yamlList(doc.Content[0], "logs")
	}
	for _, n := range list.Content {
		var e struct{ Service, Match string }
		if n.Value == args[0] || log && n.Decode(&e) == nil && e.Service == args[1] && e.Match == args[2] {
			fmt.Fprintln(env.W(), "already ignored:", what)
			return cli.ExitOK
		}
	}
	list.Content = append(list.Content, item)
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return env.Fail(err)
	}
	if err := os.WriteFile(p, out.Bytes(), 0o644); err != nil {
		return env.Fail(err)
	}
	// on main at once: the log review reads it there (laib state, #915)
	if err := proposal.CommitPath(ctx, env.Paths.State, "ignore.yml", "ignore: "+what, env.Actor); err != nil {
		return env.Fail(fmt.Errorf("ignore.yml geschrieben, aber nicht committet: %w", err))
	}
	fmt.Fprintln(env.W(), "ignored:", what)
	return cli.ExitOK
}

// yamlList is the sequence under key in a mapping, added when missing.
func yamlList(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			if v := m.Content[i+1]; v.Kind == yaml.SequenceNode {
				return v
			}
			m.Content[i+1] = &yaml.Node{Kind: yaml.SequenceNode} // units: with nothing under it
			return m.Content[i+1]
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &yaml.Node{Kind: yaml.SequenceNode})
	return m.Content[len(m.Content)-1]
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
	if err := write("policy.yml", state.DefaultPolicy); err != nil {
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

var yamlKey = regexp.MustCompile(`(?m)^(\s*)([A-Za-z_][A-Za-z0-9_]*):[ \t]*(\S[^\n]*)$`)

// cmdState lists the state repo's files on main, or prints one as main has
// it (#576), so an agent writes `laib propose --patch` against the real
// text. secrets.age is never shown; a secret's value is redacted wherever
// it stands, and so is a literal under a declared secret's key.
func cmdState(ctx context.Context, env *cliEnv, args []string) int {
	ls, err := exec.CommandContext(ctx, "git", "-C", env.Paths.State, "ls-tree", "-r", "--name-only", "main").Output()
	if err != nil {
		return env.Fail(fmt.Errorf("git ls-tree main: %w", err))
	}
	files := slices.DeleteFunc(strings.Split(string(ls), "\n"), func(f string) bool { return f == "" || f == "secrets.age" })
	if len(args) == 0 {
		if env.JSON {
			return cli.Emit(env, files)
		}
		fmt.Fprintln(env.W(), strings.Join(files, "\n"))
		return cli.ExitOK
	}
	p := args[0]
	if !slices.Contains(files, p) {
		return env.Fail(fmt.Errorf("%s is not a file on main (laib state lists them; secrets.age is never shown)", p))
	}
	b, err := exec.CommandContext(ctx, "git", "-C", env.Paths.State, "show", "main:"+p).Output()
	if err != nil {
		return env.Fail(fmt.Errorf("git show main:%s: %w", p, err))
	}
	sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	declared := map[string]bool{}
	for name := range sec {
		declared[name] = true
	}
	if st, err := cli.LoadState(env); err == nil {
		for _, s := range render.SecretNames(st) {
			declared[s.Name] = true
		}
	}
	text, hit := string(b), map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(sec)) {
		if v := sec[name]; v != "" && strings.Contains(text, v) {
			text, hit[name] = strings.ReplaceAll(text, v, render.Placeholder(name)), true
		}
	}
	text = yamlKey.ReplaceAllStringFunc(text, func(line string) string {
		m := yamlKey.FindStringSubmatch(line)
		if !declared[m[2]] || strings.HasPrefix(m[3], "!secret") || m[3] == render.Placeholder(m[2]) {
			return line
		}
		hit[m[2]] = true
		return m[1] + m[2] + ": " + render.Placeholder(m[2])
	})
	redacted := slices.Sorted(maps.Keys(hit))
	if env.JSON {
		res := map[string]any{"path": p, "content": text, "redacted": redacted}
		var data any // a skill script has no YAML parser (#915): the redacted text, parsed
		if yaml.Unmarshal([]byte(text), &data) == nil && strings.HasSuffix(p, ".yml") {
			if _, err := json.Marshal(data); err == nil {
				res["data"] = data
			}
		}
		return cli.Emit(env, res)
	}
	fmt.Fprint(env.W(), text)
	if len(redacted) > 0 {
		fmt.Fprintf(env.Errw(), "laib: redacted %s\n", strings.Join(redacted, ", "))
	}
	return cli.ExitOK
}
