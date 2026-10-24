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
	"regexp"
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
	Conflicts []string          `json:"conflicts,omitempty"` // foreign units that run one of the services too (#227)
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
// changes to one service and the box's own outputs.
// outside: a change of svc that `laib apply <only>` leaves for later. Role
// outputs and Laib's own programs (ADR 0027) belong to the box, not to a
// service: their units come with the timers that start them, or a scoped
// apply enables laib-notify.timer without the container it triggers (#633).
func outside(only, svc string) bool {
	return only != "" && svc != "" && svc != only && !slices.Contains(render.Own, svc)
}

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
		if outside(only, svc) {
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
		if _, err := os.Stat(filepath.Dir(target)); err == nil && kind == "add" && (strings.HasSuffix(k, "/"+render.ValuesFile) || strings.HasSuffix(k, "/"+render.HooksFile)) {
			svc = "" // a service from before #364 (#496): the file is written, the service does not change for it
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
		if outside(only, svc) {
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
	// that nothing names any more goes. Without the values (lint, or a
	// secrets.age that is gone: then podman holds their last copy) there is
	// nothing to compare.
	if len(secrets) > 0 {
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
			if (onBox && wanted && cur == want) || outside(only, svc) {
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
	p.Conflicts = Conflicts(st, paths)
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
		// what it found before it failed is values: only its error lines go on
		why := slices.DeleteFunc(strings.Split(js, "\n"), func(l string) bool { return !strings.HasPrefix(l, "Error") })
		return nil, fmt.Errorf("podman secret inspect: %v: %s", err, strings.Join(why, "; "))
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

// A foreign unit's Pod YAML, host ports (PublishPort=[ip:]host:ctr, a Pod
// YAML's hostPort, a host-network pod's containerPort) and host paths
// (Volume=src:…, Mount=…source=, a hostPath's path).
var (
	yamlRe     = regexp.MustCompile(`(?m)^Yaml=(.+)$`)
	hostPortRe = regexp.MustCompile(`(?m)(?:^PublishPort=(?:\[[0-9a-fA-F:.]*\]:|[0-9.]+:)?(\d+):|hostPort:\s*["']?(\d+))`)
	hostNetRe  = regexp.MustCompile(`(?m)^(?:\s*hostNetwork:\s*true|Network=host)`)
	ctrPortRe  = regexp.MustCompile(`containerPort:\s*["']?(\d+)()`)
	hostPathRe = regexp.MustCompile(`(?m)(?:^Volume=|^Mount=.*\b(?:source|src)=|hostPath:\s*\n\s*(?:type:.*\n\s*)?path:\s*["']?)(/[^\s:,"'#]*)`)
)

// realPath resolves a symlinked prefix (FCOS's /mnt is /var/mnt) up to the
// part that exists; inside reports whether a is b or below it.
func realPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if dir := filepath.Dir(p); dir != p {
		return filepath.Join(realPath(dir), filepath.Base(p))
	}
	return p
}

func inside(a, b string) bool { return a == b || strings.HasPrefix(a, strings.TrimSuffix(b, "/")+"/") }

// unitConflicts finds Quadlet files in the directory root (ServiceBay's
// <name>.kube with its Yaml=, a hand-rolled <name>.container) that would run
// beside one of this state's services (#227): the same base name (auth.kube
// is Laib's auth whatever units Laib generates for it), the same unit name, a
// host port the service publishes, or a host path at, inside or above one of
// its backed-up data paths. What lies at or below a backup:false path is not
// the service's own (the shared tree file-share/data with its folders, which
// Immich, Jellyfin and Solaris read, ADR 0017; caches). Two control planes on
// one service cannot coexist: the old unit is stopped and its file moved out
// before apply (ADR 0010, one owner per service).
func Conflicts(st *state.State, paths boxfs.Paths) []string {
	entries, _ := os.ReadDir(paths.Quadlet)
	var out []string
	for _, e := range entries {
		file, ext := e.Name(), filepath.Ext(e.Name())
		base := strings.TrimSuffix(file, ext)
		unit := map[string]string{".container": base + ".service", ".kube": base + ".service", ".pod": base + "-pod.service"}[ext]
		if e.IsDir() || unit == "" {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(paths.Quadlet, file))
		text := string(b)
		if m := yamlRe.FindStringSubmatch(text); m != nil {
			y := strings.TrimSpace(m[1])
			if !filepath.IsAbs(y) {
				y = filepath.Join(paths.Quadlet, y)
			}
			b, _ = os.ReadFile(y)
			text += string(b)
		}
		ports := hostPortRe.FindAllStringSubmatch(text, -1)
		if hostNetRe.MatchString(text) {
			ports = append(ports, ctrPortRe.FindAllStringSubmatch(text, -1)...)
		}
		for _, name := range st.ServiceNames() {
			c := st.Services[name].Contract
			var why []string
			if base == name || slices.Contains(st.Services[name].UnitNames(), unit) {
				why = append(why, unit)
			}
			for _, m := range ports {
				if slices.ContainsFunc(c.Ports, func(p state.Port) bool { return fmt.Sprint(p.Port) == m[1]+m[2] }) {
					why = append(why, "host port "+m[1]+m[2])
				}
			}
			for _, m := range hostPathRe.FindAllStringSubmatch(text, -1) {
				host := realPath(m[1])
				own, shared := false, false
				for _, d := range c.Data {
					dp := realPath(strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir))
					if d.Backup != nil && !*d.Backup {
						shared = shared || inside(host, dp)
					} else {
						own = own || inside(host, dp) || inside(dp, host)
					}
				}
				if own && !shared {
					why = append(why, "data "+m[1])
				}
			}
			if why != nil {
				out = append(out, fmt.Sprintf("%s: %s runs it too (%s)", name, file, strings.Join(why, ", ")))
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
