package state

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// unitExts are the Quadlet file kinds Laib copies verbatim after substitution.
var unitExts = map[string]bool{".container": true, ".pod": true, ".volume": true, ".network": true, ".kube": true, ".service": true, ".timer": true}

// assetExts are files copied next to the units without substitution
// (a presets.json the sidecar reads, a config a hook renders from).
var assetExts = map[string]bool{".json": true, ".ini": true, ".toml": true, ".conf": true, ".py": true} // .py: a small server a stock image runs (bote-notify)

// Load reads and validates a state repo. All validation errors are joined so
// an agent sees every problem at once.
func Load(dir string) (*State, error) {
	st := &State{Dir: dir, Services: map[string]*Service{}}
	var errs []error

	if err := readYAML(filepath.Join(dir, "box.yml"), &st.Box); err != nil {
		errs = append(errs, err)
	}
	if err := readYAML(filepath.Join(dir, "policy.yml"), &st.Policy); err != nil {
		errs = append(errs, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "ignore.yml")); err == nil {
		var ig struct {
			Units []string `yaml:"units"`
		}
		if err := yaml.Unmarshal(b, &ig); err != nil {
			errs = append(errs, fmt.Errorf("ignore.yml: %w", err))
		}
		st.Ignore = ig.Units
	}

	entries, _ := os.ReadDir(filepath.Join(dir, "services"))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		svc, err := loadService(filepath.Join(dir, "services", e.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		st.Services[svc.Name] = svc
	}

	errs = append(errs, st.loadPersonas()...)
	errs = append(errs, st.validatePeople()...)
	errs = append(errs, st.validate()...)
	st.Warnings = append(st.purposeWarnings(), st.personaWarnings()...)
	if len(errs) > 0 {
		return st, errors.Join(errs...)
	}
	return st, nil
}

func readYAML(path string, into any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, into); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

func loadService(dir string) (*Service, error) {
	svc := &Service{Name: filepath.Base(dir), Dir: dir, Values: map[string]Value{}, Units: map[string]string{}, Assets: map[string]string{}}
	if err := readYAML(filepath.Join(dir, "laib.yml"), &svc.Contract); err != nil {
		return nil, fmt.Errorf("services/%s: %w", svc.Name, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "values.yml")); err == nil {
		var doc yaml.Node
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("services/%s/values.yml: %w", svc.Name, err)
		}
		if len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
			m := doc.Content[0]
			for i := 0; i+1 < len(m.Content); i += 2 {
				k, v := m.Content[i], m.Content[i+1]
				if v.Tag == "!secret" {
					svc.Values[k.Value] = Value{Secret: v.Value}
				} else {
					svc.Values[k.Value] = Value{Literal: v.Value}
				}
			}
		}
	}
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		ext := filepath.Ext(f.Name())
		if f.IsDir() || !(unitExts[ext] || assetExts[ext]) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, err
		}
		if assetExts[ext] {
			svc.Assets[f.Name()] = string(b)
			continue
		}
		svc.Units[f.Name()] = string(b)
	}
	for _, file := range slices.Sorted(maps.Keys(svc.Contract.When)) {
		if _, ok := svc.Units[file]; !ok {
			return nil, fmt.Errorf("services/%s/laib.yml: when names %s, which is not a unit of the service", svc.Name, file)
		}
		if !slices.ContainsFunc(svc.Contract.When[file], svc.Set) {
			delete(svc.Units, file) // off: no unit, and its placeholders need no value (#184)
		}
	}
	return svc, nil
}

