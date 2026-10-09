package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// unitExts are the Quadlet file kinds Laib copies verbatim after substitution.
var unitExts = map[string]bool{".container": true, ".pod": true, ".volume": true, ".network": true, ".kube": true}

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

	errs = append(errs, st.validate()...)
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
	svc := &Service{Name: filepath.Base(dir), Dir: dir, Values: map[string]Value{}, Units: map[string]string{}}
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
		if f.IsDir() || !unitExts[filepath.Ext(f.Name())] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, err
		}
		svc.Units[f.Name()] = string(b)
	}
	return svc, nil
}

func (st *State) validate() []error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

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
	if st.Box.Laib.Host == "" {
		st.Box.Laib.Host = "laib"
	}
	if st.Box.Laib.Port == 0 {
		st.Box.Laib.Port = 5900
	}
	if st.Box.Laib.Listen == "" {
		st.Box.Laib.Listen = "127.0.0.1"
	}
	if st.Box.Laib.Group == "" {
		st.Box.Laib.Group = "admins"
	}
	if st.Box.Roles.Model != "" && !st.Box.GPU.Present {
		fail("box.yml: roles.model is set but gpu.present is false")
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
			fail("services/%s: no .container/.pod/.kube file", name)
		}
		declared := map[string]bool{}
		for _, s := range c.Secrets {
			declared[s.Name] = true
		}
		keys := make([]string, 0, len(svc.Values))
		for k := range svc.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
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
		for file, content := range svc.Units {
			for _, v := range Placeholders(content) {
				if _, ok := svc.Values[v]; ok || builtin(v) {
					continue
				}
				fail("services/%s/%s: ${%s} has no value", name, file, v)
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

// Builtins are the placeholders every unit may use without a values.yml entry.
var Builtins = []string{"DOMAIN", "DATA_DIR", "TZ", "SERVICE", "LAN_IP"}

func builtin(v string) bool {
	for _, b := range Builtins {
		if b == v {
			return true
		}
	}
	return false
}

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
		}
	}
	sort.Strings(out)
	return out
}
