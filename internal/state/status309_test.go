package state

import (
	"strings"
	"testing"
)

// #309: a template declares its status file under ${DATA_DIR} and, when
// laib may refresh it, a oneshot of its own; anything else is refused.
func TestAStatusFileIsTheTemplatesOwn(t *testing.T) {
	c := "name: a\ntemplate: {registry: builtin, name: a, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {http: http://127.0.0.1:1/}\n"
	units := map[string]string{
		"a.container":     "[Container]\nImage=${IMAGE}\n",
		"a-check.service": "[Service]\nType=oneshot\nExecStart=/bin/true\n",
	}
	for status, want := range map[string]string{
		"status: {file: '${DATA_DIR}/a/s.json', check: a-check.service}": "",
		"status: {file: '${DATA_DIR}/a/s.json'}":                         "",
		"status: {file: '/etc/s.json'}":                                  "must lie under ${DATA_DIR}/",
		"status: {file: '${DATA_DIR}/../s.json'}":                        "must lie under ${DATA_DIR}/",
		"status: {file: '${DATA_DIR}/a/s.json', check: a.container}":     "no .service of the template without [Install]",
		"status: {file: '${DATA_DIR}/a/s.json', stale: soon}":            "status.stale",
	} {
		repo := box46(t, "")
		writeSvc(t, repo, "a", c+status+"\n", units)
		st, err := Load(repo)
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", status, err)
			} else if p := StatusPath(st, st.Services["a"]); p != "/tmp/d/a/s.json" {
				t.Errorf("%s: the path is %q", status, p)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", status, want, err)
		}
	}
}