func (st *State) validate() []error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	for _, svc := range st.Services {
		if err := proxySecretsDeclared(svc); err != nil {
			errs = append(errs, err)
		}
	}

	if st.Box.Domain == "" {
		fail("box.yml: domain is required")
	}
	if st.Box.DataDir == "" {
		fail("box.yml: dataDir is required")
	}
	tier := map[string]bool{}
	for _, t := range st.Policy.Tiers {
		tier[t] = true
	}
	for name, h := range st.Policy.Hands {
		if !tier[h.Ceiling] {
			fail("policy.yml: hand %q has unknown ceiling %q", name, h.Ceiling)
		}
	}
	st.Box.Laib.Host = cmp.Or(st.Box.Laib.Host, "laib")
	st.Box.Laib.Port = cmp.Or(st.Box.Laib.Port, 5900)
	st.Box.Laib.Listen = cmp.Or(st.Box.Laib.Listen, "127.0.0.1")
	st.Box.Laib.Group = cmp.Or(st.Box.Laib.Group, "admins")
	if st.Box.Roles.Model != "" && !st.Box.GPU.Present {
		fail("box.yml: roles.model is set but gpu.present is false")
	}
	if st.Box.Roles.Model != "" && st.Box.Model.URL != "" {
		fail("box.yml: roles.model and model.url are both set; a box either serves the model or points at one")
	}
	for role, svc := range map[string]string{"proxy": st.Box.Roles.Proxy, "sso": st.Box.Roles.SSO, "dns": st.Box.Roles.DNS, "identity": st.Box.Roles.Identity, "model": st.Box.Roles.Model} {
		if svc != "" {
			if _, ok := st.Services[svc]; !ok {
				fail("box.yml: roles.%s names %q, which is not under services/", role, svc)
			}
		}
	}

	hosts := map[string]string{st.Box.Laib.Host: "laib (box.yml laib.host)"}
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		c := svc.Contract
		if !nameRe.MatchString(name) {
			fail("services/%s: name must match %s", name, nameRe)
		}
		if c.Name != name {
			fail("services/%s/laib.yml: name is %q, folder is %q", name, c.Name, name)
		}
		if len(svc.Units) == 0 {
			fail("services/%s: no .container/.pod/.kube/.service file", name)
		}
		declared := map[string]bool{}
		for _, s := range c.Secrets {
			declared[s.Name] = true
		}
		if c.OIDC != nil && c.OIDC.ClientID != "" {
			declared[OIDCSecretName(c.OIDC.ClientID)] = true // generated with the client, referenced by values
		}
		for _, k := range slices.Sorted(maps.Keys(svc.Values)) {
			if ref := svc.Values[k].Secret; ref != "" && !declared[ref] {
				fail("services/%s/values.yml: %s references undeclared secret %s", name, k, ref)
			}
		}
		for i, r := range c.Routes {
			switch r.Exposure {
			case "internal", "public":
			default:
				fail("services/%s/laib.yml: routes[%d].exposure must be internal or public", name, i)
			}
			switch r.Auth {
			case "none", "one_factor", "two_factor":
			default:
				fail("services/%s/laib.yml: routes[%d].auth must be none, one_factor or two_factor", name, i)
			}
			if r.Host == "" || r.Port == 0 {
				fail("services/%s/laib.yml: routes[%d] needs host and port", name, i)
			}
			if prev, dup := hosts[r.Host]; dup {
				fail("services/%s/laib.yml: host %q already published by %s", name, r.Host, prev)
			}
			hosts[r.Host] = name
			if r.Auth != "none" && st.Box.Roles.SSO == "" {
				fail("services/%s/laib.yml: route %s wants auth but box.yml has no roles.sso", name, r.Host)
			}
		}
		if c.OIDC != nil && c.OIDC.ClientID == "" {
			fail("services/%s/laib.yml: oidc.clientId is required", name)
		}
		if c.GPU && !st.Box.GPU.Present {
			fail("services/%s/laib.yml: gpu: true but box.yml gpu.present is false", name)
		}
		carveOut := slices.Contains(st.Box.HostNetwork, name)
		unitHost := false
		for _, content := range svc.Units {
			if unitUsesHostNetwork(content) {
				unitHost = true
			}
		}
		switch {
		case unitHost && !c.HostNetwork:
			fail("services/%s: a unit says Network=host but laib.yml does not declare hostNetwork: true", name)
		case !unitHost && c.HostNetwork:
			fail("services/%s: laib.yml declares hostNetwork: true but no unit says Network=host", name)
		case c.HostNetwork && !carveOut:
			fail("services/%s: hostNetwork needs the service in box.yml hostNetwork (the closed carve-out list)", name)
		}
		for _, need := range c.Needs {
			switch need {
			case "mcp":
				if st.Box.Laib.Listen == "127.0.0.1" {
					fail("services/%s needs laib's MCP from a container: set box.yml laib.listen: 0.0.0.0 (the nftables drop keeps the LAN out, ADR 0012 §4)", name)
				}
			default:
				fail("services/%s/laib.yml: needs %q is not a facility laib knows (mcp)", name, need)
			}
		}
		for _, dep := range c.After {
			if _, ok := st.Services[dep]; !ok {
				fail("services/%s/laib.yml: after names %q, which is not under services/", name, dep)
			}
			if dep == name {
				fail("services/%s/laib.yml: after names itself", name)
			}
		}
		for file, content := range svc.Units {
			for _, v := range Placeholders(content) {
				if _, ok := svc.Values[v]; ok || builtin(v) {
					continue
				}
				fail("services/%s/%s: ${%s} has no value", name, file, v)
			}
		}
	}
	// one host port, one service (#46): two units publishing the same port
	// never both start, and podman says so only at the second start
	owner := map[string]string{}
	for _, name := range st.ServiceNames() {
		for _, content := range st.Services[name].Units {
			for _, hp := range PublishedHostPorts(content) {
				if prev, ok := owner[hp]; ok && prev != name {
					fail("services/%s and services/%s both publish host port %s", prev, name, hp)
				}
				owner[hp] = name
			}
		}
	}
	return errs
}

