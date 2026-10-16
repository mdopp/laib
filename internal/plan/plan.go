// Package plan diffs rendered state against what is on the box.
package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
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
	Changes   []Change          `json:"changes"`
	Unmanaged []string          `json:"unmanaged"`
	Conflicts []string          `json:"conflicts,omitempty"` // unit names another generator source would also produce
	Services  []string          `json:"services"`            // services with at least one change
	Only      string            `json:"only,omitempty"`
	Output    render.Output     `json:"-"`
	Secrets   map[string]string `json:"-"` // podman secret → value (#224); a change "secret/<name>" hands it over
}

// Empty reports whether the box already matches the state.
func (p *Plan) Empty() bool { return len(p.Changes) == 0 }

// Target maps an output path to its location on the box.
func Target(paths boxfs.Paths, outPath string) string {
	if strings.HasPrefix(outPath, "quadlet/") {
		return filepath.Join(paths.Quadlet, strings.TrimPrefix(outPath, "quadlet/"))
	}
	if strings.HasPrefix(outPath, "systemd/") {
		return filepath.Join(paths.Systemd, strings.TrimPrefix(outPath, "systemd/"))
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
	p := &Plan{Output: out, Only: only}
	// add records a change and, once, the service it belongs to
	seen := map[string]bool{}
	add := func(c Change) {
		p.Changes = append(p.Changes, c)
		if c.Service != "" && !seen[c.Service] {
			seen[c.Service] = true
			p.Services = append(p.Services, c.Service)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(out)) {
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
		add(Change{
			Path: k, Target: target, Service: svc, Kind: kind,
			Diff: unified(ctx, k, render.Redact(render.Output{"a": string(cur)}, secrets)["a"], render.Redact(render.Output{"b": out[k]}, secrets)["b"]),
		})
	}
	// Files the manifest owns that render no longer produces: delete (#41).
	owned := map[string]bool{}
	current := map[string]bool{}
	for k := range out {
		current[Target(paths, k)] = true
	}
	for _, target := range ReadManifest(paths) {
		owned[target] = true
		if current[target] {
			continue
		}
		if _, err := os.Stat(target); err != nil {
			continue
		}
		k := outPathOf(paths, target)
		if k == "" {
			continue
		}
		svc := ServiceOf(k)
		if only != "" && svc != "" && svc != only {
			continue
		}
		add(Change{Path: k, Target: target, Service: svc, Kind: "delete"})
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
				add(Change{Path: k, Target: Target(paths, k), Service: name, Kind: "delete"})
			}
		}
	}
	// Podman secrets (#224) differ like files: missing or another value on
	// the box is a change of its service (which restarts it), one of laib's
	// that nothing names any more goes. Without the values (lint) there is
	// nothing to compare.
	if secrets != nil {
		box, err := boxSecrets(ctx)
		if err != nil {
			return nil, err
		}
		p.Secrets = render.PodmanSecrets(st, secrets)
		names := maps.Clone(p.Secrets)
		maps.Copy(names, box)
		for _, n := range slices.Sorted(maps.Keys(names)) {
			cur, onBox := box[n]
			want, wanted := p.Secrets[n]
			svc := strings.SplitN(n, ".", 3)[1]
			if (onBox && wanted && cur == want) || (only != "" && svc != only) {
				continue
			}
			kind := map[bool]string{true: "change", false: "add"}[onBox]
			if !wanted {
				kind = "delete"
			}
			add(Change{Path: "secret/" + n, Target: "podman secret " + n, Service: svc, Kind: kind, Diff: "(a secret value, not shown)"})
		}
	}
	p.Unmanaged = unmanaged(st, paths)
	// a folder whose files the manifest owns is laib's, not unmanaged
	var um []string
	for _, name := range p.Unmanaged {
		entries, _ := os.ReadDir(filepath.Join(paths.Quadlet, name))
		mine := len(entries) > 0 && !slices.ContainsFunc(entries, func(e os.DirEntry) bool { return !owned[filepath.Join(paths.Quadlet, name, e.Name())] })
		if !mine {
			um = append(um, name)
		}
	}
	p.Unmanaged = um
	p.Conflicts = unitConflicts(st, paths)
	return p, nil
}

