package proposal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mdopp/laib/internal/state"
)

// Edit is one change an agent asks for without file access (issue #11).
type Edit struct {
	Patch  string            // unified diff against the repo root
	Set    map[string]string // "services/x/values.yml:KEY" → value (or "!secret NAME")
	Add    *AddService
	Update *UpdateService        // replace a service's template files, keep values.yml and add the new keys (#14, #215)
	Write  map[string][]byte     // path → the file's new content, written whole (laib propose --grant, #575)
	At     map[string]*yaml.Node // "box.yml:people.mama.signal" → its value, nil removes it (state.WithPath, #598)
}

// UpdateService refreshes services/<name> from a newer template folder.
type UpdateService struct {
	Name     string
	Template fs.FS
}

// Verdict is what the judge says about a change (internal/review).
type Verdict struct {
	Human []string // reasons only a human may allow it; empty = the dial decides
	Mints []string // hands the change mints Laib tokens for
	Added []string // services the change adds
	Facts []string // plain facts for the decision card (#58)
	Ask   []string // a person decides, not the dial (#528): a warning
	Snap  []string // services snapshotted before the approved change applies (#528)
	// Changes: what the change does, before → after, for the card (#616)
	Changes []Change
}

// Judge reads the change — a checkout of main and one of the change — and
// says what only a human may allow. It may refuse the proposal outright
// (an error: nothing is kept), e.g. a token stronger than the proposer.
type Judge func(base, changed string) (Verdict, error)

// classOf is the class the dial is keyed by: human when the judge says so,
// new-service when a service is added (whatever the edit's shape), else the
// edit's shape.
func classOf(kind string, v Verdict) string {
	switch {
	case len(v.Human) > 0:
		return "human"
	case len(v.Added) > 0:
		return "new-service"
	}
	return kind
}

// Class names the shape of the edit (the proposal's Kind).
func (e Edit) Class() string {
	switch {
	case e.Add != nil:
		return "new-service"
	case e.Update != nil:
		return "template-update"
	case e.Patch != "":
		return "patch"
	case len(e.Set) > 0 || len(e.At) > 0:
		return "values"
	}
	return "tree"
}

// AddService copies a template folder into services/<name>.
type AddService struct {
	Name     string
	Template fs.FS // the template folder as a file system
	Values   map[string]string
}

