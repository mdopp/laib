package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

// tree is everything the pages are made of, read from one checkout: the
// PR's, or main's for the gate.
type tree struct {
	module  string // github.com/mdopp/laib
	tpls    []tpl
	hostnet map[string]map[string]string
	budgets []budget
	pkgs    []pkg
	policy  state.Policy
	fixed   map[string]state.Hand // the hands fixed in code beside policy.yml (HaushaltHand, LeaseHand)
	c4      c4src
	sec     secsrc
	c4raw   []byte
	secraw  []byte
}

type tpl struct {
	name  string
	c     state.Contract
	units []unit
}

type unit struct{ file, text string }

type budget struct {
	name  string
	cap   int
	lines int
}

type pkg struct {
	path    string // internal/state, cmd/laib
	lines   int
	imports []string
}

type c4src struct {
	Einleitung string `yaml:"einleitung"`
	Kontext    struct {
		Text    string `yaml:"text"`
		Mermaid string `yaml:"mermaid"`
	} `yaml:"kontext"`
	Schichten map[string]int      `yaml:"schichten"`
	Kern      map[string]kernpart `yaml:"kern"`
	Programme map[string]program  `yaml:"programme"`
	Daten     struct {
		Text    string   `yaml:"text"`
		Mermaid string   `yaml:"mermaid"`
		Regeln  []string `yaml:"regeln"`
	} `yaml:"daten"`
	Ablauf struct {
		Titel   string `yaml:"titel"`
		Mermaid string `yaml:"mermaid"`
	} `yaml:"ablauf"`
	Faehigkeiten []gruppe `yaml:"faehigkeiten"`
}

type kernpart struct {
	Titel string `yaml:"titel"`
	Text  string `yaml:"text"`
	Zieht *struct {
		Nach  string `yaml:"nach"`
		Was   string `yaml:"was"`
		Issue string `yaml:"issue"`
	} `yaml:"zieht"`
}

type program struct {
	Ring    string `yaml:"ring"`
	Vorlage string `yaml:"vorlage"`
	Titel   string `yaml:"titel"`
	Text    string `yaml:"text"`
}

type gruppe struct {
	Titel  string   `yaml:"titel"`
	Punkte []string `yaml:"punkte"`
}

type stand struct {
	Status string   `yaml:"status"`
	Notiz  string   `yaml:"notiz"`
	Issues []string `yaml:"issues"`
}

type secsrc struct {
	Einleitung  string `yaml:"einleitung"`
	Grundsaetze []struct {
		Titel string `yaml:"titel"`
		Text  string `yaml:"text"`
		En    string `yaml:"en"`
	} `yaml:"grundsaetze"`
	Zonen struct {
		Text    string `yaml:"text"`
		Mermaid string `yaml:"mermaid"`
	} `yaml:"zonen"`
	Identitaeten []struct {
		Wer   string `yaml:"wer"`
		Wie   string `yaml:"wie"`
		Darf  string `yaml:"darf"`
		Stand stand  `yaml:"stand"`
	} `yaml:"identitaeten"`
	Netz []struct {
		Weg    string `yaml:"weg"`
		Schutz string `yaml:"schutz"`
		Stand  stand  `yaml:"stand"`
	} `yaml:"netz"`
	Daten []struct {
		Daten  string `yaml:"daten"`
		Liegen string `yaml:"liegen"`
		Wer    string `yaml:"wer"`
		Stand  stand  `yaml:"stand"`
	} `yaml:"daten"`
	Schichtregeln []gruppe `yaml:"schichtregeln"`
	WasPassiert   []struct {
		Fall  string `yaml:"fall"`
		Folge string `yaml:"folge"`
	} `yaml:"wasPassiert"`
	Offen []struct {
		Titel  string   `yaml:"titel"`
		Stand  stand    `yaml:"stand"`
		Punkte []string `yaml:"punkte"`
	} `yaml:"offen"`
	Aenderungen []struct {
		Fakt  string `yaml:"fakt"`
		Grund string `yaml:"grund"`
	} `yaml:"aenderungen"`
}

// load reads a checkout. It checks nothing about the hand-written files
// beyond their YAML (validate does): main's tree may predate them.
func load(root string) (*tree, error) {
	t := &tree{}
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	t.module = strings.TrimSpace(strings.TrimPrefix(strings.SplitN(string(gomod), "\n", 2)[0], "module "))
	steps := []func(string) error{t.loadTemplates, t.loadBudgets, t.loadPackages, t.loadPolicy, t.loadSources}
	for _, step := range steps {
		if err := step(root); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (t *tree) loadTemplates(root string) error {
	files, _ := filepath.Glob(filepath.Join(root, "templates", "*", "laib.yml"))
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		dir := filepath.Dir(f)
		x := tpl{name: filepath.Base(dir)}
		if err := yaml.Unmarshal(b, &x.c); err != nil {
			return &os.PathError{Op: "laib.yml", Path: f, Err: err}
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			switch filepath.Ext(e.Name()) {
			case ".container", ".service", ".pod", ".timer":
				u, _ := os.ReadFile(filepath.Join(dir, e.Name()))
				x.units = append(x.units, unit{e.Name(), string(u)})
			}
		}
		t.tpls = append(t.tpls, x)
	}
	if b, err := os.ReadFile(filepath.Join(root, "templates", "hostnet.yml")); err == nil {
		return yaml.Unmarshal(b, &t.hostnet)
	}
	return nil
}

var budgetLine = regexp.MustCompile(`^budget ([a-z0-9_-]+) ([0-9]+) ([^#]+)`)

// loadBudgets reads the programs and caps of scripts/loc.sh, and counts
// each one's production lines as loc.sh does.
func (t *tree) loadBudgets(root string) error {
	b, err := os.ReadFile(filepath.Join(root, "scripts", "loc.sh"))
	if err != nil {
		return err
	}
	for _, l := range strings.Split(string(b), "\n") {
		m := budgetLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		c, _ := strconv.Atoi(m[2])
		t.budgets = append(t.budgets, budget{name: m[1], cap: c, lines: lines(root, strings.Fields(m[3])...)})
	}
	return nil
}

// lines counts the production lines under dirs: every .go file but tests.
func lines(root string, dirs ...string) int {
	n := 0
	for _, d := range dirs {
		filepath.WalkDir(filepath.Join(root, d), func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				b, _ := os.ReadFile(p)
				n += bytes.Count(b, []byte("\n"))
			}
			return nil
		})
	}
	return n
}

