package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mdopp/laib/personas"
	"gopkg.in/yaml.v3"
)

// Persona is personas/<name>/persona.yml (ADR 0020): who the house is — name,
// wake word, voice, greeting, colours, gimmicks — plus style.md and icon.svg.
// Style only: the rules are code and win over anything style.md says.
type Persona struct {
	Name      string   `yaml:"name"`
	Display   string   `yaml:"display"`
	Languages []string `yaml:"languages"`
	Wakeword  struct {
		Phrase string `yaml:"phrase"`
		ID     string `yaml:"id"` // the trained model's name; derived from the phrase when empty
	} `yaml:"wakeword"`
	Voice    string            `yaml:"voice"`  // a Piper voice
	Prompt   string            `yaml:"prompt"` // the name in the directory's prompt; default the display name in capitals
	Greeting map[string]string `yaml:"greeting"`
	Colors   map[string]string `yaml:"colors"`
	Gimmicks []string          `yaml:"gimmicks"`
	Files    map[string]string `yaml:"-"` // persona.yml, style.md, icon.svg as read
	Source   string            `yaml:"-"` // state, registry <name>, builtin
}

var nonID = regexp.MustCompile(`[^a-z0-9]+`)

// JSON is the resolved persona for consumers without a YAML parser: Haus,
// and the app through Haus (#129). Defaults filled in, files left out.
func (p *Persona) JSON() string {
	b, _ := json.Marshal(map[string]any{
		"name": p.Name, "display": p.Display, "languages": p.Languages, "phrase": p.Wakeword.Phrase,
		"wakeword": p.WakewordID(), "voice": p.Voice, "prompt": p.Prompt, "greeting": p.Greeting,
		"colors": p.Colors, "gimmicks": p.Gimmicks,
	})
	return string(b) + "\n"
}

// WakewordID is the wake-word model's name: "Hey Joshua" → hey_joshua.
func (p *Persona) WakewordID() string {
	if p.Wakeword.ID != "" {
		return p.Wakeword.ID
	}
	return strings.Trim(nonID.ReplaceAllString(strings.ToLower(p.Wakeword.Phrase), "_"), "_")
}