var placeholderRe = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)\}`)

// Placeholders lists the distinct ${VAR} names in a unit file.
func Placeholders(content string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(content, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// proxySecretsDeclared: a route's proxySecret must be one of the service's
// own declared secrets, so apply generates it and the renderer finds it.
func proxySecretsDeclared(svc *Service) error {
	for _, r := range svc.Contract.Routes {
		if r.ProxySecret == "" {
			continue
		}
		if !slices.ContainsFunc(svc.Contract.Secrets, func(s Secret) bool { return s.Name == r.ProxySecret }) {
			return fmt.Errorf("services/%s: route %s names proxySecret %s, which the service does not declare under secrets", svc.Name, r.Host, r.ProxySecret)
		}
	}
	return nil
}

// Builtins are the placeholders every unit may use without a values.yml entry.
var Builtins = []string{"DOMAIN", "DATA_DIR", "TZ", "SERVICE", "LAN_IP", "MODEL_URL", "MODEL_KEY", "MODEL_LEASE",
	"PERSONA", "PERSONA_DISPLAY", "PERSONA_PHRASE", "PERSONA_WAKEWORD", "PERSONA_VOICE", "PERSONA_LANGUAGES", "PERSONA_REV", // the house persona (ADR 0020)
	"PEOPLE_REV"} // box.yml people (ADR 0022 §3): a unit that carries it gets render/people/people.json

// ModelURL is where a service on this box finds the LLM: the local model
// role's proxy, or box.yml model.url. Empty when there is neither.
func (st *State) ModelURL() string {
	if st.Box.Roles.Model != "" {
		port := 11435
		if svc, ok := st.Services[st.Box.Roles.Model]; ok && len(svc.Contract.Routes) > 0 {
			port = svc.Contract.Routes[0].Port
		}
		return fmt.Sprintf("http://127.0.0.1:%d/v1", port)
	}
	return st.Box.Model.URL
}

// ModelLease is the lease endpoint, local or remote; empty when none.
func (st *State) ModelLease() string {
	if st.Box.Roles.Model != "" {
		// the lease is served by the proxy, on the proxy's port: one port everywhere (#42)
		return strings.TrimSuffix(st.ModelURL(), "/v1") + "/api/model-lease"
	}
	return st.Box.Model.Lease
}

func builtin(v string) bool { return slices.Contains(Builtins, v) }

// ServiceNames returns the service names sorted, so every output is stable.
func (st *State) ServiceNames() []string {
	names := make([]string, 0, len(st.Services))
	for n := range st.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// UnitNames returns the systemd unit names a service's Quadlet files produce.
// Quadlet names a `.container` file `x.container` as `x.service` and a `.pod`
// file `x.pod` as `x-pod.service`.
func (svc *Service) UnitNames() []string {
	var out []string
	for file := range svc.Units {
		base := strings.TrimSuffix(file, filepath.Ext(file))
		switch filepath.Ext(file) {
		case ".container", ".kube":
			out = append(out, base+".service")
		case ".pod":
			out = append(out, base+"-pod.service")
		case ".service":
			out = append(out, file) // a plain user unit, e.g. llama-lease.service
		}
	}
	sort.Strings(out)
	return out
}

var hostNetRe = regexp.MustCompile(`(?m)^\s*Network\s*=\s*host\s*$`)

// unitUsesHostNetwork reports whether a Quadlet unit asks for the host
// network namespace.
func unitUsesHostNetwork(content string) bool { return hostNetRe.MatchString(content) }

// Order returns the service names in apply order (#12): platform roles first
// (identity, sso, proxy, dns, model), then everything else alphabetically,
// with `after:` honoured by moving a service behind its dependencies.
func (st *State) Order(names []string) []string {
	rank := map[string]int{}
	for i, role := range []string{st.Box.Roles.Identity, st.Box.Roles.SSO, st.Box.Roles.Proxy, st.Box.Roles.DNS, st.Box.Roles.Model} {
		if role != "" {
			rank[role] = i + 1
		}
	}
	sorted := append([]string(nil), names...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := cmp.Or(rank[sorted[i]], 100), cmp.Or(rank[sorted[j]], 100)
		if ri != rj {
			return ri < rj
		}
		return sorted[i] < sorted[j]
	})
	// after: a few passes of "move behind the last dependency"; N is small.
	for pass := 0; pass < len(sorted); pass++ {
		moved := false
		for i, name := range sorted {
			svc, ok := st.Services[name]
			if !ok {
				continue
			}
			last := -1
			for _, dep := range svc.Contract.After {
				for j, other := range sorted {
					if other == dep && j > last {
						last = j
					}
				}
			}
			if last > i {
				sorted = append(sorted[:i], sorted[i+1:]...)
				sorted = append(sorted[:last], append([]string{name}, sorted[last:]...)...)
				moved = true
				break
			}
		}
		if !moved {
			break
		}
	}
	return sorted
}

// purposeWarnings: a purpose served by more than one service is the "100
// tools for one topic" smell. It is a warning, not an error: the human sees
// it in the plan and in the Inbox and decides. Strict mode could make it an
// error later; nothing in the tree asks for that yet.
func (st *State) purposeWarnings() []string {
	by := map[string][]string{}
	for _, name := range st.ServiceNames() {
		for _, p := range st.Services[name].Contract.Provides {
			by[p] = append(by[p], name)
		}
	}
	var out []string
	for _, k := range slices.Sorted(maps.Keys(by)) {
		if len(by[k]) > 1 {
			out = append(out, fmt.Sprintf("Zweck %q wird von %d Diensten erfüllt: %s", k, len(by[k]), strings.Join(by[k], ", ")))
		}
	}
	return out
}

// OIDCSecretName is the secret that holds a service's OIDC client secret;
// generated on first apply like any declared secret.
func OIDCSecretName(clientID string) string {
	return "OIDC_" + strings.ToUpper(strings.ReplaceAll(clientID, "-", "_")) + "_SECRET"
}

// PublishedHostPorts lists the host ports a unit publishes (PublishPort=
// [ip:]host:container[/proto]).
func PublishedHostPorts(unit string) []string {
	var out []string
	for _, line := range strings.Split(unit, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "PublishPort=") {
			continue
		}
		spec := strings.SplitN(strings.TrimPrefix(line, "PublishPort="), "/", 2)[0]
		parts := strings.Split(spec, ":")
		switch len(parts) {
		case 2:
			out = append(out, parts[0])
		case 3:
			out = append(out, parts[1])
		}
	}
	return out
}