// boxSecrets reads laib's podman secrets on the box, name → value, from
// podman's output (never an argv).
func boxSecrets(ctx context.Context) (map[string]string, error) {
	ls, err := sysd.Run(ctx, "podman", "secret", "ls", "--format", "{{.Name}}")
	if err != nil {
		return nil, fmt.Errorf("podman secret ls: %s", ls)
	}
	// a warning on stderr is a line too: only laib's names count
	names := slices.DeleteFunc(strings.Split(ls, "\n"), func(n string) bool { return !strings.HasPrefix(n, "laib.") })
	if len(names) == 0 {
		return nil, nil
	}
	js, err := sysd.Run(ctx, "podman", append([]string{"secret", "inspect", "--showsecret", "--format", "{{.Spec.Name}} {{json .SecretData}}"}, names...)...)
	if err != nil {
		return nil, fmt.Errorf("podman secret inspect: %s", js)
	}
	out := map[string]string{}
	for _, line := range strings.Split(js, "\n") {
		var v string
		if n, q, _ := strings.Cut(line, " "); slices.Contains(names, n) && json.Unmarshal([]byte(q), &v) == nil {
			out[n] = v
		}
	}
	return out, nil
}

// unitConflicts finds Quadlet files in the directory root (ServiceBay's
// <name>.kube, a hand-rolled <name>.container) that would generate the same
// unit name as one of this state's services. Quadlet derives the unit name
// from the file name, not the folder, so both sources cannot coexist: the
// old file must go before apply (docs/MIGRATION.md, one owner per service).
func unitConflicts(st *state.State, paths boxfs.Paths) []string {
	entries, _ := os.ReadDir(paths.Quadlet)
	rootUnits := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		switch filepath.Ext(e.Name()) {
		case ".container", ".kube":
			rootUnits[base+".service"] = e.Name()
		case ".pod":
			rootUnits[base+"-pod.service"] = e.Name()
		}
	}
	var out []string
	for _, name := range st.ServiceNames() {
		for _, u := range st.Services[name].UnitNames() {
			if file, ok := rootUnits[u]; ok {
				out = append(out, fmt.Sprintf("%s: %s would also be generated from %s", name, u, file))
			}
		}
	}
	return out
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
		if !e.IsDir() {
			base := strings.TrimSuffix(name, filepath.Ext(name))
			if _, ok := st.Services[base]; ok {
				continue // reported as a conflict instead
			}
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

// The manifest (#41): every file apply rendered onto the box. What it has
// but render no longer produces is deleted and, for a unit, stopped and
// disabled — a host unit of a disabled operator, the lease unit of a
// removed llama, the quadlet folder of a service a rollback took out.
func manifestPath(paths boxfs.Paths) string { return filepath.Join(paths.Home, "managed.json") }

func WriteManifest(paths boxfs.Paths, targets []string) error {
	sorted := append([]string{}, targets...)
	sort.Strings(sorted)
	b, _ := json.MarshalIndent(sorted, "", "  ")
	return os.WriteFile(manifestPath(paths), b, 0o644)
}

func ReadManifest(paths boxfs.Paths) []string {
	b, err := os.ReadFile(manifestPath(paths))
	if err != nil {
		return nil
	}
	var out []string
	json.Unmarshal(b, &out)
	return out
}

// outPathOf maps a target on the box back to its render.Output path.
func outPathOf(paths boxfs.Paths, target string) string {
	for prefix, dir := range map[string]string{"quadlet/": paths.Quadlet, "systemd/": paths.Systemd} {
		if dir != "" && strings.HasPrefix(target, dir+"/") {
			return prefix + strings.TrimPrefix(target, dir+"/")
		}
	}
	if paths.Render != "" && strings.HasPrefix(target, paths.Render+"/") {
		return strings.TrimPrefix(target, paths.Render+"/")
	}
	return ""
}