// Rev changes with every file of the persona, so a unit that carries it is
// restarted when the persona changes (a persona change is a plan, ADR 0020).
func (p *Persona) Rev() string {
	h := sha256.New()
	for _, f := range []string{"persona.yml", "style.md", "icon.svg"} {
		fmt.Fprintf(h, "%s\x00%s\x00", f, p.Files[f])
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// PersonaRev covers every persona in use and the chat map: one value a unit
// carries to be restarted on any persona change.
func (st *State) PersonaRev() string {
	h := sha256.New()
	for _, name := range sortedPersonaNames(st.Personas) {
		fmt.Fprintf(h, "%s=%s\n", name, st.Personas[name].Rev())
	}
	h.Write([]byte(st.ChatMap()))
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// ChatMap is render/persona/chat.yml: which Signal number talks to which
// persona (the house persona for every number not listed).
func (st *State) ChatMap() string {
	numbers := make([]string, 0, len(st.Box.ChatPersonas))
	for n := range st.Box.ChatPersonas {
		numbers = append(numbers, n)
	}
	sort.Strings(numbers)
	var b strings.Builder
	b.WriteString("# Rendered by laib from box.yml (ADR 0020): the house persona, and a persona per Signal number.\nhouse: " + st.Box.Persona + "\nchat:\n")
	for _, n := range numbers {
		fmt.Fprintf(&b, "  %q: %s\n", n, st.Box.ChatPersonas[n])
	}
	return b.String()
}

// HousePersona is box.yml persona, Joshua when unset.
func (st *State) HousePersona() *Persona { return st.Personas[st.Box.Persona] }

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// loadPersonas resolves the house persona and every chat persona: the state
// repo first, then the registries next to it, then the built-ins.
func (st *State) loadPersonas() []error {
	var errs []error
	if st.Box.Persona == "" {
		st.Box.Persona = "joshua"
	}
	st.Personas = map[string]*Persona{}
	names := []string{st.Box.Persona}
	numbers := make([]string, 0, len(st.Box.ChatPersonas))
	for n := range st.Box.ChatPersonas {
		numbers = append(numbers, n)
	}
	sort.Strings(numbers)
	for _, n := range numbers {
		if !e164.MatchString(n) {
			errs = append(errs, fmt.Errorf("box.yml chatPersonas: %q is not a phone number in E.164 (+49…)", n))
		}
		names = append(names, st.Box.ChatPersonas[n])
	}
	for _, name := range names {
		if _, done := st.Personas[name]; done {
			continue
		}
		p, err := findPersona(st.Dir, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		st.Personas[name] = p
	}
	if p := st.HousePersona(); p != nil && p.Wakeword.Phrase == "" {
		errs = append(errs, fmt.Errorf("persona %q is the house persona but has no wake word (wakeword.phrase)", p.Name))
	}
	return errs
}

func findPersona(stateDir, name string) (*Persona, error) {
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("persona %q: not a name", name)
	}
	if dir := filepath.Join(stateDir, "personas", name); exists(dir) {
		return ReadPersona(os.DirFS(dir), name, "state")
	}
	regs, _ := filepath.Glob(filepath.Join(filepath.Dir(stateDir), "registries", "*", "personas", name, "persona.yml"))
	if len(regs) > 0 {
		dir := filepath.Dir(regs[0])
		return ReadPersona(os.DirFS(dir), name, "registry "+filepath.Base(filepath.Dir(filepath.Dir(dir))))
	}
	if p, err := BuiltinPersona(name); err == nil {
		return p, nil
	}
	return nil, fmt.Errorf("persona %q: not in the state repo, a registry or built in", name)
}

// BuiltinPersona reads personas/<name> from the binary.
func BuiltinPersona(name string) (*Persona, error) {
	sub, err := fs.Sub(personas.FS, name)
	if err != nil {
		return nil, err
	}
	return ReadPersona(sub, name, "builtin")
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// ReadPersona reads one package: persona.yml, style.md, icon.svg.
func ReadPersona(fsys fs.FS, name, source string) (*Persona, error) {
	p := &Persona{Files: map[string]string{}, Source: source}
	for _, f := range []string{"persona.yml", "style.md", "icon.svg"} {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, fmt.Errorf("persona %q (%s): %s missing", name, source, f)
		}
		p.Files[f] = string(b)
	}
	if err := yaml.Unmarshal([]byte(p.Files["persona.yml"]), p); err != nil {
		return nil, fmt.Errorf("persona %q: persona.yml: %w", name, err)
	}
	if p.Name != name || p.Display == "" || len(p.Languages) == 0 {
		return nil, fmt.Errorf("persona %q (%s): persona.yml needs name: %s, display and languages", name, source, name)
	}
	if p.Prompt == "" {
		p.Prompt = strings.ToUpper(p.Display)
	}
	return p, nil
}

// ruleTalk is style.md overstepping into rules (ADR 0020 §3): approvals,
// tokens, or ignoring instructions. The rules are code and still win.
var ruleTalk = regexp.MustCompile(`(?i)freigabe|freigeben|genehmig|approv|token|regeln? (zu )?ignor|ignore (the |all |any )?(rules|instructions)`)

func (st *State) personaWarnings() []string {
	var out []string
	for _, name := range sortedPersonaNames(st.Personas) {
		if ruleTalk.MatchString(st.Personas[name].Files["style.md"]) {
			out = append(out, fmt.Sprintf("persona %s: style.md talks about approvals or rules; a persona is style only, the rules are code and win (ADR 0020 §3)", name))
		}
	}
	return out
}

func sortedPersonaNames(m map[string]*Persona) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
