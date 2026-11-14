package templates

import (
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// #1166: every Network=host and SecurityLabelDisable=true in a template's
// unit is an exception with its reason in hostnet.yml, one place outside
// the templates, so a reason changes nothing on the box. No entry is
// stale, and the count per kind only falls: lower it here when one goes.
var exceptions = map[string]string{"Network=host": "host network", "SecurityLabelDisable=true": "labelling off"}

var exceptionBaseline = map[string]int{"host network": 20, "labelling off": 20}

func TestEveryHostNetworkAndLabelOffSaysWhy(t *testing.T) {
	b, err := os.ReadFile("hostnet.yml")
	if err != nil {
		t.Fatal(err)
	}
	var why map[string]map[string]string
	if err := yaml.Unmarshal(b, &why); err != nil {
		t.Fatalf("hostnet.yml: %v", err)
	}
	seen, count := map[string]bool{}, map[string]int{}
	fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".container" {
			return err
		}
		b, _ := fs.ReadFile(FS, p)
		for _, l := range strings.Split(string(b), "\n") {
			kind, ok := exceptions[strings.TrimSpace(l)]
			if !ok {
				continue
			}
			count[kind]++
			seen[p+" "+kind] = true
			if strings.TrimSpace(why[p][kind]) == "" {
				t.Errorf("templates/%s: %s without its reason in templates/hostnet.yml (%s: {%s: …})", p, strings.TrimSpace(l), p, kind)
			}
		}
		return nil
	})
	for p, kinds := range why {
		for kind := range kinds {
			if !seen[p+" "+kind] {
				t.Errorf("templates/hostnet.yml: %s %s, but the unit has no such line: drop the entry", p, kind)
			}
		}
	}
	for kind, was := range exceptionBaseline {
		if n := count[kind]; n > was {
			t.Errorf("%d × %s in the templates, the baseline says %d: a new one needs the owner's yes and the baseline raised with its reason (#1166)", n, kind, was)
		} else if n < was {
			t.Errorf("%d × %s in the templates, the baseline says %d: one went, lower the baseline", n, kind, was)
		}
	}
}
