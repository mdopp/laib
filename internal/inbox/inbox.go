// Package inbox is the change flow (#309): propose, the judge every
// proposal passes (the state must render, the content review names what
// only a human may allow, a token above the proposer's ceiling is refused),
// approve, reject, the canary auto-approve, add, the inbox listing and
// rollback by proposal. Trust kernel: a proposal's class comes from its
// content (ADR 0019 §2b).
package inbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/templates"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/review"
	"github.com/mdopp/laib/internal/state"
)

// Propose has two shapes. A human with a shell: `laib propose "<grund>"`
// commits the working tree. An agent over MCP (issue #11): `--patch` (diff
// on stdin), `--set services/x/values.yml:KEY=VALUE`, `--add <template>
// [--name n] [KEY=VALUE…]`, applied in a worktree and validated first.
func Propose(ctx context.Context, env *cli.Env, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), "propose") {
		return cli.Refused(env, env.Actor+" may not propose")
	}
	var edit proposal.Edit
	var anchor proposal.Anchor
	var words []string
	usePatch := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--patch":
			usePatch = true
		case "--finding", "--evidence", "--undo", "--source": // the anchor (#544)
			if i+1 >= len(args) {
				return env.Fail(fmt.Errorf("%s wants a value", args[i]))
			}
			v := strings.TrimSpace(args[i+1])
			switch args[i] {
			case "--finding":
				anchor.Finding = strings.Join(strings.Fields(v), " ")
			case "--evidence":
				anchor.Evidence = v
			case "--undo":
				anchor.Undo = v
			default:
				anchor.Sources = append(anchor.Sources, v)
			}
			i++
		case "--set":
			if i+1 >= len(args) {
				return env.Fail(errors.New("--set wants path:KEY=VALUE"))
			}
			k, v, ok := strings.Cut(args[i+1], "=")
			if !ok {
				return env.Fail(errors.New("--set wants path:KEY=VALUE"))
			}
			if edit.Set == nil {
				edit.Set = map[string]string{}
			}
			edit.Set[k] = v
			i++
		case "--grant":
			if i+2 >= len(args) {
				return env.Fail(errors.New("--grant wants <hand> <verb>"))
			}
			if edit.Write, err = grantEdit(env, st, args[i+1], args[i+2]); err != nil {
				return env.Fail(err)
			}
			i += 2
		case "--add":
			if i+1 >= len(args) {
				return env.Fail(errors.New("--add wants a template name"))
			}
			tpl, err := TemplateFS(env, args[i+1])
			if err != nil {
				return env.Fail(err)
			}
			edit.Add = &proposal.AddService{Name: args[i+1], Template: tpl, Values: map[string]string{}}
			i++
		case "--name":
			if edit.Add == nil || i+1 >= len(args) {
				return env.Fail(errors.New("--name follows --add"))
			}
			edit.Add.Name = args[i+1]
			i++
		default:
			if edit.Add != nil && isValueArg(args[i]) {
				k, v, _ := strings.Cut(args[i], "=")
				edit.Add.Values[k] = v
				continue
			}
			words = append(words, args[i])
		}
	}
	if usePatch {
		b, err := io.ReadAll(env.Stdin())
		if err != nil {
			return env.Fail(err)
		}
		edit.Patch = string(b)
	}
	reason := strings.Join(words, " ")
	if reason == "" {
		return env.Fail(errors.New("usage: laib propose [--patch|--set path:KEY=VALUE|--add <template> [--name n] [KEY=VALUE…]|--grant <hand> <verb>] \"<grund>\""))
	}
	// the finding a proposal answers is one diag shows now, or laib measures nothing (#544)
	if anchor.Finding != "" && Present != nil && !Present(ctx, env, st, anchor.Finding) {
		return env.Fail(fmt.Errorf("Befund %q steht nicht in laib diag: nenne probe und Dienst eines Befunds, den diag jetzt zeigt", anchor.Finding))
	}
	var p *proposal.Proposal
	if edit.Patch == "" && edit.Set == nil && edit.Add == nil && edit.Write == nil {
		if env.MCP {
			return env.Fail(errors.New("over MCP a proposal needs content: --patch (with the patch property), --set path:KEY=VALUE, --add <template> or --grant <hand> <verb>"))
		}
		p, err = proposal.Create(ctx, env.Paths.State, cli.InboxDir(env), env.Actor, reason, JudgeFor(st, env.Actor))
	} else {
		p, err = proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), env.Actor, reason, edit, JudgeFor(st, env.Actor))
	}
	if err != nil {
		return env.Fail(err)
	}
	if err := Anchor(cli.InboxDir(env), p, anchor); err != nil {
		return env.Fail(err)
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "propose", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d %s (%s%s)", p.ID, strings.Join(p.Files, " "), p.Class, reasonsSuffix(p))})
	if policy.Auto(st.Policy, p.Class) && len(p.Warnings) == 0 && proposal.Hand(env.Actor) != "human" { // Auto is never true for human or remove; approvals[tier] binds hands, not laib's own delegation
		// The dial says laib decides this class itself: approve as laib,
		// apply, and roll back on a red verify. A warning (a second service
		// for one purpose) and anything the judge named always go to a human.
		return AutoApprove(ctx, env, p)
	}
	if env.JSON {
		return cli.Emit(env, p)
	}
	if p.Class == "human" {
		fmt.Fprintf(env.W(), "Vorschlag #%d angelegt (%s). Nur ein Mensch kann ihn freigeben:\n", p.ID, strings.Join(p.Files, ", "))
		for _, r := range p.Reasons {
			fmt.Fprintln(env.W(), "  -", r)
		}
	} else {
		fmt.Fprintf(env.W(), "Vorschlag #%d angelegt (%s). Braucht Freigabe durch eine andere Hand mit Stufe apply.\n", p.ID, strings.Join(p.Files, ", "))
	}
	for _, w := range p.Warnings {
		fmt.Fprintln(env.W(), "HINWEIS:", w)
	}
	return cli.ExitOK
}

