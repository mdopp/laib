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

// ADR 0021 rules 1 and 5: a Bote tool is named for the system it reaches. A
// tool named like a template must reach that template; the Home Assistant
// tool named `haus` reached HA while `haus` is the family's pages (#134).
func TestBoteToolsAreNamedForTheSystemTheyReach(t *testing.T) {
	root := filepath.Join("..", "..")
	hook, _ := os.ReadFile(filepath.Join(root, "templates", "bote", "post-deploy"))
	entry := regexp.MustCompile(`\s{2}([a-z]+):\\n\s+url: ([^\\]+)\\n`)
	found := map[string]string{}
	for _, m := range entry.FindAllStringSubmatch(string(hook), -1) {
		found[m[1]] = m[2]
		if _, err := os.Stat(filepath.Join(root, "templates", m[1], "laib.yml")); err == nil && !strings.Contains(strings.ToLower(m[2]), m[1]) {
			t.Errorf("the Bote tool %q is named like the template %s but reaches %s (ADR 0021)", m[1], m[1], m[2])
		}
	}
	if url, ok := found["ha"]; !ok || !strings.Contains(url, "HA_URL") {
		t.Errorf("Home Assistant is the Bote tool `ha` (ADR 0021): %v", found)
	}
}

// #135: a template that says how many containers its pod has is right. The
// bote pod said "two" while it ran five.
func TestPodCommentsCountTheirContainers(t *testing.T) {
	words := map[string]int{"two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7}
	count := regexp.MustCompile(`(?i)\b(two|three|four|five|six|seven) containers in one pod`)
	ymls, _ := filepath.Glob(filepath.Join("..", "..", "templates", "*", "laib.yml"))
	for _, y := range ymls {
		b, _ := os.ReadFile(y)
		text := regexp.MustCompile(`\s*\n\s*#\s*`).ReplaceAllString(string(b), " ") // a comment wraps
		m := count.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		units, _ := filepath.Glob(filepath.Join(filepath.Dir(y), "*.container"))
		if want := words[strings.ToLower(m[1])]; want != len(units) {
			t.Errorf("%s says %s containers in one pod, the template has %d", y, m[1], len(units))
		}
	}
}
