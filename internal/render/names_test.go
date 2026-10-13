package render

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ADR 0021 rule 4: a mechanism is named for what it does, never after the
// persona it serves first. A persona is data; the engine, its command, image,
// container, MCP tool and values are `persona` (#133).
func TestMechanismsAreNotNamedAfterAPersona(t *testing.T) {
	personas := []string{"joshua"}
	root := filepath.Join("..", "..")
	named := func(what, s string) {
		for _, p := range personas {
			if strings.Contains(strings.ToLower(s), p) {
				t.Errorf("%s %q is named after the persona %s (ADR 0021)", what, s, p)
			}
		}
	}
	for _, dir := range []string{"cmd", "internal", "templates", ".github/workflows"} {
		filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() && d.Name() == "personas" {
				return filepath.SkipDir // a persona package is the persona's data, named after it on purpose
			}
			if err == nil {
				named("path", strings.TrimPrefix(path, root+"/"))
			}
			return nil
		})
	}
	tops, _ := os.ReadDir(root)
	for _, e := range tops {
		named("directory", e.Name())
	}
	tool := regexp.MustCompile(`\s{2}([a-z]+):\\n\s+url:`)
	hook, _ := os.ReadFile(filepath.Join(root, "templates", "bote", "post-deploy"))
	for _, m := range tool.FindAllStringSubmatch(string(hook), -1) {
		named("Bote MCP tool", m[1])
	}
	key := regexp.MustCompile(`(?m)^([A-Z_]+):`)
	vals, _ := filepath.Glob(filepath.Join(root, "templates", "*", "values.example.yml"))
	for _, v := range vals {
		b, _ := os.ReadFile(v)
		for _, m := range key.FindAllStringSubmatch(string(b), -1) {
			named("value", m[1])
		}
	}
	for _, f := range []string{"bote.pod", "laib.yml"} {
		b, _ := os.ReadFile(filepath.Join(root, "templates", "bote", f))
		for _, l := range strings.Split(string(b), "\n") {
			if i := strings.Index(l, "#"); i >= 0 {
				l = l[:i]
			}
			named("bote/"+f+" line", l)
		}
	}
}
