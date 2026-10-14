package render

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

// shippedBox loads every shipped template, each with its values.example.yml,
// into one state.
func shippedBox(t *testing.T) *state.State {
	t.Helper()
	repo := t.TempDir()
	contracts, _ := filepath.Glob(filepath.Join("..", "..", "templates", "*", "laib.yml"))
	var names []string
	for _, c := range contracts {
		src := filepath.Dir(c)
		name := filepath.Base(src)
		names = append(names, name)
		dst := filepath.Join(repo, "services", name)
		os.MkdirAll(dst, 0o755)
		entries, _ := os.ReadDir(src)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, _ := os.ReadFile(filepath.Join(src, e.Name()))
			if e.Name() == "values.example.yml" {
				os.WriteFile(filepath.Join(dst, "values.yml"), b, 0o644)
			}
			os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644)
		}
	}
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: x.home\ndataDir: /d\nlanIp: 10.0.0.2\ngpu: {present: true}\nhostNetwork: ["+strings.Join(names, ", ")+"]\nroles: {sso: auth, proxy: caddy}\nlaib: {listen: 0.0.0.0}\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "policy.yml"), []byte("tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n"), 0o644)
	st, err := state.Load(repo)
	if err != nil {
		t.Fatalf("the shipped templates do not load with their values.example.yml:\n%v", err)
	}
	return st
}

// backedBy maps each values.example.yml key to the declared secret its value
// names, read from the YAML itself so a shape the loader misreads still counts.
func backedBy(t *testing.T, svc *state.Service) map[string]string {
	t.Helper()
	declared := map[string]bool{}
	for _, s := range svc.Contract.Secrets {
		declared[s.Name] = true
	}
	if o := svc.Contract.OIDC; o != nil {
		declared[state.OIDCSecretName(o.ClientID)] = true
	}
	b, _ := os.ReadFile(filepath.Join("..", "..", "templates", svc.Name, "values.example.yml"))
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil || len(doc.Content) != 1 {
		t.Fatalf("%s/values.example.yml: %v", svc.Name, err)
	}
	var names func(n *yaml.Node) string
	names = func(n *yaml.Node) string {
		if n.Kind == yaml.ScalarNode && declared[n.Value] {
			return n.Value
		}
		for _, c := range n.Content {
			if s := names(c); s != "" {
				return s
			}
		}
		return ""
	}
	out := map[string]string{}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if s := names(m.Content[i+1]); s != "" {
			out[m.Content[i].Value] = s
		}
	}
	return out
}

func unitPath(name, file string) string {
	if ext := filepath.Ext(file); ext == ".service" || ext == ".timer" {
		return "systemd/" + file
	}
	return "quadlet/" + name + "/" + file
}

// #185: every shipped template, rendered from its values.example.yml with
// every declared secret set, carries each secret-backed value into its
// units: never empty, never the placeholder.
func TestShippedTemplatesCarryTheirSecrets(t *testing.T) {
	st := shippedBox(t)
	secrets := map[string]string{}
	for _, s := range SecretNames(st) {
		secrets[s.Name] = "fake-" + s.Name
	}
	out, err := Render(st, secrets)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		for key, secret := range backedBy(t, svc) {
			for file, src := range svc.Units {
				if !strings.Contains(src, "${"+key+"}") {
					continue
				}
				checked++
				if got := out[unitPath(name, file)]; !strings.Contains(got, "fake-"+secret) {
					t.Errorf("%s/%s: ${%s} is backed by the secret %s, which is set, but the unit does not carry it", name, file, key, secret)
				}
			}
		}
	}
	if checked < 20 {
		t.Fatalf("only %d secret-backed placeholders checked; the walk is broken", checked)
	}
	for p, c := range out {
		if strings.Contains(c, "${secret:") {
			t.Errorf("%s carries a secret placeholder although every secret is set", p)
		}
	}
}

// #185: an optional secret that stays unset renders as an empty value, the
// same as a hook's LAIB_SECRET_<NAME>; the Environment= line stays, and
// nothing on the box ever carries the placeholder text.
func TestAnUnsetOptionalSecretRendersEmpty(t *testing.T) {
	st := shippedBox(t)
	secrets := map[string]string{}
	optional := map[string]bool{}
	for _, s := range SecretNames(st) {
		if !s.Optional {
			secrets[s.Name] = "fake-" + s.Name // generated or required by some service: set
		}
	}
	for _, s := range SecretNames(st) {
		if s.Optional && secrets[s.Name] == "" {
			optional[s.Name] = true
		}
	}
	if !optional["HAUS_HA_TOKEN"] || !optional["DNS_API_TOKEN"] {
		t.Fatalf("the shipped templates no longer declare the optional secrets this test leans on: %v", optional)
	}
	out, err := Render(st, secrets)
	if err != nil {
		t.Fatal(err)
	}
	for p, c := range out {
		if strings.Contains(c, "${secret:") {
			t.Errorf("%s carries a secret placeholder", p)
		}
	}
	checked := 0
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		for key, secret := range backedBy(t, svc) {
			if !optional[secret] {
				continue
			}
			line := regexp.MustCompile(`(?m)^Environment=([A-Za-z0-9_]+)=\$\{` + key + `\}$`)
			for file, src := range svc.Units {
				for _, m := range line.FindAllStringSubmatch(src, -1) {
					checked++
					if got := out[unitPath(name, file)]; !regexp.MustCompile(`(?m)^Environment=` + m[1] + `=$`).MatchString(got) {
						t.Errorf("%s/%s: %s is backed by the unset optional secret %s and must render as Environment=%s=", name, file, m[1], secret, m[1])
					}
				}
			}
		}
	}
	if checked < 3 {
		t.Fatalf("only %d optional secret lines checked; the walk is broken", checked)
	}
}
