package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
	"github.com/mdopp/laib/templates"
)

func registriesDir(env *cliEnv) string { return filepath.Join(env.Paths.Home, "registries") }

// syncRegistries clones or fetches every box.yml registry into
// /mnt/data/laib/registries/<name> at its ref (#14).
func syncRegistries(ctx context.Context, env *cliEnv, st *state.State) ([]string, error) {
	var lines []string
	for _, r := range st.Box.Registries {
		if r.Name == "" || r.URL == "" {
			return lines, errors.New("box.yml registries: name and url are required")
		}
		ref := r.Ref
		if ref == "" {
			ref = "main"
		}
		dir := filepath.Join(registriesDir(env), r.Name)
		var out string
		var err error
		if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr != nil {
			os.MkdirAll(registriesDir(env), 0o755)
			out, err = sysd.Run(ctx, "git", "clone", "-q", "--depth", "1", "--branch", ref, r.URL, dir)
		} else {
			if out, err = sysd.Run(ctx, "git", "-C", dir, "fetch", "-q", "--depth", "1", "origin", ref); err == nil {
				out, err = sysd.Run(ctx, "git", "-C", dir, "reset", "-q", "--hard", "FETCH_HEAD")
			}
		}
		if err != nil {
			return lines, fmt.Errorf("registry %s: %s", r.Name, out)
		}
		head, _ := sysd.Run(ctx, "git", "-C", dir, "rev-parse", "--short", "HEAD")
		lines = append(lines, r.Name+" @ "+head)
	}
	return lines, nil
}

// availableTemplates lists built-ins and every registry template, with the
// registry name ("builtin" for embedded ones).
func availableTemplates(env *cliEnv) map[string][]string {
	out := map[string][]string{}
	if entries, err := fs.ReadDir(templates.FS, "."); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				out[e.Name()] = append(out[e.Name()], "builtin")
			}
		}
	}
	regs, _ := filepath.Glob(filepath.Join(registriesDir(env), "*", "templates", "*", "laib.yml"))
	for _, p := range regs {
		parts := strings.Split(filepath.ToSlash(p), "/")
		name, reg := parts[len(parts)-2], parts[len(parts)-4]
		out[name] = append(out[name], reg)
	}
	return out
}

// templateUpdate proposes refreshing a service from its template's current
// files; the diff lands in the Inbox like any other proposal.
func templateUpdate(ctx context.Context, env *cliEnv, st *state.State, svcName string) int {
	svc, ok := st.Services[svcName]
	if !ok {
		return env.Fail(fmt.Errorf("unknown service %q", svcName))
	}
	tplName := svc.Contract.Template.Name
	if tplName == "" {
		tplName = svcName
	}
	tpl, err := inbox.TemplateFS(env, tplName)
	if err != nil {
		return env.Fail(err)
	}
	p, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), env.Actor, "template update "+svcName,
		proposal.Edit{Update: &proposal.UpdateService{Name: svcName, Template: tpl}}, inbox.JudgeFor(st, env.Actor))
	if err != nil {
		if strings.Contains(err.Error(), "nothing to propose") {
			return cli.ExitUsage // up to date; the caller's loop skips it
		}
		return env.Fail(err)
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "propose", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d %s (%s)", p.ID, strings.Join(p.Files, " "), p.Class)})
	if policy.Auto(st.Policy, p.Class) && len(p.Warnings) == 0 && proposal.Hand(env.Actor) != "human" { // Auto is never true for human or remove; approvals[tier] binds hands, not laib's own delegation
		return inbox.AutoApprove(ctx, env, p)
	}
	if env.JSON {
		return cli.Emit(env, p)
	}
	fmt.Fprintf(env.W(), "Vorschlag #%d: %s aus Template %s aktualisieren (%s)\n", p.ID, svcName, tplName, strings.Join(p.Files, ", "))
	return cli.ExitOK
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