// JudgeFor is the judge every proposal passes: the state must render, the
// content review (internal/review) names what only a human may allow, and
// a token for a hand above the proposer's own ceiling is refused outright
// (#34: a template may never mint a token stronger than the hand that
// asked for it; a human may mint anything).
func JudgeFor(st *state.State, actor string) proposal.Judge {
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
		return proposal.Verdict{Human: r.Human, Mints: r.Mints, Added: r.Added, Facts: r.Facts, Ask: r.Ask, Snap: r.Snapshot}, nil
	}
}

// Present says whether laib diag shows a finding ("<probe> <service>")
// now; main sets it to the diag chore's probes (#544).
var Present func(ctx context.Context, env *cli.Env, st *state.State, finding string) bool

// Anchor stores what a proposal answers (#544). A proposal that rests on an
// outside source is class human: what the web says, a person checks.
func Anchor(inbox string, p *proposal.Proposal, a proposal.Anchor) error {
	if a.Finding == "" && a.Evidence == "" && a.Undo == "" && len(a.Sources) == 0 {
		return nil
	}
	p.Anchor = a
	if len(a.Sources) > 0 {
		p.Class = "human"
		p.Reasons = append(p.Reasons, "stützt sich auf eine Quelle aus dem Netz: "+strings.Join(a.Sources, ", "))
	}
	return proposal.Save(inbox, p)
}

func reasonsSuffix(p *proposal.Proposal) string {
	if len(p.Reasons) == 0 {
		return ""
	}
	return ": " + strings.Join(p.Reasons, "; ")
}

