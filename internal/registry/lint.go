// The template lint (`laib template lint`, ADR 0004): a template folder
// loaded as the only service of a throw-away state, so the same validation
// applies, plus what only a template can get wrong — mounts outside its
// data, hook interpreters outside the hooks image, a missing health, a host
// port a sibling template publishes. Units and hooks come only from
// templates (ADR 0019 §2a).

package registry

import (
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

// LintTemplate loads a template folder as if it were the only service of a
// throw-away state, so the same validation applies (ADR 0004).
func LintTemplate(env *cli.Env, dir string) int {
	tmp, err := os.MkdirTemp("", "laib-lint")
	if err != nil {
		return env.Fail(err)
	}
	defer os.RemoveAll(tmp)
	name := filepath.Base(filepath.Clean(dir))
	os.WriteFile(filepath.Join(tmp, "box.yml"), []byte("domain: lint.example\ndataDir: /mnt/data/stacks\nlanIp: 10.0.0.2\ngpu: {present: true}\nhostNetwork: ["+name+"]\nroles: {sso: "+name+"}\nlaib: {listen: 0.0.0.0}\n"), 0o644)
	os.WriteFile(filepath.Join(tmp, "policy.yml"), []byte("tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n"), 0o644)
	dst := filepath.Join(tmp, "services", name)
	os.MkdirAll(dst, 0o755)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return env.Fail(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644)
	}
	if _, err := os.Stat(filepath.Join(dst, "values.yml")); err != nil {
		// A template ships values.example.yml; lint with it.
		if b, err := os.ReadFile(filepath.Join(dir, "values.example.yml")); err == nil {
			os.WriteFile(filepath.Join(dst, "values.yml"), b, 0o644)
		}
	}
	// after: names services the template expects to exist; stub them so the
	// reference itself is checked, not the neighbour's presence.
	if b, err := os.ReadFile(filepath.Join(dir, "laib.yml")); err == nil {
		var c struct {
			After []string `yaml:"after"`
		}
		if yaml.Unmarshal(b, &c) == nil {
			for _, dep := range c.After {
				if dep == name {
					continue
				}
				stub := filepath.Join(tmp, "services", dep)
				os.MkdirAll(stub, 0o755)
				os.WriteFile(filepath.Join(stub, "laib.yml"), []byte("name: "+dep+"\n"), 0o644)
				os.WriteFile(filepath.Join(stub, dep+".container"), []byte("[Container]\nImage=stub\n"), 0o644)
			}
		}
	}
	st, err := state.Load(tmp)
	if err != nil {
		fmt.Fprintln(env.Errw(), err)
		return cli.ExitUsage
	}
	out, err := render.Render(st, nil)
	if err != nil {
		return env.Fail(err)
	}
	// the after: neighbours' declared data paths (their apply creates them)
	// may be mounted too: the file share's folders in media, immich, paperless
	var neighbours []string
	for dep := range st.Services {
		if dep == name {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Clean(dir)), dep, "laib.yml")); err == nil {
			var c struct {
				Data []state.DataPath `yaml:"data"`
			}
			if yaml.Unmarshal(b, &c) == nil {
				for _, d := range c.Data {
					neighbours = append(neighbours, d.Path)
				}
			}
		}
	}
	if errs := slices.Concat(lintMounts(st, name, out, neighbours), lintEnvironment(name, out), lintHostWrites(name, out), lintPodNetwork(name, out)); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(env.Errw(), e)
		}
		return cli.ExitUsage
	}
	for _, phase := range []string{"pre-start", "post-deploy"} {
		if b, err := os.ReadFile(filepath.Join(dir, phase)); err == nil {
			first := strings.SplitN(string(b), "\n", 2)[0]
			if !hookInterpreters[first] {
				fmt.Fprintf(env.Errw(), "%s/%s: interpreter %q is not in the hooks image (use one of: %s)\n", dir, phase, first, strings.Join(slices.Sorted(maps.Keys(hookInterpreters)), ", "))
				return cli.ExitUsage
			}
		}
	}
	// a health declaration (#46): an http probe, or a reason why there is none
	if h := st.Services[name].Contract.Health; h.HTTP == "" && h.None == "" {
		fmt.Fprintf(env.Errw(), "%s: laib.yml declares no health (health: {http: …} or health: {none: \"why\"})\n", dir)
		return cli.ExitUsage
	}
	// a host port another template of the same registry publishes (#46: whoami and bote on 8080)
	mine := map[string]bool{}
	for _, content := range st.Services[name].Units {
		for _, hp := range state.PublishedHostPorts(content) {
			mine[hp] = true
		}
	}
	siblings, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Clean(dir)), "*", "*.container"))
	pods, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Clean(dir)), "*", "*.pod"))
	for _, f := range append(siblings, pods...) {
		other := filepath.Base(filepath.Dir(f))
		if other == name {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, hp := range state.PublishedHostPorts(string(b)) {
			if mine[hp] {
				fmt.Fprintf(env.Errw(), "%s: publishes host port %s like template %s — two services on one port never both start\n", dir, hp, other)
				return cli.ExitUsage
			}
		}
	}
	fmt.Printf("ok: %s (%d unit files, %d routes, %d secrets)\n", name, len(st.Services[name].Units), len(st.Services[name].Contract.Routes), len(st.Services[name].Contract.Secrets))
	return cli.ExitOK
}

