package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/templates"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

func inboxDir(env *cliEnv) string { return env.paths.Home + "/inbox" }

// runApply is the apply mechanics after the policy check: lock, secrets,
// plan, apply, journal. Used by `laib apply` and by an approval.
func runApply(ctx context.Context, env *cliEnv, st *state.State, only, reason string) int {
	unlock, err := apply.Lock(env.paths, "apply:"+env.actor)
	if errors.Is(err, apply.ErrLocked) {
		fmt.Fprintln(os.Stderr, "laib: eingeschränkt, gesperrt durch", apply.LockHolder(env.paths))
		return exitLocked
	}
	if err != nil {
		return fail(err)
	}
	defer unlock()

	sec, err := secrets.Load(env.paths.Secrets, env.paths.Key)
	if err != nil {
		return fail(err)
	}
	changed := false
	for _, s := range render.SecretNames(st) {
		if _, ok := sec[s.Name]; ok {
			continue
		}
		if s.External {
			fmt.Fprintf(os.Stderr, "laib: secret %s is external and not set; add it to secrets.age first\n", s.Name)
			return exitUsage
		}
		sec[s.Name] = secrets.Generate()
		changed = true
	}
	if changed {
		if err := secrets.Save(env.paths.Secrets, env.paths.Key, sec); err != nil {
			return fail(err)
		}
	}

	p, err := plan.Compute(ctx, st, sec, env.paths, only)
	if err != nil {
		return fail(err)
	}
	start := time.Now()
	res, err := apply.Run(ctx, st, p, env.paths, sec, reason)
	entry := journal.Entry{Actor: env.actor, Verb: "apply", Service: only, Reason: reason, Commit: res.Commit, MS: time.Since(start).Milliseconds()}
	if err != nil {
		entry.Result, entry.Detail = "red", err.Error()
		journal.Append(env.paths.Journal, entry)
		return fail(err)
	}
	if len(res.NewSecrets) > 0 {
		for k, v := range res.NewSecrets {
			sec[k] = v
		}
		if err := secrets.Save(env.paths.Secrets, env.paths.Key, sec); err != nil {
			return fail(err)
		}
	}
	entry.Result = "green"
	if !res.Green {
		entry.Result = "red"
		var bad []string
		for _, c := range res.Checks {
			if !c.OK {
				bad = append(bad, c.Service+": "+c.What+" "+c.Detail)
			}
		}
		entry.Detail = strings.Join(bad, "; ")
	}
	if p.Empty() && res.Green {
		entry.Result, entry.Detail = "info", "no change"
	}
	record(env, st, entry)
	if env.json {
		emit(env, res)
	} else {
		for _, w := range res.Written {
			fmt.Println("geschrieben:", w)
		}
		for _, u := range res.Restarted {
			fmt.Println("neu gestartet:", u)
		}
		for _, c := range res.Checks {
			mark := "ok "
			if !c.OK {
				mark = "ROT"
			}
			fmt.Printf("%s %s %s %s\n", mark, c.Service, c.What, c.Detail)
		}
		if res.Green {
			fmt.Printf("grün · %s\n", res.Commit)
		} else {
			fmt.Printf("rot · %s · angewendet, aber verify ist rot\n", res.Commit)
		}
	}
	if !res.Green {
		return exitRed
	}
	return exitOK
}