// CreateFromEdit applies the edit in a temporary worktree on a new branch,
// validates the result with the state loader, lets the judge read it
// against main, commits and records the inbox entry. The main checkout is
// never touched, so a bad patch leaves nothing behind. The judge is
// injected so this package stays free of render and review.
func CreateFromEdit(ctx context.Context, repo, inbox, actor, reason string, e Edit, judge Judge) (*Proposal, error) {
	if err := CommitDirty(ctx, repo, actor); err != nil {
		return nil, err
	}
	id, err := nextID(inbox)
	if err != nil {
		return nil, err
	}
	branch := fmt.Sprintf("proposal/%d", id)
	wt, err := os.MkdirTemp("", "laib-proposal")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(wt)
	defer git(ctx, repo, "worktree", "prune")
	if _, err := git(ctx, repo, "worktree", "add", "-q", "-b", branch, wt, "main"); err != nil {
		return nil, err
	}
	fail := func(err error) (*Proposal, error) {
		git(ctx, repo, "worktree", "remove", "--force", wt)
		git(ctx, repo, "branch", "-q", "-D", branch)
		return nil, err
	}
	if e.Patch != "" {
		pf := filepath.Join(wt, ".laib-patch")
		if err := os.WriteFile(pf, []byte(e.Patch), 0o600); err != nil {
			return fail(err)
		}
		if out, err := git(ctx, wt, "apply", "--index", "--unidiff-zero", "--whitespace=nowarn", pf); err != nil {
			return fail(fmt.Errorf("patch does not apply: %s", out))
		}
		os.Remove(pf)
	}
	for file, b := range e.Write {
		if err := os.WriteFile(filepath.Join(wt, file), b, 0o644); err != nil {
			return fail(err)
		}
	}
	for key, val := range e.Set {
		file, k, ok := strings.Cut(key, ":")
		if !ok {
			return fail(fmt.Errorf("--set wants path:KEY=VALUE, got %q", key))
		}
		if err := setValue(filepath.Join(wt, file), k, val); err != nil {
			return fail(err)
		}
	}
	for key, v := range e.At {
		file, at, ok := strings.Cut(key, ":")
		if !ok {
			return fail(fmt.Errorf("--path wants file:path=VALUE, got %q", key))
		}
		if err := setAt(filepath.Join(wt, file), at, v); err != nil {
			return fail(err)
		}
	}
	if e.Add != nil {
		if err := addService(wt, e.Add); err != nil {
			return fail(err)
		}
		if err := wireBox(wt, e.Add.Name); err != nil {
			return fail(err)
		}
	}
	var updateWarnings []string
	if e.Update != nil {
		if updateWarnings, err = updateService(wt, e.Update); err != nil {
			return fail(err)
		}
	}
	proposed, err := state.Load(wt)
	if err != nil {
		return fail(fmt.Errorf("the proposed state does not load:\n%w", err))
	}
	var verdict Verdict
	if judge != nil {
		verdict, err = judge(repo, wt)
		if err != nil {
			return fail(err)
		}
	}
	if _, err := git(ctx, wt, "add", "-A"); err != nil {
		return fail(err)
	}
	status, _ := git(ctx, wt, "status", "--porcelain")
	// only the base copies (#511): the service is as the template has it, and that alone
	// is no proposal; the base is recorded with the next real change
	onlyBase := e.Update != nil && !slices.ContainsFunc(strings.Split(strings.TrimSpace(status), "\n"), func(l string) bool {
		return !strings.Contains(l, "services/"+e.Update.Name+"/"+state.BaseDir+"/")
	})
	if strings.TrimSpace(status) == "" || onlyBase {
		return fail(errors.New("nothing to propose: the edit changes nothing"))
	}
	if _, err := git(ctx, wt, "commit", "-q", "-m", "proposal: "+reason, "--author", actor+" <"+actor+"@laib>"); err != nil {
		return fail(err)
	}
	p := &Proposal{ID: id, Actor: actor, Reason: reason, Branch: branch, Tier: "apply", Status: "open", Created: now(),
		Kind: e.Class(), Class: classOf(e.Class(), verdict), Reasons: verdict.Human, Mints: verdict.Mints, Adds: verdict.Added, Facts: verdict.Facts, Snapshot: verdict.Snap, Changes: verdict.Changes}
	for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			p.Files = append(p.Files, f[len(f)-1])
		}
	}
	p.Warnings = slices.Concat(ownWarnings(repo, proposed.Warnings, append(servicesOf(p), p.Adds...)), updateWarnings, verdict.Ask)
	git(ctx, repo, "worktree", "remove", "--force", wt)
	return p, Save(inbox, p)
}

// ownWarnings: the state's warnings this change brings, or that name a
// service it touches (#616). A warning the box had before and that is
// about other services (a second service for one purpose elsewhere) is
// the box's, shown on the status page — not on every card, and not a
// reason to keep each later proposal from the dial.
func ownWarnings(repo string, warnings, touched []string) []string {
	before, err := state.Load(repo)
	if err != nil {
		return warnings
	}
	return slices.DeleteFunc(slices.Clone(warnings), func(w string) bool {
		return slices.Contains(before.Warnings, w) && !slices.ContainsFunc(touched, func(s string) bool { return mentions(w, s) })
	})
}

// setValue sets one key of a values.yml to a string, or "!secret NAME" to a
// tagged node; the other entries keep their comments and tags.
func setValue(path, key, val string) error {
	v := &yaml.Node{Kind: yaml.ScalarNode, Value: val, Tag: "!!str"}
	if name, ok := strings.CutPrefix(val, "!secret "); ok {
		v.Value, v.Tag = name, "!secret"
	}
	return setAt(path, key, v)
}

