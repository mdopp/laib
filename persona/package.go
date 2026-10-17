package persona

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/mdopp/laib/personas"
	"gopkg.in/yaml.v3"
)

// Persona is what the engine needs of a persona package (ADR 0020):
// persona.yml and style.md, as laib rendered them to render/persona. laib
// validated the whole package when it loaded the state; the engine reads
// its two files and keeps no copy of that validation (#309).
type Persona struct {
	Name      string            `yaml:"name"`
	Display   string            `yaml:"display"`
	Languages []string          `yaml:"languages"`
	Prompt    string            `yaml:"prompt"` // the name in the directory's prompt; default the display name in capitals
	Greeting  map[string]string `yaml:"greeting"`
	Gimmicks  []string          `yaml:"gimmicks"`
	Files     map[string]string `yaml:"-"` // persona.yml and style.md as read
	Source    string            `yaml:"-"` // rendered, builtin
}

// readPersona reads one package's persona.yml and style.md.
func readPersona(fsys fs.FS, name, source string) (*Persona, error) {
	p := &Persona{Files: map[string]string{}, Source: source}
	for _, f := range []string{"persona.yml", "style.md"} {
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

// builtin reads personas/<name> from the binary.
func builtin(name string) (*Persona, error) {
	sub, err := fs.Sub(personas.FS, name)
	if err != nil {
		return nil, err
	}
	return readPersona(sub, name, "builtin")
}
