package proposal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mdopp/laib/internal/state"
)

// Edit is one change an agent asks for without file access (issue #11).
type Edit struct {
	Patch  string            // unified diff against the repo root
	Set    map[string]string // "services/x/values.yml:KEY" → value (or "!secret NAME")
	Add    *AddService
	Update *UpdateService // replace a service's template files, keep values.yml (#14)
}

// UpdateService refreshes services/<name> from a newer template folder.
type UpdateService struct {
	Name     string
	Template fs.FS
}

// AddService copies a template folder into services/<name>.
type AddService struct {
	Name     string
	Template fs.FS // the template folder as a file system
	Values   map[string]string
}

// CreateFromEdit applies the edit in a temporary worktree on a new branch,
// validates the result with the state loader, commits and records the inbox
// entry. The main checkout is never touched, so a bad patch leaves nothing
// behind. Validate is injected so this package stays free of render.
func CreateFromEdit(ctx context.Context, repo, inbox, actor, reason string, e Edit, validate func(dir string) error) (*Proposal, error) {
	if err := EnsureRepo(ctx, repo); err != nil {
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
	for key, val := range e.Set {
		file, k, ok := strings.Cut(key, ":")
		if !ok {
			return fail(fmt.Errorf("--set wants path:KEY=VALUE, got %q", key))
		}
		if err := setValue(filepath.Join(wt, file), k, val); err != nil {
			return fail(err)
		}
	}
	if e.Add != nil {
		if err := addService(wt, e.Add); err != nil {
			return fail(err)
		}
	}
	if e.Update != nil {
		if err := updateService(wt, e.Update); err != nil {
			return fail(err)
		}
	}
	if _, err := state.Load(wt); err != nil {
		return fail(fmt.Errorf("the proposed state does not load:\n%w", err))
	}
	if validate != nil {
		if err := validate(wt); err != nil {
			return fail(fmt.Errorf("the proposed state does not render: %w", err))
		}
	}
	if _, err := git(ctx, wt, "add", "-A"); err != nil {
		return fail(err)
	}
	status, _ := git(ctx, wt, "status", "--porcelain")
	if strings.TrimSpace(status) == "" {
		return fail(errors.New("nothing to propose: the edit changes nothing"))
	}
	if _, err := git(ctx, wt, "commit", "-q", "-m", "proposal: "+reason, "--author", actor+" <"+actor+"@laib>"); err != nil {
		return fail(err)
	}
	p := &Proposal{ID: id, Actor: actor, Reason: reason, Branch: branch, Tier: "apply", Status: "open", Created: now()}
	for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			p.Files = append(p.Files, f[len(f)-1])
		}
	}
	git(ctx, repo, "worktree", "remove", "--force", wt)
	return p, save(inbox, p)
}

// setValue edits one key of a values.yml, keeping comments and the !secret
// tag of other entries. A value "!secret NAME" becomes a tagged node.
func setValue(path, key, val string) error {
	var doc yaml.Node
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return fmt.Errorf("%s is not a map", path)
	}
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: val, Tag: "!!str"}
	if strings.HasPrefix(val, "!secret ") {
		node = &yaml.Node{Kind: yaml.ScalarNode, Value: strings.TrimPrefix(val, "!secret "), Tag: "!secret"}
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = node
			return writeYAML(path, &doc)
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"}, node)
	return writeYAML(path, &doc)
}

func writeYAML(path string, doc *yaml.Node) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(doc)
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
		if name == "post-deploy" {
			mode = 0o755
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

// updateService copies every template file except values.example.yml over
// services/<name>, removes unit files the new template no longer ships, and
// keeps values.yml. The contract's name line follows the service name.
func updateService(wt string, u *UpdateService) error {
	dst := filepath.Join(wt, "services", u.Name)
	if _, err := os.Stat(filepath.Join(dst, "laib.yml")); err != nil {
		return fmt.Errorf("services/%s does not exist", u.Name)
	}
	entries, err := fs.ReadDir(u.Template, ".")
	if err != nil {
		return err
	}
	shipped := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "values.example.yml" || e.Name() == "README.md" {
			continue
		}
		b, err := fs.ReadFile(u.Template, e.Name())
		if err != nil {
			return err
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
		mode := fs.FileMode(0o644)
		if e.Name() == "post-deploy" {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, mode); err != nil {
			return err
		}
		shipped[e.Name()] = true
	}
	old, _ := os.ReadDir(dst)
	for _, e := range old {
		if e.IsDir() || e.Name() == "values.yml" || e.Name() == "laib.yml" || shipped[e.Name()] {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext == ".container" || ext == ".pod" || ext == ".volume" || ext == ".network" || ext == ".kube" || e.Name() == "post-deploy" {
			os.Remove(filepath.Join(dst, e.Name()))
		}
	}
	return nil
}
