package templates

import (
	"io/fs"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// #785: „Wo ist Jellyfin?“ — the Box page showed service ids. Every
// template names itself the way the household says it, with one short
// line of what it is; the page shows that big and the id small.
func TestEveryTemplateHasAPlainNameAndAShortDescription(t *testing.T) {
	dirs, _ := fs.ReadDir(FS, ".")
	for _, d := range dirs {
		b, err := fs.ReadFile(FS, d.Name()+"/laib.yml")
		if err != nil {
			continue
		}
		var c struct{ Title, Beschreibung string }
		if err := yaml.Unmarshal(b, &c); err != nil {
			t.Fatalf("%s: %v", d.Name(), err)
		}
		if c.Title == "" || c.Beschreibung == "" {
			t.Errorf("templates/%s: title %q, beschreibung %q: both are what the Box page shows", d.Name(), c.Title, c.Beschreibung)
		}
		if len([]rune(c.Beschreibung)) > 45 || len([]rune(c.Title)) > 25 {
			t.Errorf("templates/%s: %q · %q is longer than a phone's card line", d.Name(), c.Title, c.Beschreibung)
		}
		if strings.Contains(strings.ToLower(c.Title+c.Beschreibung), "solaris") {
			t.Errorf("templates/%s names Solaris, which is gone", d.Name())
		}
	}
}