// loadPackages reads the kernel's packages, internal/* and cmd/laib, with
// their lines and which of each other they import.
func (t *tree) loadPackages(root string) error {
	dirs, _ := filepath.Glob(filepath.Join(root, "internal", "*"))
	dirs = append(dirs, filepath.Join(root, "cmd", "laib"))
	for _, d := range dirs {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			continue
		}
		rel, _ := filepath.Rel(root, d)
		p := pkg{path: filepath.ToSlash(rel), lines: lines(root, rel)}
		p.imports = t.imports(d, p.path)
		t.pkgs = append(t.pkgs, p)
	}
	sort.Slice(t.pkgs, func(i, j int) bool { return t.pkgs[i].path < t.pkgs[j].path })
	return nil
}

func (t *tree) imports(dir, self string) []string {
	seen := map[string]bool{}
	filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if err != nil {
			return nil
		}
		for _, im := range f.Imports {
			if k := kernelPackage(t.module, strings.Trim(im.Path.Value, `"`)); k != "" && k != self {
				seen[k] = true
			}
		}
		return nil
	})
	return sortedKeys(seen)
}

// kernelPackage maps an import path to its kernel package, or "".
func kernelPackage(module, path string) string {
	rel, ok := strings.CutPrefix(path, module+"/")
	if !ok {
		return ""
	}
	parts := strings.Split(rel, "/")
	switch {
	case len(parts) >= 2 && parts[0] == "internal":
		return "internal/" + parts[1]
	case len(parts) >= 2 && parts[0] == "cmd" && parts[1] == "laib":
		return "cmd/laib"
	}
	return ""
}

// loadPolicy reads the built-in hands from internal/state's source, not
// from the compiled package, so main's tree shows main's hands: the
// DefaultPolicy constant and every `var XHand = Hand{…}`.
func (t *tree) loadPolicy(root string) error {
	t.fixed = map[string]state.Hand{}
	files, _ := filepath.Glob(filepath.Join(root, "internal", "state", "*.go"))
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if vs, ok := n.(*ast.ValueSpec); ok {
				t.policySpec(vs)
			}
			return true
		})
	}
	return nil
}

func (t *tree) policySpec(vs *ast.ValueSpec) {
	for i, name := range vs.Names {
		if i >= len(vs.Values) {
			return
		}
		switch v := vs.Values[i].(type) {
		case *ast.BasicLit:
			if s, err := strconv.Unquote(v.Value); err == nil && name.Name == "DefaultPolicy" {
				yaml.Unmarshal([]byte(s), &t.policy)
			}
		case *ast.CompositeLit:
			if id, ok := v.Type.(*ast.Ident); ok && id.Name == "Hand" && strings.HasSuffix(name.Name, "Hand") {
				t.fixed[strings.ToLower(strings.TrimSuffix(name.Name, "Hand"))] = handOf(v)
			}
		}
	}
}

func handOf(c *ast.CompositeLit) state.Hand {
	var h state.Hand
	for _, e := range c.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, _ := kv.Key.(*ast.Ident)
		if key == nil {
			continue
		}
		switch key.Name {
		case "Ceiling":
			h.Ceiling = literal(kv.Value)
		case "TTL":
			h.TTL = literal(kv.Value)
		case "Verbs":
			h.Verbs = literals(kv.Value)
		case "Paths":
			h.Paths = literals(kv.Value)
		}
	}
	return h
}

func literal(e ast.Expr) string {
	if b, ok := e.(*ast.BasicLit); ok {
		s, _ := strconv.Unquote(b.Value)
		return s
	}
	return ""
}

func literals(e ast.Expr) []string {
	c, ok := e.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range c.Elts {
		out = append(out, literal(x))
	}
	return out
}

func (t *tree) loadSources(root string) error {
	var err error
	if t.c4raw, err = os.ReadFile(filepath.Join(root, "docs", "c4.yml")); err == nil {
		if err := yaml.Unmarshal(t.c4raw, &t.c4); err != nil {
			return &os.PathError{Op: "yaml", Path: "docs/c4.yml", Err: err}
		}
	}
	if t.secraw, err = os.ReadFile(filepath.Join(root, "docs", "sicherheit.yml")); err == nil {
		if err := yaml.Unmarshal(t.secraw, &t.sec); err != nil {
			return &os.PathError{Op: "yaml", Path: "docs/sicherheit.yml", Err: err}
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// layer is a template's layer from docs/c4.yml, -1 when it has none.
func (t *tree) layer(name string) int {
	if l, ok := t.c4.Schichten[name]; ok {
		return l
	}
	return -1
}

func (t *tree) budget(name string) (budget, bool) {
	for _, b := range t.budgets {
		if b.name == name {
			return b, true
		}
	}
	return budget{}, false
}