// setAt sets the value at a dotted path of one file as state.WithPath does
// (#598); a missing file starts empty.
func setAt(path, at string, v *yaml.Node) error {
	b, _ := os.ReadFile(path)
	b, err := state.WithPath(b, filepath.Base(path), at, v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// addService copies the template files into services/<name>, writes
// values.yml from values.example.yml plus the given values, and sets the
// contract's name to the service name.
func addService(wt string, a *AddService) error {
	dst := filepath.Join(wt, "services", a.Name)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("services/%s exists", a.Name)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := fs.ReadDir(a.Template, ".")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(a.Template, e.Name())
		if err != nil {
			return err
		}
		name := e.Name()
		if name == "values.example.yml" {
			name = "values.yml"
		}
		if name == "laib.yml" {
			// the template's own name line is "name: <template>"; rewrite it.
			lines := strings.Split(string(b), "\n")
			for i, l := range lines {
				if strings.HasPrefix(l, "name: ") {
					lines[i] = "name: " + a.Name
				}
			}
			b = []byte(strings.Join(lines, "\n"))
		}
		mode := fs.FileMode(0o644)
		if isHook(name) {
			mode = 0o755
		}
		if name != "values.yml" && name != "README.md" {
			if err := writeBase(dst, name, b); err != nil {
				return err
			}
		}
		if err := os.WriteFile(filepath.Join(dst, name), b, mode); err != nil {
			return err
		}
	}
	for k, v := range a.Values {
		if err := setValue(filepath.Join(dst, "values.yml"), k, v); err != nil {
			return err
		}
	}
	return nil
}

// updateService brings every template file except values.example.yml to
// services/<name>, removes unit files the new template no longer ships, and
// keeps values.yml, adding the example's new keys (newValues). The
// contract's name line follows the service name. A file the owner changed
// on the box keeps that change (#511): the box's file, the template as it
// was copied in last (state.BaseDir) and the new template are merged three ways.
// Where that cannot be told or the two change the same lines, the template
// wins and the returned warning names the file.
func updateService(wt string, u *UpdateService) ([]string, error) {
	dst := filepath.Join(wt, "services", u.Name)
	if _, err := os.Stat(filepath.Join(dst, "laib.yml")); err != nil {
		return nil, fmt.Errorf("services/%s does not exist", u.Name)
	}
	entries, err := fs.ReadDir(u.Template, ".")
	if err != nil {
		return nil, err
	}
	var noBase, conflicts []string
	shipped := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "values.example.yml" || e.Name() == "README.md" {
			continue
		}
		b, err := fs.ReadFile(u.Template, e.Name())
		if err != nil {
			return nil, err
		}
		if e.Name() == "laib.yml" {
			lines := strings.Split(string(b), "\n")
			for i, l := range lines {
				if strings.HasPrefix(l, "name: ") {
					lines[i] = "name: " + u.Name
				}
			}
			b = []byte(strings.Join(lines, "\n"))
		}
		file := b
		cur, curErr := os.ReadFile(filepath.Join(dst, e.Name()))
		base, baseErr := os.ReadFile(filepath.Join(dst, state.BaseDir, e.Name()))
		switch {
		case curErr != nil || string(cur) == string(b) || baseErr == nil && string(cur) == string(base):
		case baseErr != nil: // a service from before #511: nothing says which lines were the owner's
			noBase = append(noBase, e.Name())
		default:
			if merged, ok := merge3(cur, base, b); ok {
				file = merged
			} else {
				conflicts = append(conflicts, e.Name())
			}
		}
		if err := writeBase(dst, e.Name(), b); err != nil {
			return nil, err
		}
		mode := fs.FileMode(0o644)
		if isHook(e.Name()) {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), file, mode); err != nil {
			return nil, err
		}
		shipped[e.Name()] = true
	}
	if ex, err := fs.ReadFile(u.Template, "values.example.yml"); err == nil {
		if err := newValues(filepath.Join(dst, "values.yml"), ex); err != nil {
			return nil, err
		}
	}
	old, _ := os.ReadDir(dst)
	for _, e := range old {
		if e.IsDir() || e.Name() == "values.yml" || e.Name() == "laib.yml" || shipped[e.Name()] {
			continue
		}
		// a unit the template dropped goes, a host unit too: the lease moved into a container (ADR 0027)
		if ext := filepath.Ext(e.Name()); ext == ".container" || ext == ".pod" || ext == ".volume" || ext == ".network" || ext == ".kube" || ext == ".service" || ext == ".timer" || e.Name() == "post-deploy" {
			os.Remove(filepath.Join(dst, e.Name()))
		}
	}
	bases, _ := os.ReadDir(filepath.Join(dst, state.BaseDir))
	for _, e := range bases {
		if !shipped[e.Name()] {
			os.Remove(filepath.Join(dst, state.BaseDir, e.Name()))
		}
	}
	var warn []string
	if len(noBase) > 0 {
		warn = append(warn, fmt.Sprintf("services/%s: %s weichen von der Vorlage ab, und Laib kennt den Stand der Vorlage von vorher nicht (vor #511). Die Vorlage gilt: prüfe im Diff, ob dabei eine Änderung des Besitzers verloren geht.", u.Name, strings.Join(noBase, ", ")))
	}
	if len(conflicts) > 0 {
		warn = append(warn, fmt.Sprintf("services/%s: in %s ändern die Box und die Vorlage dieselben Zeilen. Die Vorlage gilt: prüfe im Diff, ob dabei eine Änderung des Besitzers verloren geht.", u.Name, strings.Join(conflicts, ", ")))
	}
	return warn, nil
}

