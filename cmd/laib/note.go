package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/token"
	"gopkg.in/yaml.v3"
)

// cmdNote: an agent writes what it concluded. `--kind report` is the morning
// report (the Box page shows the latest), `incident` a repair story,
// `watch` a routine finding. Journaled as the hand; the webhook carries
// reports and incidents.
func cmdNote(_ context.Context, env *cliEnv, args []string) int {
	kind := "watch"
	var words []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--kind" && i+1 < len(args) {
			kind = args[i+1]
			i++
			continue
		}
		words = append(words, args[i])
	}
	text := strings.TrimSpace(strings.Join(words, " "))
	if len(text) > 4000 {
		text = text[:4000] + "…" // a note is a few sentences; the journal reader has a line limit (#35)
	}
	if text == "" {
		return env.fail(errors.New("usage: laib note [--kind report|incident|watch] \"<text>\""))
	}
	if kind != "report" && kind != "incident" && kind != "watch" {
		return env.fail(fmt.Errorf("unknown kind %q", kind))
	}
	st, err := loadState(env)
	if err != nil {
		return env.fail(err)
	}
	e := journal.Entry{Actor: env.actor, Verb: "note", Reason: kind, Result: "info", Detail: text}
	journal.Append(env.paths.Journal, e)
	// reports and incidents reach the webhook like a red does
	if (kind == "report" || kind == "incident") && st.Box.Notify.URL != "" {
		go postEvent(env, st.Box.Notify.URL, e)
		time.Sleep(50 * time.Millisecond)
	}
	if kind == "report" {
		dir := filepath.Join(env.paths.Home, "reports")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, time.Now().Format("2006-01-02")+".md"), []byte(text+"\n"), 0o644)
	}
	if env.json {
		return emit(env, e)
	}
	fmt.Fprintln(env.w(), "notiert:", kind)
	return exitOK
}

// LastReport returns the newest morning report, if any.
func lastReport(env *cliEnv) (string, string) {
	files, _ := filepath.Glob(filepath.Join(env.paths.Home, "reports", "*.md"))
	if len(files) == 0 {
		return "", ""
	}
	last := files[len(files)-1]
	b, _ := os.ReadFile(last)
	return strings.TrimSuffix(filepath.Base(last), ".md"), string(b)
}

// cmdOperator: `laib operator enable|disable|status`. enable mints the ops
// token (humans only), stores it as OPERATOR_TOKEN in secrets.age, sets
// box.yml operator.enabled and applies, which renders and starts the unit.
func cmdOperator(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 {
		return env.fail(errors.New("usage: laib operator enable [--model alias] [--report-at 07:00] | disable | status"))
	}
	st, err := loadState(env)
	if err != nil {
		return env.fail(err)
	}
	switch args[0] {
	case "status":
		on := st.Box.Operator.Enabled
		if env.json {
			return emit(env, map[string]any{"enabled": on, "model": st.Box.Operator.Model, "modelUrl": st.ModelURL()})
		}
		fmt.Fprintf(env.w(), "operator: enabled=%v model=%s url=%s\n", on, st.Box.Operator.Model, st.ModelURL())
		return exitOK
	case "enable", "disable":
		if proposal.Hand(env.actor) != "human" {
			return exitRefusedMsg(env, "only a human enables or disables the operator")
		}
		if !policy.Allowed(st.Policy, "ops", "apply") {
			return env.fail(errors.New("policy.yml: the ops hand needs ceiling apply for the operator"))
		}
		enable := args[0] == "enable"
		model, reportAt := "", ""
		for i := 1; i+1 < len(args); i += 2 {
			switch args[i] {
			case "--model":
				model = args[i+1]
			case "--report-at":
				reportAt = args[i+1]
			}
		}
		if enable {
			if st.ModelURL() == "" {
				return env.fail(errors.New("no model: set box.yml model.url or a model role first"))
			}
			sec, err := secrets.Load(env.paths.Secrets, env.paths.Key)
			if err != nil {
				return env.fail(err)
			}
			if _, ok := sec[render.OperatorToken]; !ok {
				store, err := token.Load(tokenPath(env))
				if err != nil {
					return env.fail(err)
				}
				plain, rec, err := store.Mint("ops", 0, "laib-operator")
				if err != nil {
					return env.fail(err)
				}
				sec[render.OperatorToken] = plain
				if err := secrets.Save(env.paths.Secrets, env.paths.Key, sec); err != nil {
					return env.fail(err)
				}
				journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "token", Result: "info", Detail: "mint " + rec.ID + " for ops (laib-operator)"})
			}
		}
		if err := setBox(filepath.Join(env.paths.State, "box.yml"), "operator", "enabled", fmt.Sprint(enable), "model", model, "reportAt", reportAt); err != nil {
			return env.fail(err)
		}
		if enable {
			// reds wake the operator (#43): the webhook points at its event endpoint unless the owner set another
			if st.Box.Notify.URL == "" {
				if err := setBox(filepath.Join(env.paths.State, "box.yml"), "notify", "url", "http://127.0.0.1:5901/event"); err != nil {
					return env.fail(err)
				}
			}
		}
		if !enable {
			// nothing keeps running with a valid token (#41): the ops tokens go
			if store, err := token.Load(tokenPath(env)); err == nil {
				for _, r := range store.List() {
					if r.Hand == "ops" {
						store.Revoke(r.ID)
						journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "token", Result: "info", Detail: "revoke " + r.ID + " (operator disabled)"})
					}
				}
			}
		}
		journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "operator", Result: "info", Detail: args[0]})
		st, err = loadState(env)
		if err != nil {
			return env.fail(err)
		}
		return runApply(ctx, env, st, "", "operator "+args[0])
	}
	return env.fail(fmt.Errorf("unknown operator subcommand %q", args[0]))
}

// setBox sets keys of one top-level block of box.yml (operator, notify),
// keeping the rest of the file and its comments. A missing block is
// appended; an empty value leaves its key alone.
func setBox(path, block string, kv ...string) error {
	var doc yaml.Node
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return err
	}
	root := doc.Content[0]
	var m *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == block {
			m = root.Content[i+1]
		}
	}
	if m == nil {
		m = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: block, Tag: "!!str"}, m)
	}
	set := func(k, v string) {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == k {
				m.Content[i+1].Value, m.Content[i+1].Tag = v, ""
				return
			}
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k, Tag: "!!str"}, &yaml.Node{Kind: yaml.ScalarNode, Value: v})
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			set(kv[i], kv[i+1])
		}
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