// lintEnvironment: systemd splits an unquoted Environment= value at
// whitespace, so "Hey Joshua" reaches the container as "Hey" (#571, #627).
// Lint renders with the built-in persona and values.example.yml, so the
// rendered line shows a value with a space; quoted, the whole value arrives.
func lintEnvironment(name string, out render.Output) []string {
	var errs []string
	for key, text := range out {
		if !strings.HasPrefix(key, "quadlet/"+name+"/") {
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			v, ok := strings.CutPrefix(strings.TrimSpace(line), "Environment=")
			if ok && !strings.HasPrefix(v, `"`) && !strings.HasPrefix(v, "'") && strings.ContainsAny(v, " \t") {
				errs = append(errs, fmt.Sprintf("%s: %s: systemd splits an unquoted value at the space; quote it: Environment=\"%s\"", key, line, v))
			}
		}
	}
	return errs
}

// hookInterpreters is what ghcr.io/mdopp/laib-hooks provides (#36).
var hookInterpreters = map[string]bool{"#!/usr/bin/env python3": true, "#!/usr/bin/env bash": true, "#!/bin/bash": true, "#!/bin/sh": true, "#!/usr/bin/env sh": true}

// lintMounts: every Volume= source of the rendered units must be a declared
// data path (or under one), a rendered file, a named volume or a tmpfs —
// apply creates only declared paths, and podman refuses a missing bind
// source (#36; the failure afc1115 fixed for declared paths).
func lintMounts(st *state.State, name string, out render.Output, neighbours []string) []string {
	allowed := state.DataPaths(st, st.Services[name])
	for _, p := range neighbours {
		allowed = append(allowed, strings.ReplaceAll(p, "${DATA_DIR}", st.Box.DataDir))
	}
	var errs []string
	for key, text := range out {
		if !strings.HasPrefix(key, "quadlet/"+name+"/") {
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "Volume=") {
				continue
			}
			src, _, _ := strings.Cut(strings.TrimPrefix(line, "Volume="), ":")
			if strings.HasPrefix(src, "%h/") || strings.HasPrefix(src, "~/") { // #687: llama-lease, open /presets.json: permission denied
				errs = append(errs, fmt.Sprintf("%s: %s mounts %s from laib's home: under SELinux a container may not read laib's config folder; copy the file into a declared data path in pre-start and mount that", key, line, src))
				continue
			}
			if !strings.HasPrefix(src, "/") {
				continue // a named volume
			}
			// rendered files, and what the host itself provides (devices, dbus, sysfs)
			ok := strings.HasPrefix(src, "/mnt/data/laib/render/") || strings.HasPrefix(src, "/dev/") || strings.HasPrefix(src, "/run/") || strings.HasPrefix(src, "/sys/") || strings.HasPrefix(src, "/proc/")
			for _, a := range allowed {
				if src == a || strings.HasPrefix(src, strings.TrimSuffix(a, "/")+"/") {
					ok = true
				}
			}
			if !ok {
				errs = append(errs, fmt.Sprintf("%s: %s mounts %s, which is not a declared data path (apply creates only declared paths; podman refuses a missing bind source)", key, line, src))
			}
			// the shared tree is never relabelled (#230): :z walks it on every start and fails on an entry core does not own
			share, opts := st.Box.DataDir+"/file-share/data", strings.Split(line, ":")
			relabel := len(opts) > 2 && slices.ContainsFunc(strings.Split(opts[2], ","), func(o string) bool { return o == "z" || o == "Z" })
			if (src == share || strings.HasPrefix(src, share+"/")) && (relabel || !strings.Contains(text, "\nSecurityLabelDisable=true")) {
				errs = append(errs, fmt.Sprintf("%s: %s relabels the shared file-share tree: mount it without :z/:Z and set SecurityLabelDisable=true", key, line))
			}
		}
	}
	return errs
}

