package templates

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

// #1166: a container on the host network reaches every loopback listener
// of the box, and one with labelling off is not held by SELinux. Each is an
// exception, decided with its reason: one form, a comment block directly
// above the line that names it ("host network …", "labelling off …") and
// says why. The count per kind only falls; lower it here when one goes.
var exceptions = []struct{ line, names string }{
	{"Network=host", "host network"},
	{"SecurityLabelDisable=true", "labelling off"},
}

var exceptionBaseline = map[string]int{"Network=host": 20, "SecurityLabelDisable=true": 20}

func TestEveryHostNetworkAndLabelOffSaysWhy(t *testing.T) {
	count := map[string]int{}
	fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".container" {
			return err
		}
		b, _ := fs.ReadFile(FS, p)
		lines := strings.Split(string(b), "\n")
		for i, l := range lines {
			for _, e := range exceptions {
				if strings.TrimSpace(l) != e.line {
					continue
				}
				count[e.line]++
				var why []string
				for j := i - 1; j >= 0 && strings.HasPrefix(strings.TrimSpace(lines[j]), "#"); j-- {
					why = append(why, lines[j])
				}
				if !strings.Contains(strings.ToLower(strings.Join(why, " ")), e.names) {
					t.Errorf("templates/%s:%d: %s without its reason: a comment directly above that says „%s“ and why", p, i+1, e.line, e.names)
				}
			}
		}
		return nil
	})
	for _, e := range exceptions {
		if n, was := count[e.line], exceptionBaseline[e.line]; n != was {
			t.Errorf("%d × %s in the templates, the baseline says %d: %s", n, e.line, was, map[bool]string{true: "a new one needs the owner's yes and the baseline raised with its reason (#1166)", false: "one went, lower the baseline"}[n > was])
		}
	}
}
