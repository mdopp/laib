package templates

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 2026-11-10: whether Immich keeps each person's photos apart had to be dug
// out of a post-deploy script. docs/DATA.md says, per service, whose data it
// keeps and who reaches it. A template with a privacy class, its own logins
// (oidc:) or one instance per person has a section `### <name>` there, and
// the section names the class the template declares, so neither can drift
// silently. What the section says beyond that is the PR's to keep true.
func TestEveryTemplateWithFamilyDataIsInDataDoc(t *testing.T) {
	doc, err := os.ReadFile("../docs/DATA.md")
	if err != nil {
		t.Fatalf("docs/DATA.md: %v", err)
	}
	dirs, _ := fs.ReadDir(FS, ".")
	for _, d := range dirs {
		b, err := fs.ReadFile(FS, d.Name()+"/laib.yml")
		if err != nil {
			continue
		}
		var c struct {
			Private   string
			PerPerson bool `yaml:"perPerson"`
			OIDC      any  `yaml:"oidc"`
		}
		if err := yaml.Unmarshal(b, &c); err != nil {
			t.Fatalf("%s: %v", d.Name(), err)
		}
		if c.Private == "" && !c.PerPerson && c.OIDC == nil {
			continue
		}
		sec := section(string(doc), d.Name())
		if sec == "" {
			t.Errorf("templates/%s keeps the family's data or logins: docs/DATA.md needs a section \"### %s\"", d.Name(), d.Name())
			continue
		}
		want := "no `private:`"
		if c.Private != "" {
			want = "`private: " + c.Private + "`"
		}
		if !strings.Contains(strings.ToLower(sec), want) {
			t.Errorf("docs/DATA.md ### %s does not say %s, which templates/%s/laib.yml declares", d.Name(), want, d.Name())
		}
	}
}

// section returns the text under "### <name>" up to the next heading of level 2 or 3.
func section(doc, name string) string {
	_, rest, ok := strings.Cut(doc, "\n### "+name+"\n")
	if !ok {
		return ""
	}
	for _, h := range []string{"\n### ", "\n## "} {
		if i := strings.Index(rest, h); i >= 0 {
			rest = rest[:i]
		}
	}
	return rest
}
