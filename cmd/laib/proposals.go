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

	"github.com/mdopp/laib/internal/token"
	"github.com/mdopp/laib/templates"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/review"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

func inboxDir(env *cliEnv) string { return env.paths.Home + "/inbox" }

// runApply is the apply mechanics after the policy check: lock, secrets,
// plan, apply, journal. Used by `laib apply` and by an approval.
func runApply(ctx context.Context, env *cliEnv, st *state.State, only, reason string) int {
	unlock, code := takeLock(env, st, "apply")
	if code != exitOK {
		return code
	}
	defer unlock()
	return applyHeld(ctx, env, st, only, reason)
}

// takeLock takes the one lock (#39) for a verb; a stale lock (its process
// gone) is taken over with a journal line, a live one refuses with exitLocked.
func takeLock(env *cliEnv, st *state.State, verb string) (func(), int) {
	unlock, stale, err := apply.Lock(env.paths, verb+":"+env.actor)
	if errors.Is(err, apply.ErrLocked) {
		fmt.Fprintln(os.Stderr, "laib: eingeschränkt, gesperrt durch", apply.LockHolder(env.paths))
		return nil, exitLocked
	}
	if err != nil {
		return nil, fail(err)
	}
	if stale != "" {
		record(env, st, journal.Entry{Actor: env.actor, Verb: verb, Result: "info", Detail: "Sperre übernommen: " + stale + " (Prozess weg)"})
	}
	return unlock, exitOK
}

// applyHeld is apply under a lock the caller holds.
func applyHeld(ctx context.Context, env *cliEnv, st *state.State, only, reason string) int {

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
			if s.Mint != "" {
				// a Laib token the service needs: minted here, once, as the hand the template names
				h, ok := st.Policy.Hands[s.Mint]
				if !ok || s.Mint == "human" {
					fmt.Fprintf(os.Stderr, "laib: secret %s wants a token for hand %q, which policy.yml does not have (or is human)\n", s.Name, s.Mint)
					return exitUsage
				}
				store, err := token.Load(tokenPath(env))
				if err != nil {
					return fail(err)
				}
				ttl := time.Duration(0)
				if h.TTL != "" {
					ttl, _ = time.ParseDuration(h.TTL)
				}
				plain, rec, err := store.Mint(s.Mint, ttl, s.Name)
				if err != nil {
					return fail(err)
				}
				journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "token", Result: "info", Detail: fmt.Sprintf("mint %s for %s (%s)", rec.ID, s.Mint, s.Name)})
				sec[s.Name] = plain
				changed = true
				continue
			}
			if s.Optional {
				continue
			}
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
	return runApplyPlan(ctx, env, st, p, sec, reason, "apply", true)
}