// AutoApprove is the "canary by rollback": laib merges and applies; if the
// verify is red, it reverts the merge and applies again, and the journal
// carries both so the human sees what was tried.
func AutoApprove(ctx context.Context, env *cli.Env, p *proposal.Proposal) int {
	laibEnv := &cli.Env{Paths: env.Paths, JSON: env.JSON, Actor: "laib", In: env.In, MCP: env.MCP}
	unlock, code := apply.TakeLock(laibEnv, nil, "approve")
	if code != cli.ExitOK {
		return code
	}
	defer unlock()
	if err := proposal.ApproveChecked(ctx, env.Paths.State, cli.InboxDir(env), p, "laib", stateLoads); err != nil {
		return env.Fail(err)
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	cli.Record(laibEnv, st, journal.Entry{Actor: "laib", Verb: "approve", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d von %s, Klasse %s per autonomy", p.ID, p.Actor, p.Class)})
	code = apply.ApplyHeld(ctx, laibEnv, st, "", fmt.Sprintf("#%d %s (auto, von %s)", p.ID, p.Reason, p.Actor))
	if code == cli.ExitRed {
		if _, err := RevertProposal(ctx, laibEnv, st, p.ID); err != nil {
			cli.Record(laibEnv, st, journal.Entry{Actor: "laib", Verb: "rollback", Result: "red", Detail: "revert failed: " + err.Error()})
			return cli.ExitRed
		}
		cli.Record(laibEnv, st, journal.Entry{Actor: "laib", Verb: "rollback", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d zurückgenommen: verify war rot", p.ID)})
		st, err = cli.LoadState(env)
		if err != nil {
			return env.Fail(err)
		}
		apply.ApplyHeld(ctx, laibEnv, st, "", fmt.Sprintf("rollback #%d", p.ID))
		return cli.ExitRed
	}
	return code
}

func Decide(ctx context.Context, env *cli.Env, args []string, approve bool) int {
	if len(args) < 1 || approve && len(args) > 1 {
		return env.Fail(errors.New("usage: laib approve <id> · laib reject <id> [\"<grund>\"]"))
	}
	id, err := strconv.Atoi(strings.TrimPrefix(args[0], "#"))
	if err != nil {
		return env.Fail(err)
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	p, err := proposal.Get(cli.InboxDir(env), id)
	if err != nil {
		return env.Fail(err)
	}
	if !policy.Allowed(st.Policy, proposal.Hand(env.Actor), p.Tier) {
		return cli.Refused(env, fmt.Sprintf("%s may not decide a %s-tier proposal", env.Actor, p.Tier))
	}
	if approve && !policy.IsHuman(st.Policy, proposal.Hand(env.Actor)) && policy.NeedsHuman(st.Policy, p.Tier, p.Class) {
		why := strings.Join(p.Reasons, "; ")
		if why == "" {
			why = "Stufe " + p.Tier + " verlangt einen Menschen (policy.yml approvals)"
		}
		fmt.Fprintf(env.Errw(), "laib: %s may not approve #%d — nur ein Mensch: %s\n", env.Actor, p.ID, why)
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "approve", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d abgelehnt für %s: nur ein Mensch — %s", p.ID, env.Actor, why)})
		return cli.ExitRefused
	}
	// the chat decides only what is not destructive (#441, ADR 0016): what the review marks
	// for a human, a removal and every destroy are the Inbox's, behind the second factor
	if approve && proposal.Hand(env.Actor) != "human" && (p.Tier == "destroy" || p.Class == "human" || p.Class == "remove") {
		fmt.Fprintf(env.Errw(), "laib: #%d nur in der Inbox, mit dem zweiten Faktor: https://%s.%s/inbox\n", p.ID, st.Box.Laib.Host, st.Box.Domain)
		return cli.ExitRefused
	}
	if !approve {
		// the owner's reason is the Operator's strongest lesson (#544); "nicht jetzt" is free
		why := strings.Join(args[1:], " ")
		if err := proposal.Reject(ctx, env.Paths.State, cli.InboxDir(env), p, env.Actor, why); err != nil {
			return env.Fail(err)
		}
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "reject", Reason: p.Reason, Result: "info", Detail: strings.TrimSuffix(fmt.Sprintf("#%d von %s: %s", p.ID, p.Actor, why), ": ")})
		fmt.Fprintf(env.W(), "Vorschlag #%d abgelehnt.\n", p.ID)
		return cli.ExitOK
	}
	unlock, code := apply.TakeLock(env, st, "approve")
	if code != cli.ExitOK {
		return code
	}
	release := sync.OnceFunc(unlock)
	defer release()
	for _, name := range p.Snapshot { // a service with data (#528): its snapshot before the merge, fail closed
		if svc := st.Services[name]; svc != nil {
			if code := backup.Pre(ctx, env, st, svc, "approve", "pre-update", true); code != cli.ExitOK {
				return code
			}
		}
	}
	if err := proposal.ApproveChecked(ctx, env.Paths.State, cli.InboxDir(env), p, env.Actor, stateLoads); err != nil {
		if errors.Is(err, proposal.ErrOwnProposal) {
			fmt.Fprintln(env.Errw(), "laib:", err)
			return cli.ExitRefused
		}
		return env.Fail(err)
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "approve", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d von %s", p.ID, p.Actor)})
	// The merged state is the new truth: reload and apply it.
	st, err = cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	FileWishes(ctx, env, st, p)
	if len(p.Files) > 0 && !slices.ContainsFunc(p.Files, func(f string) bool { return !strings.HasPrefix(f, "wishes/") && !strings.HasPrefix(f, "repairs/") }) {
		release() // a wish changes nothing on the box: no apply; a repair's verb takes the lock itself
		return RunRepairs(ctx, env, st, p)
	}
	return apply.ApplyHeld(ctx, env, st, "", fmt.Sprintf("#%d %s (von %s)", p.ID, p.Reason, p.Actor))
}

func Approve(ctx context.Context, env *cli.Env, args []string) int {
	return Decide(ctx, env, args, true)
}
func Reject(ctx context.Context, env *cli.Env, args []string) int {
	return Decide(ctx, env, args, false)
}

// TemplateFS finds a template: a registry under /mnt/data/laib/registries
// first (issue #14), then the built-ins embedded in the binary.
func TemplateFS(env *cli.Env, name string) (fs.FS, error) {
	regs, _ := filepath.Glob(filepath.Join(env.Paths.Home, "registries", "*", "templates", name, "laib.yml"))
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

// Add is the human's install verb: `laib add <template> [--name n]
// [KEY=VALUE…] ["reason"]` — the --add proposal, approved by the same
// human, applied. Agents keep proposing; this is sugar for the shell.
func Add(ctx context.Context, env *cli.Env, args []string) int {
	if len(args) < 1 {
		return env.Fail(errors.New("usage: laib add <template> [--name <service>] [KEY=VALUE…] [\"<grund>\"]"))
	}
	if proposal.Hand(env.Actor) != "human" {
		return cli.Refused(env, "add is the human's verb; agents use propose --add")
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	tpl, err := TemplateFS(env, args[0])
	if err != nil {
		return env.Fail(err)
	}
	add := &proposal.AddService{Name: args[0], Template: tpl, Values: map[string]string{}}
	var words []string
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--name" && i+1 < len(args):
			add.Name = args[i+1]
			i++
		case isValueArg(args[i]):
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
	// the same add again after its apply was cut short (lock, power cut):
	// the service is in the state already, so finish the apply (#294)
	if svc, ok := st.Services[add.Name]; ok && svc.Contract.Template.Name == args[0] {
		fmt.Fprintf(env.W(), "%s ist schon im Zustand: rolle es aus\n", add.Name)
		return apply.ApplyLocked(ctx, env, st, add.Name, reason)
	}
	p, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), env.Actor, reason, proposal.Edit{Add: add}, JudgeFor(st, env.Actor))
	if err != nil {
		return env.Fail(err)
	}
	for _, w := range p.Warnings {
		fmt.Fprintln(env.W(), "HINWEIS:", w)
	}
	if err := proposal.ApproveChecked(ctx, env.Paths.State, cli.InboxDir(env), p, env.Actor, stateLoads); err != nil {
		return env.Fail(err)
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "approve", Reason: p.Reason, Result: "info", Detail: fmt.Sprintf("#%d add %s", p.ID, add.Name)})
	st, err = cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	return apply.ApplyLocked(ctx, env, st, "", reason)
}

// List lists proposals: open ones by default, --all for every one.
func List(_ context.Context, env *cli.Env, args []string) int {
	openOnly := true
	for _, a := range args {
		if a == "--all" {
			openOnly = false
		}
	}
	ps, err := proposal.List(cli.InboxDir(env), openOnly)
	if err != nil {
		return env.Fail(err)
	}
	domain := ""
	if st, err := cli.LoadState(env); err == nil {
		domain = st.Box.Domain
	}
	if env.JSON {
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
		return cli.Emit(env, out)
	}
	if len(ps) == 0 {
		fmt.Fprintln(env.W(), "Inbox leer")
		return 0
	}
	for _, p := range ps {
		fmt.Fprintf(env.W(), "#%-3d %-9s %-8s %-14s %s  (%s)\n", p.ID, p.Status, p.Tier, p.Actor, p.Reason, p.Created.Format("02.01. 15:04"))
		if p.Status == "open" {
			c := proposal.Card(p, domain)
			fmt.Fprintf(env.W(), "     %s\n", c.Summary)
			for _, i := range c.Impact {
				fmt.Fprintf(env.W(), "     · %s\n", i)
			}
			fmt.Fprintf(env.W(), "     %s\n     sicherer Knopf: %s\n", c.Reversible, c.Primary)
		}
		if p.Class == "human" {
			fmt.Fprintln(env.W(), "     nur ein Mensch kann freigeben:")
			for _, r := range p.Reasons {
				fmt.Fprintln(env.W(), "       -", r)
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

// valueKey is a template value's name at the start of an argument.
var valueKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*=`)

// isValueArg: KEY=VALUE, not a word of the reason. A value may hold blanks
// when its key is a value's name ("SMTP_FROM=Die Box <box@…>", #359).
func isValueArg(a string) bool {
	return valueKey.MatchString(a) || strings.Contains(a, "=") && !strings.Contains(a, " ")
}

const defaultsReason = "Neue Standard-Verben für eingebaute Hände: "

// ProposeDefaults files one proposal that adds the verbs the built-in hands
// lack against state.DefaultPolicy (#575), run by `laib updates --run` right
// after laib-images' ExecStartPre updated Laib, so the new release's
// defaults decide. policy.yml makes it class human. Nothing while one is
// open, nothing again for verbs the owner rejected; a hand whose list is
// not on one line stays the owner's. nil without a proposal.
func ProposeDefaults(ctx context.Context, env *cli.Env, st *state.State) (*proposal.Proposal, error) {
	text, err := os.ReadFile(filepath.Join(env.Paths.State, "policy.yml"))
	if err != nil {
		return nil, err
	}
	miss := policy.Missing(st.Policy)
	var adds []string
	for _, hand := range slices.Sorted(maps.Keys(miss)) {
		if b, err := policy.Grant(text, hand, miss[hand]...); err == nil {
			text, adds = b, append(adds, hand+" +"+strings.Join(miss[hand], " +"))
		}
	}
	reason := defaultsReason + strings.Join(adds, ", ")
	all, _ := proposal.List(cli.InboxDir(env), false)
	if len(adds) == 0 || slices.ContainsFunc(all, func(p *proposal.Proposal) bool {
		return (p.Status == "open" && strings.HasPrefix(p.Reason, defaultsReason)) || (p.Status == "rejected" && p.Reason == reason)
	}) {
		return nil, nil
	}
	p, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), env.Actor, reason, proposal.Edit{Write: map[string][]byte{"policy.yml": text}}, JudgeFor(st, env.Actor))
	if err != nil {
		return nil, err
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "propose", Reason: reason, Result: "info", Detail: fmt.Sprintf("#%d policy.yml (%s)", p.ID, p.Class)})
	return p, nil
}

// grantEdit is `laib propose --grant <hand> <verb>` (#575): one verb added
// to one hand's verb list, as a policy.yml line edit.
func grantEdit(env *cli.Env, st *state.State, hand, verb string) (map[string][]byte, error) {
	h, ok := st.Policy.Hands[hand]
	switch {
	case !policy.KnownVerbs[verb]:
		return nil, fmt.Errorf("--grant: %q ist kein Verb von laib", verb)
	case !ok:
		return nil, fmt.Errorf("--grant: policy.yml kennt keine Hand %q", hand)
	case len(h.Verbs) == 0:
		return nil, fmt.Errorf("--grant: %s hat keine Verbliste und darf schon jedes Verb bis zu ihrer Obergrenze", hand)
	case slices.Contains(h.Verbs, verb):
		return nil, fmt.Errorf("--grant: %s darf %s schon", hand, verb)
	}
	text, err := os.ReadFile(filepath.Join(env.Paths.State, "policy.yml"))
	if err != nil {
		return nil, err
	}
	b, err := policy.Grant(text, hand, verb)
	return map[string][]byte{"policy.yml": b}, err
}
