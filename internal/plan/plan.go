// Package plan diffs rendered state against what is on the box.
package plan

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

// Change is one file that differs.
type Change struct {
	Path    string `json:"path"`    // render.Output path
	Target  string `json:"target"`  // absolute path on the box
	Service string `json:"service"` // "" for role outputs
	Kind    string `json:"kind"`    // add | change | delete
	Diff    string `json:"diff"`    // unified diff, secrets redacted
}

// Plan is the result of `laib plan`.
type Plan struct {
	Changes   []Change      `json:"changes"`
	Unmanaged []string      `json:"unmanaged"`
	Services  []string      `json:"services"` // services with at least one change
	Output    render.Output `json:"-"`
}

// Empty reports whether the box already matches the state.
func (p *Plan) Empty() bool { return len(p.Changes) == 0 }

// Target maps an output path to its location on the box.
func Target(paths boxfs.Paths, outPath string) string {
	if strings.HasPrefix(outPath, "quadlet/") {
		return filepath.Join(paths.Quadlet, strings.TrimPrefix(outPath, "quadlet/"))
	}
	return filepath.Join(paths.Render, outPath)
}

// ServiceOf returns the service an output path belongs to, or "".
func ServiceOf(outPath string) string {
	if strings.HasPrefix(outPath, "quadlet/") {
		return strings.SplitN(strings.TrimPrefix(outPath, "quadlet/"), "/", 2)[0]
	}
	return ""
}

// Compute renders (with real secrets, so the box comparison is exact) and
// diffs against the box. The diffs it returns are redacted. `only` limits the
// changes to one service and the role outputs.
func Compute(ctx context.Context, st *state.State, secrets map[string]string, paths boxfs.Paths, only string) (*Plan, error) {
	out, err := render.Render(st, secrets)
	if err != nil {
		return nil, err
	}
	p := &Plan{Output: out}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	for _, k := range keys {
		svc := ServiceOf(k)
		if only != "" && svc != "" && svc != only {
			continue
		}
		target := Target(paths, k)
		cur, err := os.ReadFile(target)
		kind := "change"
		if errors.Is(err, os.ErrNotExist) {
			kind = "add"
			cur = nil
		} else if err != nil {
			return nil, err
		}
		if string(cur) == out[k] {
			continue
		}
		p.Changes = append(p.Changes, Change{
			Path: k, Target: target, Service: svc, Kind: kind,
			Diff: unified(ctx, k, render.Redact(render.Output{"a": string(cur)}, secrets)["a"], render.Redact(render.Output{"b": out[k]}, secrets)["b"]),
		})
		if svc != "" && !seen[svc] {
			seen[svc] = true
			p.Services = append(p.Services, svc)
		}
	}
	// Quadlet files on the box that the state no longer has: delete.
	for _, name := range st.ServiceNames() {
		if only != "" && name != only {
			continue
		}
		entries, _ := os.ReadDir(filepath.Join(paths.Quadlet, name))
		for _, e := range entries {
			k := "quadlet/" + name + "/" + e.Name()
			if _, ok := out[k]; !ok && !e.IsDir() {
				p.Changes = append(p.Changes, Change{Path: k, Target: Target(paths, k), Service: name, Kind: "delete"})
				if !seen[name] {
					seen[name] = true
					p.Services = append(p.Services, name)
				}
			}
		}
	}
	p.Unmanaged = unmanaged(st, paths)
	return p, nil
}

// unmanaged lists Quadlet entries on the box that no service and no
// ignore.yml line claims (ADR 0008).
func unmanaged(st *state.State, paths boxfs.Paths) []string {
	entries, _ := os.ReadDir(paths.Quadlet)
	ignored := map[string]bool{}
	for _, u := range st.Ignore {
		ignored[u] = true
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if _, ok := st.Services[name]; ok || ignored[name] {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// unified shells out to diff -u (present on Fedora CoreOS); without it the
// change is still reported, just without a hunk.
func unified(ctx context.Context, label, a, b string) string {
	if _, err := exec.LookPath("diff"); err != nil {
		return "(diff unavailable)"
	}
	dir, err := os.MkdirTemp("", "laib-diff")
	if err != nil {
		return "(diff unavailable)"
	}
	defer os.RemoveAll(dir)
	pa, pb := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.WriteFile(pa, []byte(a), 0o600)
	os.WriteFile(pb, []byte(b), 0o600)
	outb, _ := exec.CommandContext(ctx, "diff", "-u", "--label", "box/"+label, "--label", "state/"+label, pa, pb).Output()
	return string(outb)
}