func writeBase(dst, name string, b []byte) error {
	if err := os.MkdirAll(filepath.Join(dst, state.BaseDir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, state.BaseDir, name), b, 0o644)
}

// merge3 merges base→theirs into cur, line by line (git merge-file); false
// when both changed the same lines.
func merge3(cur, base, theirs []byte) ([]byte, bool) {
	dir, err := os.MkdirTemp("", "laib-merge")
	if err != nil {
		return nil, false
	}
	defer os.RemoveAll(dir)
	args := []string{"merge-file", "-p"}
	for i, b := range [][]byte{cur, base, theirs} {
		args = append(args, filepath.Join(dir, []string{"box", "base", "template"}[i]))
		os.WriteFile(args[len(args)-1], b, 0o600)
	}
	out, err := exec.Command("git", args...).Output()
	return out, err == nil
}

// newValues appends to values.yml each key the template's example has and
// values.yml lacks, as the example's line writes it (#215): a template
// update never leaves a new placeholder without a value, and what the
// operator set stays as it is, byte for byte.
func newValues(path string, example []byte) error {
	have, _ := os.ReadFile(path)
	var cur, ex yaml.Node
	if err := yaml.Unmarshal(have, &cur); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := yaml.Unmarshal(example, &ex); err != nil {
		return fmt.Errorf("the template's values.example.yml: %w", err)
	}
	if len(ex.Content) == 0 {
		return nil
	}
	known := map[string]bool{}
	if len(cur.Content) > 0 {
		for i := 0; i < len(cur.Content[0].Content); i += 2 {
			known[cur.Content[0].Content[i].Value] = true
		}
	}
	lines, m := strings.Split(string(example), "\n"), ex.Content[0]
	var add []byte
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k, v := m.Content[i], m.Content[i+1]; !known[k.Value] {
			if v.Line != k.Line || v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
				return fmt.Errorf("the template's values.example.yml: %s is not one line", k.Value)
			}
			add = append(add, lines[k.Line-1]+"\n"...)
		}
	}
	if len(add) == 0 {
		return nil
	}
	if len(have) > 0 && have[len(have)-1] != '\n' {
		have = append(have, '\n')
	}
	return os.WriteFile(path, append(have, add...), 0o644)
}

// wireBox: a template that provides a platform role (proxy, sso, identity,
// dns, model) takes the role in box.yml when nobody holds it, and a
// hostNetwork template joins the carve-out list. Both are what a human
// would do by hand right after --add, and the order of the first
// installation (caddy, auth, adguard, whoami) needs them before validate.
func wireBox(wt, name string) error {
	var contract struct {
		Provides    []string `yaml:"provides"`
		HostNetwork bool     `yaml:"hostNetwork"`
	}
	var box struct {
		Roles       map[string]string `yaml:"roles"`
		HostNetwork []string          `yaml:"hostNetwork"`
	}
	path := filepath.Join(wt, "box.yml")
	cb, err := os.ReadFile(filepath.Join(wt, "services", name, "laib.yml"))
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(cb, &contract); err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, &box); err != nil {
		return err
	}
	for _, p := range contract.Provides {
		switch p {
		case "proxy", "sso", "identity", "dns", "model":
			if box.Roles[p] == "" && err == nil {
				err = setAt(path, "roles."+p, &yaml.Node{Kind: yaml.ScalarNode, Value: name})
			}
		}
	}
	if err == nil && contract.HostNetwork && !slices.Contains(box.HostNetwork, name) {
		hn := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, s := range append(box.HostNetwork, name) {
			hn.Content = append(hn.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: s})
		}
		err = setAt(path, "hostNetwork", hn)
	}
	return err
}

// isHook: the template hooks apply runs (ADR 0004 amendment, #36). Both
// phases are executable in the state repo — only post-deploy was, and a
// pre-start copied 0644 was skipped in silence (PR #70's first VM run).
func isHook(name string) bool { return name == "pre-start" || name == "post-deploy" }