// cmdPropose has two shapes. A human with a shell: `laib propose "<grund>"`
// commits the working tree. An agent over MCP (issue #11): `--patch` (diff
// on stdin), `--set services/x/values.yml:KEY=VALUE`, `--add <template>
// [--name n] [KEY=VALUE…]`, applied in a worktree and validated first.
func cmdPropose(ctx context.Context, env *cliEnv, args []string) int {
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), "propose") {
		fmt.Fprintf(os.Stderr, "laib: %s may not propose\n", env.actor)
		return exitRefused
	}
	var edit proposal.Edit
	var words []string
	usePatch := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--patch":
			usePatch = true
		case "--set":
			if i+1 >= len(args) {
				return fail(errors.New("--set wants path:KEY=VALUE"))
			}
			k, v, ok := strings.Cut(args[i+1], "=")
			if !ok {
				return fail(errors.New("--set wants path:KEY=VALUE"))
			}
			if edit.Set == nil {
				edit.Set = map[string]string{}
			}
			edit.Set[k] = v
			i++
		case "--add":
			if i+1 >= len(args) {
				return fail(errors.New("--add wants a template name"))
			}
			tpl, err := templateFS(env, args[i+1])
			if err != nil {
				return fail(err)
			}
			edit.Add = &proposal.AddService{Name: args[i+1], Template: tpl, Values: map[string]string{}}
			i++
		case "--name":
			if edit.Add == nil || i+1 >= len(args) {
				return fail(errors.New("--name follows --add"))
			}
			edit.Add.Name = args[i+1]
			i++
		default:
			if edit.Add != nil && strings.Contains(args[i], "=") && !strings.Contains(args[i], " ") {
				k, v, _ := strings.Cut(args[i], "=")
				edit.Add.Values[k] = v
				continue
			}
			words = append(words, args[i])
		}
	}
	if usePatch {
		b, err := io.ReadAll(env.stdin())
		if err != nil {
			return fail(err)
		}
		edit.Patch = string(b)
	}
	reason := strings.Join(words, " ")
	if reason == "" {
		return fail(errors.New("usage: laib propose [--patch|--set path:KEY=VALUE|--add <template> [--name n] [KEY=VALUE…]] \"<grund>\""))
	}
	var p *proposal.Proposal
	if edit.Patch == "" && edit.Set == nil && edit.Add == nil {
		if env.mcp {
			return fail(errors.New("over MCP a proposal needs content: --patch (with the patch property), --set path:KEY=VALUE or --add <template>"))
		}
		p, err = proposal.Create(ctx, env.paths.State, inboxDir(env), env.actor, reason)
	} else {
		p, err = proposal.CreateFromEdit(ctx, env.paths.State, inboxDir(env), env.actor, reason, edit, func(dir string) error {
			st, err := state.Load(dir)
			if err != nil {
				return err
			}
			_, err = render.Render(st, nil)
			return err
		})
	}
	if err != nil {
		return fail(err)
	}
	record(env, st, journal.Entry{Actor: env.actor, Verb: "propose", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d %s", p.ID, strings.Join(p.Files, " "))})
	if env.json {
		return emit(env, p)
	}
	fmt.Printf("Vorschlag #%d angelegt (%s). Braucht Freigabe durch eine andere Hand mit Stufe apply.\n", p.ID, strings.Join(p.Files, ", "))
	return exitOK
}

func decide(ctx context.Context, env *cliEnv, args []string, approve bool) int {
	if len(args) != 1 {
		return fail(errors.New("usage: laib approve|reject <id>"))
	}
	id, err := strconv.Atoi(strings.TrimPrefix(args[0], "#"))
	if err != nil {
		return fail(err)
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	p, err := proposal.Get(inboxDir(env), id)
	if err != nil {
		return fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.actor), p.Tier) {
		fmt.Fprintf(os.Stderr, "laib: %s may not decide a %s-tier proposal\n", env.actor, p.Tier)
		return exitRefused
	}
	if !approve {
		if err := proposal.Reject(ctx, env.paths.State, inboxDir(env), p, env.actor); err != nil {
			return fail(err)
		}
		record(env, st, journal.Entry{Actor: env.actor, Verb: "reject", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d von %s", p.ID, p.Actor)})
		fmt.Printf("Vorschlag #%d abgelehnt.\n", p.ID)
		return exitOK
	}
	if err := proposal.Approve(ctx, env.paths.State, inboxDir(env), p, env.actor); err != nil {
		if errors.Is(err, proposal.ErrOwnProposal) {
			fmt.Fprintln(os.Stderr, "laib:", err)
			return exitRefused
		}
		return fail(err)
	}
	record(env, st, journal.Entry{Actor: env.actor, Verb: "approve", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d von %s", p.ID, p.Actor)})
	// The merged state is the new truth: reload and apply it.
	st, err = loadState(env)
	if err != nil {
		return fail(err)
	}
	return runApply(ctx, env, st, "", fmt.Sprintf("#%d %s (von %s)", p.ID, p.Reason, p.Actor))
}

func cmdApprove(ctx context.Context, env *cliEnv, args []string) int {
	return decide(ctx, env, args, true)
}
func cmdReject(ctx context.Context, env *cliEnv, args []string) int {
	return decide(ctx, env, args, false)
}

// templateFS finds a template: a registry under /mnt/data/laib/registries
// first (issue #14), then the built-ins embedded in the binary.
func templateFS(env *cliEnv, name string) (fs.FS, error) {
	regs, _ := filepath.Glob(filepath.Join(env.paths.Home, "registries", "*", "templates", name, "laib.yml"))
	if len(regs) > 0 {
		return os.DirFS(filepath.Dir(regs[0])), nil
	}
	sub, err := fs.Sub(templates.FS, name)
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, "laib.yml"); err != nil {
		return nil, fmt.Errorf("no template %q (built-in or in a registry)", name)
	}
	return sub, nil
}
