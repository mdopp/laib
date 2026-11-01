package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/registry"
)

// #986: a per-person template is linted as two persons' instances side by
// side. The test template passes; one that hard-codes a unit name, a
// container name or a data path both would share is refused.
func TestLintRefusesAPerPersonTemplateThatCollides(t *testing.T) {
	src := "../../testdata/perperson/notizbuch"
	if registry.LintTemplate(&cliEnv{}, src) != cli.ExitOK {
		t.Fatal("the per-person test template fails lint")
	}
	files := map[string]string{}
	entries, _ := os.ReadDir(src)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(src, e.Name()))
		files[e.Name()] = string(b)
	}
	for what, change := range map[string]func(map[string]string){
		"a unit not named after the template": func(f map[string]string) {
			f["sync.timer"] = f["notizbuch-sync.timer"]
			delete(f, "notizbuch-sync.timer")
		},
		"a fixed container name": func(f map[string]string) {
			f["notizbuch.container"] = strings.Replace(f["notizbuch.container"], "Image=", "ContainerName=notizbuch\nImage=", 1)
		},
		"a data path without ${SERVICE}": func(f map[string]string) {
			f["laib.yml"] = strings.Replace(f["laib.yml"], "${DATA_DIR}/${SERVICE}", "${DATA_DIR}/notizbuch", 1)
			f["notizbuch.container"] = strings.Replace(f["notizbuch.container"], "${DATA_DIR}/${SERVICE}", "${DATA_DIR}/notizbuch", 1)
		},
	} {
		f := map[string]string{}
		for k, v := range files {
			f[k] = v
		}
		change(f)
		dir := filepath.Join(t.TempDir(), "notizbuch")
		os.MkdirAll(dir, 0o755)
		for name, body := range f {
			os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
		}
		if registry.LintTemplate(&cliEnv{}, dir) == cli.ExitOK {
			t.Errorf("%s: two persons' instances would share it, and lint passed", what)
		}
	}
}
