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
		return fail(errors.New("usage: laib note [--kind report|incident|watch] \"<text>\""))
	}
	if kind != "report" && kind != "incident" && kind != "watch" {
		return fail(fmt.Errorf("unknown kind %q", kind))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	e := journal.Entry{Actor: env.actor, Verb: "note", Reason: kind, Result: "info", Detail: text}
	if kind == "report" || kind == "incident" {
		// reports and incidents reach the webhook like a red does
		journal.Append(env.paths.Journal, e)
		if st.Box.Notify.URL != "" {
			go postEvent(env, st.Box.Notify.URL, e)
			time.Sleep(50 * time.Millisecond)
		}
	} else {
		journal.Append(env.paths.Journal, e)
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
		return fail(errors.New("usage: laib operator enable [--model alias] [--report-at 07:00] | disable | status"))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
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
			return fail(errors.New("policy.yml: the ops hand needs ceiling apply for the operator"))
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
				return fail(errors.New("no model: set box.yml model.url or a model role first"))
			}
			sec, err := secrets.Load(env.paths.Secrets, env.paths.Key)
			if err != nil {
				return fail(err)
			}
			if _, ok := sec[render.OperatorToken]; !ok {
				store, err := token.Load(tokenPath(env))
				if err != nil {
					return fail(err)
				}
				plain, rec, err := store.Mint("ops", 0, "laib-operator")
				if err != nil {
					return fail(err)
				}
				sec[render.OperatorToken] = plain
				if err := secrets.Save(env.paths.Secrets, env.paths.Key, sec); err != nil {
					return fail(err)
				}
				journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "token", Result: "info", Detail: "mint " + rec.ID + " for ops (laib-operator)"})
			}
		}
		if err := setBoxOperator(filepath.Join(env.paths.State, "box.yml"), enable, model, reportAt); err != nil {
			return fail(err)
		}
		if enable {
			// reds wake the operator (#43): the webhook points at its event endpoint unless the owner set another
			if st.Box.Notify.URL == "" {
				if err := setBoxNotifyURL(filepath.Join(env.paths.State, "box.yml"), "http://127.0.0.1:5901/event"); err != nil {
					return fail(err)
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
			return fail(err)
		}
		return runApply(ctx, env, st, "", "operator "+args[0])
	}
	return fail(fmt.Errorf("unknown operator subcommand %q", args[0]))
}

// setBoxOperator edits the operator block of box.yml, keeping the rest.
func setBoxOperator(path string, enabled bool, model, reportAt string) error {
	var doc yaml.Node
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return err
	}
	root := doc.Content[0]
	var op *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "operator" {
			op = root.Content[i+1]
		}
	}
	if op == nil {
		op = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "operator", Tag: "!!str"}, op)
	}
	set := func(k, v string) {
		for i := 0; i+1 < len(op.Content); i += 2 {
			if op.Content[i].Value == k {
				op.Content[i+1].Value = v
				op.Content[i+1].Tag = ""
				return
			}
		}
		op.Content = append(op.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k, Tag: "!!str"}, &yaml.Node{Kind: yaml.ScalarNode, Value: v})
	}
	set("enabled", fmt.Sprint(enabled))
	if model != "" {
		set("model", model)
	}
	if reportAt != "" {
		set("reportAt", reportAt)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// setBoxNotifyURL sets notify.url in box.yml, keeping the rest.
func setBoxNotifyURL(path, url string) error {
	var doc yaml.Node
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return err
	}
	root := doc.Content[0]
	var n *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "notify" {
			n = root.Content[i+1]
		}
	}
	if n == nil {
		n = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "notify", Tag: "!!str"}, n)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "url" {
			n.Content[i+1].Value, n.Content[i+1].Tag = url, ""
			out, err := yaml.Marshal(&doc)
			if err != nil {
				return err
			}
			return os.WriteFile(path, out, 0o644)
		}
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "url", Tag: "!!str"}, &yaml.Node{Kind: yaml.ScalarNode, Value: url, Tag: "!!str"})
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