// writers: the commands that create or change a file at a path they name.
var writers = map[string]bool{"touch": true, "mkdir": true, "cp": true, "mv": true, "install": true, "tee": true, "chmod": true, "chown": true, "rm": true, "ln": true, "truncate": true}

// lintHostWrites (#687): a host-side command of a unit (Exec* but Exec=)
// that writes into a folder a container of the template mounts writable.
// What the container writes there belongs to its user — a subuid unless
// it runs as root — and the host's user is refused (stimme-whisper: touch:
// Permission denied). `podman unshare <command>` writes in podman's user
// namespace, where it works whoever owns the folder; a read, a podman
// command and a write anywhere else are not this shape.
func lintHostWrites(name string, out render.Output) []string {
	var mounted []string
	for key, text := range out {
		for _, line := range strings.Split(text, "\n") {
			v, ok := strings.CutPrefix(strings.TrimSpace(line), "Volume=")
			parts := strings.Split(v, ":")
			if ok && strings.HasPrefix(key, "quadlet/"+name+"/") && strings.HasPrefix(v, "/") && !(len(parts) > 2 && slices.Contains(strings.Split(parts[2], ","), "ro")) {
				mounted = append(mounted, strings.TrimSuffix(parts[0], "/"))
			}
		}
	}
	into := func(f string) bool {
		return slices.ContainsFunc(mounted, func(m string) bool { return f == m || strings.HasPrefix(f, m+"/") })
	}
	var errs []string
	for key, text := range out {
		if !strings.HasPrefix(key, "quadlet/"+name+"/") {
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
			if !commandKey[k] {
				continue
			}
			// the shell's words, segment by segment: a quote is no boundary, a separator is
			v = strings.NewReplacer("'", " ", `"`, " ", ">", " > ", ";", "\n", "&&", "\n", "||", "\n", "|", "\n", "$(", "\n", "(", "\n", ")", "\n", "{", "\n", "}", "\n").Replace(v)
			for _, seg := range strings.Split(v, "\n") {
				f := strings.Fields(seg)
				for len(f) > 0 && strings.Contains(f[0], "=") {
					f = f[1:] // VAR=value before the command
				}
				if len(f) == 0 || filepath.Base(strings.TrimLeft(f[0], "-@+!:")) == "podman" {
					continue
				}
				for i, w := range f {
					if into(w) && (writers[filepath.Base(strings.TrimLeft(f[0], "-@+!:"))] && i > 0 || i > 0 && f[i-1] == ">") {
						errs = append(errs, fmt.Sprintf("%s: %s writes %s on the host, in a folder a container of %s mounts: the container's user may own it and the host's user is refused; run it as podman unshare %s", key, line, w, name, strings.Join(f, " ")))
					}
				}
			}
		}
	}
	return errs
}

var commandKey = map[string]bool{"ExecStart": true, "ExecStartPre": true, "ExecStartPost": true, "ExecStop": true, "ExecStopPost": true, "ExecReload": true, "ExecCondition": true}

// lintPodNetwork: a container of a pod has no network of its own, so
// `podman run --network container:<it>` fails with exit 125 („is part of a
// pod, but container is not“) — the #785 checks of bote, frage and immich
// went red on the box. Such a one-off joins the pod: --pod <pod>.
func lintPodNetwork(name string, out render.Output) []string {
	inPod := map[string]bool{}
	for key, text := range out {
		base, ok := strings.CutSuffix(path.Base(key), ".container")
		if !ok || !strings.HasPrefix(key, "quadlet/"+name+"/") || !strings.Contains(text, "\nPod=") {
			continue
		}
		inPod["systemd-"+base] = true
		for _, line := range strings.Split(text, "\n") {
			if n, ok := strings.CutPrefix(strings.TrimSpace(line), "ContainerName="); ok {
				inPod[n] = true
			}
		}
	}
	var errs []string
	for key, text := range out {
		for _, line := range strings.Split(text, "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
			if !commandKey[k] {
				continue
			}
			for _, f := range strings.Fields(v) {
				if c, ok := strings.CutPrefix(f, "container:"); ok && inPod[c] {
					errs = append(errs, fmt.Sprintf("%s: %s joins the network of %s, a container in a pod: podman refuses it (exit 125); use --pod with the pod's name", key, k, c))
				}
			}
		}
	}
	return errs
}