// runApplyPlan applies a computed plan under the lock the caller holds (or
// takes it when none is held), journals as `verb`, stores hook secrets.
func runApplyPlan(ctx context.Context, env *cliEnv, st *state.State, p *plan.Plan, sec map[string]string, reason, verb string, held bool) int {
	only := p.Only
	if !held {
		unlock, code := takeLock(env, st, verb)
		if code != exitOK {
			return code
		}
		defer unlock()
	}
	// an apply killed between write and restart (#39): its services are
	// restarted and verified now, even with a clean file diff
	for _, name := range apply.Pending(env.paths) {
		if _, ok := st.Services[name]; ok && !has(p.Services, name) {
			p.Services = append(p.Services, name)
		}
	}
	start := time.Now()
	res, err := apply.Run(ctx, st, p, env.paths, sec, reason)
	entry := journal.Entry{Actor: env.actor, Verb: verb, Service: only, Reason: reason, Commit: res.Commit, MS: time.Since(start).Milliseconds()}
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
		if len(res.Restarted) > 0 { // a restart-only run is a run: it counts against converge's budget (#39)
			entry.Result, entry.Detail = "green", "neu gestartet: "+strings.Join(res.Restarted, ", ")
		}
	}
	record(env, st, entry)
	if env.json {
		emit(env, res)
	} else {
		for _, w := range res.Written {
			fmt.Fprintln(env.w(), "geschrieben:", w)
		}
		for _, u := range res.Restarted {
			fmt.Fprintln(env.w(), "neu gestartet:", u)
		}
		for _, c := range res.Checks {
			mark := "ok "
			if !c.OK {
				mark = "ROT"
			}
			fmt.Fprintf(env.w(), "%s %s %s %s\n", mark, c.Service, c.What, c.Detail)
		}
		if res.Green {
			fmt.Fprintf(env.w(), "grün · %s\n", res.Commit)
		} else {
			fmt.Fprintf(env.w(), "rot · %s · angewendet, aber verify ist rot\n", res.Commit)
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
		return exitRefusedMsg(env, env.actor+" may not propose")
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
		p, err = proposal.Create(ctx, env.paths.State, inboxDir(env), env.actor, reason, judgeFor(st, env.actor))
	} else {
		p, err = proposal.CreateFromEdit(ctx, env.paths.State, inboxDir(env), env.actor, reason, edit, judgeFor(st, env.actor))
	}
	if err != nil {
		return fail(err)
	}
	record(env, st, journal.Entry{Actor: env.actor, Verb: "propose", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d %s (%s%s)", p.ID, strings.Join(p.Files, " "), p.Class, reasonsSuffix(p))})
	if policy.Auto(st.Policy, p.Class) && len(p.Warnings) == 0 && proposal.Hand(env.actor) != "human" { // Auto is never true for human or remove; approvals[tier] binds hands, not laib's own delegation
		// The dial says laib decides this class itself: approve as laib,
		// apply, and roll back on a red verify. A warning (a second service
		// for one purpose) and anything the judge named always go to a human.
		return autoApprove(ctx, env, p)
	}
	if env.json {
		return emit(env, p)
	}
	if p.Class == "human" {
		fmt.Fprintf(env.w(), "Vorschlag #%d angelegt (%s). Nur ein Mensch kann ihn freigeben:\n", p.ID, strings.Join(p.Files, ", "))
		for _, r := range p.Reasons {
			fmt.Fprintln(env.w(), "  -", r)
		}
	} else {
		fmt.Fprintf(env.w(), "Vorschlag #%d angelegt (%s). Braucht Freigabe durch eine andere Hand mit Stufe apply.\n", p.ID, strings.Join(p.Files, ", "))
	}
	for _, w := range p.Warnings {
		fmt.Fprintln(env.w(), "HINWEIS:", w)
	}
	return exitOK
}

// judgeFor is the judge every proposal passes: the state must render, the
// content review (internal/review) names what only a human may allow, and
// a token for a hand above the proposer's own ceiling is refused outright
// (#34: a template may never mint a token stronger than the hand that
// asked for it; a human may mint anything).
func judgeFor(st *state.State, actor string) proposal.Judge {
	return func(base, changed string) (proposal.Verdict, error) {
		cst, err := state.Load(changed)
		if err != nil {
			return proposal.Verdict{}, err
		}
		if _, err := render.Render(cst, nil); err != nil {
			return proposal.Verdict{}, fmt.Errorf("the proposed state does not render: %w", err)
		}
		r := review.Dirs(base, changed)
		hand := proposal.Hand(actor)
		if hand != "human" {
			mine := policy.Index(st.Policy.Hands[hand].Ceiling)
			for _, h := range r.Mints {
				if policy.Index(st.Policy.Hands[h].Ceiling) > mine || h == "human" {
					return proposal.Verdict{}, fmt.Errorf("%s may not add a Laib token for hand %s (ceiling %s is above its own %s): a human proposes that", actor, h, st.Policy.Hands[h].Ceiling, st.Policy.Hands[hand].Ceiling)
				}
			}
		}
		return proposal.Verdict{Human: r.Human, Mints: r.Mints, Added: r.Added, Facts: r.Facts}, nil
	}
}

func reasonsSuffix(p *proposal.Proposal) string {
	if len(p.Reasons) == 0 {
		return ""
	}
	return ": " + strings.Join(p.Reasons, "; ")
}

// autoApprove is the "canary by rollback": laib merges and applies; if the
// verify is red, it reverts the merge and applies again, and the journal
// carries both so the human sees what was tried.
func autoApprove(ctx context.Context, env *cliEnv, p *proposal.Proposal) int {
	laibEnv := &cliEnv{paths: env.paths, json: env.json, actor: "laib", in: env.in, mcp: env.mcp}
	unlock, code := takeLock(laibEnv, nil, "approve")
	if code != exitOK {
		return code
	}
	defer unlock()
	if err := proposal.ApproveChecked(ctx, env.paths.State, inboxDir(env), p, "laib", stateLoads); err != nil {
		return fail(err)
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	record(laibEnv, st, journal.Entry{Actor: "laib", Verb: "approve", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d von %s, Klasse %s per autonomy", p.ID, p.Actor, p.Class)})
	code = applyHeld(ctx, laibEnv, st, "", fmt.Sprintf("#%d %s (auto, von %s)", p.ID, p.Reason, p.Actor))
	if code == exitRed {
		if _, err := revertProposal(ctx, laibEnv, st, p.ID); err != nil {
			record(laibEnv, st, journal.Entry{Actor: "laib", Verb: "rollback", Result: "red", Detail: "revert failed: " + err.Error()})
			return exitRed
		}
		record(laibEnv, st, journal.Entry{Actor: "laib", Verb: "rollback", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d zurückgenommen: verify war rot", p.ID)})
		st, err = loadState(env)
		if err != nil {
			return fail(err)
		}
		applyHeld(ctx, laibEnv, st, "", fmt.Sprintf("rollback #%d", p.ID))
		return exitRed
	}
	return code
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
		return exitRefusedMsg(env, fmt.Sprintf("%s may not decide a %s-tier proposal", env.actor, p.Tier))
	}
	if approve && !policy.IsHuman(st.Policy, proposal.Hand(env.actor)) && policy.NeedsHuman(st.Policy, p.Tier, p.Class) {
		why := strings.Join(p.Reasons, "; ")
		if why == "" {
			why = "Stufe " + p.Tier + " verlangt einen Menschen (policy.yml approvals)"
		}
		fmt.Fprintf(os.Stderr, "laib: %s may not approve #%d — nur ein Mensch: %s\n", env.actor, p.ID, why)
		record(env, st, journal.Entry{Actor: env.actor, Verb: "approve", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d abgelehnt für %s: nur ein Mensch — %s", p.ID, env.actor, why)})
		return exitRefused
	}
	if !approve {
		if err := proposal.Reject(ctx, env.paths.State, inboxDir(env), p, env.actor); err != nil {
			return fail(err)
		}
		record(env, st, journal.Entry{Actor: env.actor, Verb: "reject", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d von %s", p.ID, p.Actor)})
		fmt.Fprintf(env.w(), "Vorschlag #%d abgelehnt.\n", p.ID)
		return exitOK
	}
	unlock, code := takeLock(env, st, "approve")
	if code != exitOK {
		return code
	}
	defer unlock()
	if err := proposal.ApproveChecked(ctx, env.paths.State, inboxDir(env), p, env.actor, stateLoads); err != nil {
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
	return applyHeld(ctx, env, st, "", fmt.Sprintf("#%d %s (von %s)", p.ID, p.Reason, p.Actor))
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

// cmdAdd is the human's install verb: `laib add <template> [--name n]
// [KEY=VALUE…] ["reason"]` — the --add proposal, approved by the same
// human, applied. Agents keep proposing; this is sugar for the shell.
func cmdAdd(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) < 1 {
		return fail(errors.New("usage: laib add <template> [--name <service>] [KEY=VALUE…] [\"<grund>\"]"))
	}
	if proposal.Hand(env.actor) != "human" {
		return exitRefusedMsg(env, "add is the human's verb; agents use propose --add")
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	tpl, err := templateFS(env, args[0])
	if err != nil {
		return fail(err)
	}
	add := &proposal.AddService{Name: args[0], Template: tpl, Values: map[string]string{}}
	var words []string
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--name" && i+1 < len(args):
			add.Name = args[i+1]
			i++
		case strings.Contains(args[i], "=") && !strings.Contains(args[i], " "):
			k, v, _ := strings.Cut(args[i], "=")
			add.Values[k] = v
		default:
			words = append(words, args[i])
		}
	}
	reason := strings.Join(words, " ")
	if reason == "" {
		reason = "add " + add.Name
	}
	p, err := proposal.CreateFromEdit(ctx, env.paths.State, inboxDir(env), env.actor, reason, proposal.Edit{Add: add}, judgeFor(st, env.actor))
	if err != nil {
		return fail(err)
	}
	for _, w := range p.Warnings {
		fmt.Fprintln(env.w(), "HINWEIS:", w)
	}
	if err := proposal.ApproveChecked(ctx, env.paths.State, inboxDir(env), p, env.actor, stateLoads); err != nil {
		return fail(err)
	}
	record(env, st, journal.Entry{Actor: env.actor, Verb: "approve", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d add %s", p.ID, add.Name)})
	st, err = loadState(env)
	if err != nil {
		return fail(err)
	}
	return runApply(ctx, env, st, "", reason)
}

// cmdInbox lists proposals: open ones by default, --all for every one.
func cmdInbox(_ context.Context, env *cliEnv, args []string) int {
	openOnly := true
	for _, a := range args {
		if a == "--all" {
			openOnly = false
		}
	}
	ps, err := proposal.List(inboxDir(env), openOnly)
	if err != nil {
		return fail(err)
	}
	domain := ""
	if st, err := loadState(env); err == nil {
		domain = st.Box.Domain
	}
	if env.json {
		if ps == nil {
			ps = []*proposal.Proposal{}
		}
		type withCard struct {
			*proposal.Proposal
			Card proposal.Decision `json:"card"`
		}
		out := make([]withCard, 0, len(ps))
		for _, p := range ps {
			out = append(out, withCard{p, proposal.Card(p, domain)})
		}
		return emit(env, out)
	}
	if len(ps) == 0 {
		fmt.Fprintln(env.w(), "Inbox leer")
		return 0
	}
	for _, p := range ps {
		fmt.Fprintf(env.w(), "#%-3d %-9s %-8s %-14s %s  (%s)\n", p.ID, p.Status, p.Tier, p.Actor, p.Reason, p.Created.Format("02.01. 15:04"))
		if p.Status == "open" {
			c := proposal.Card(p, domain)
			fmt.Fprintf(env.w(), "     %s\n", c.Summary)
			for _, i := range c.Impact {
				fmt.Fprintf(env.w(), "     · %s\n", i)
			}
			fmt.Fprintf(env.w(), "     %s\n     sicherer Knopf: %s\n", c.Reversible, c.Primary)
		}
		if p.Class == "human" {
			fmt.Fprintln(env.w(), "     nur ein Mensch kann freigeben:")
			for _, r := range p.Reasons {
				fmt.Fprintln(env.w(), "       -", r)
			}
		}
	}
	return 0
}

// stateLoads is the check every approve runs on the merged result (#41):
// the state loads and renders, or main does not move.
func stateLoads(dir string) error {
	st, err := state.Load(dir)
	if err != nil {
		return err
	}
	_, err = render.Render(st, nil)
	return err
}
