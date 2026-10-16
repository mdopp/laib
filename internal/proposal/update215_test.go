package proposal

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

// #215: a template update never breaks an install. values.yml is the
// operator's and is kept, but a key the new template's values.example.yml
// brings and values.yml lacks would leave its placeholder without a value:
// the proposed state does not load, and the update never lands. The update
// carries the example's line over; every value already there stays as set.
func TestTemplateUpdateBringsNewValues(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	dst := filepath.Join(repo, "services", "echo")
	os.MkdirAll(dst, 0o755)
	os.WriteFile(filepath.Join(dst, "laib.yml"), []byte("name: echo\ntemplate: {registry: builtin, name: echo, version: 1}\nroutes: []\nsecrets: []\ndata: []\n"), 0o644)
	os.WriteFile(filepath.Join(dst, "echo.container"), []byte("[Container]\nImage=${IMAGE}\nEnvironment=OLD=${OLD}\n"), 0o644)
	os.WriteFile(filepath.Join(dst, "values.yml"), []byte("IMAGE: docker.io/echo:2 # pinned by hand\nOLD: mine"), 0o644)
	git(ctx, repo, "add", "-A")
	git(ctx, repo, "commit", "-q", "-m", "echo, installed")
	tpl := fstest.MapFS{
		"laib.yml":           {Data: []byte("name: echo\ntemplate: {registry: builtin, name: echo, version: 2}\nroutes: []\nsecrets: [{name: ECHO_KEY, external: true, optional: true}]\ndata: []\n")},
		"values.example.yml": {Data: []byte("IMAGE: docker.io/echo:1\nOLD: theirs\nNEW: http://host.containers.internal:1/v1 # the new thing\nKEY: !secret ECHO_KEY\n")},
		"echo.container":     {Data: []byte("[Container]\nImage=${IMAGE}\nEnvironment=OLD=${OLD}\nEnvironment=NEW=${NEW}\nEnvironment=KEY=${KEY}\n")},
	}
	p, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update echo", Edit{Update: &UpdateService{Name: "echo", Template: tpl}}, nil)
	if err != nil {
		t.Fatalf("a template update that adds values must still propose: %v", err)
	}
	if err := Approve(ctx, repo, inbox, p, "human:core"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "values.yml"))
	for _, want := range []string{"IMAGE: docker.io/echo:2", "# pinned by hand", "OLD: mine", "NEW: http://host.containers.internal:1/v1", "# the new thing", "KEY: !secret ECHO_KEY"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("values.yml lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "theirs") || strings.Contains(string(b), "echo:1") {
		t.Errorf("the update overwrote a value the operator has:\n%s", b)
	}
}

// #215, the case itself: a haus added before the Sprechen field has a
// values.yml without BOTE_URL and BOTE_KEY. The template update proposes,
// loads, and renders Haus's line to the Bote — with the Bote's key when
// there is one, empty without — and no hand edit.
func TestHausFromBeforeTheBoteStillPlans(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	box, _ := os.ReadFile(filepath.Join(repo, "box.yml"))
	os.WriteFile(filepath.Join(repo, "box.yml"), append(box, []byte("laib: {listen: 0.0.0.0}\n")...), 0o644) // haus needs laib's MCP
	src := filepath.Join("..", "..", "templates", "haus")
	dst := filepath.Join(repo, "services", "haus")
	os.MkdirAll(dst, 0o755)
	entries, _ := os.ReadDir(src)
	before := regexp.MustCompile(`(?m)^(BOTE_|Environment=HAUS_BOTE_).*\n`) // what haus had before #215
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(src, e.Name()))
		name := e.Name()
		switch name {
		case "README.md":
			continue
		case "values.example.yml":
			name = "values.yml"
			b = []byte(strings.Replace(string(before.ReplaceAllString(string(b), "")), `ENERGY_FLOW: ""`, `ENERGY_FLOW: pv=sensor.dach`, 1))
		case "haus.container":
			b = before.ReplaceAll(b, nil)
		}
		os.WriteFile(filepath.Join(dst, name), b, 0o644)
	}
	if _, err := state.Load(repo); err != nil {
		t.Fatalf("the install from before #215 must load: %v", err)
	}
	git(ctx, repo, "add", "-A")
	git(ctx, repo, "commit", "-q", "-m", "haus, added before #215")
	p, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update haus", Edit{Update: &UpdateService{Name: "haus", Template: os.DirFS(src)}}, nil)
	if err != nil {
		t.Fatalf("the haus template update must propose on an install from before #215: %v", err)
	}
	if err := Approve(ctx, repo, inbox, p, "human:core"); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(repo)
	if err != nil {
		t.Fatalf("haus after the update does not load: %v", err)
	}
	if v := st.Services["haus"].Values["ENERGY_FLOW"].Literal; v != "pv=sensor.dach" {
		t.Errorf("the operator's ENERGY_FLOW became %q", v)
	}
	for _, c := range []struct {
		secrets map[string]string
		key     string
	}{{map[string]string{"BOTE_API_KEY": "k-bote-api"}, "k-bote-api"}, {map[string]string{}, ""}} {
		out, err := render.Render(st, c.secrets)
		if err != nil {
			t.Fatal(err)
		}
		unit := out["quadlet/haus/haus.container"]
		for _, want := range []string{"\nEnvironment=HAUS_BOTE_URL=http://host.containers.internal:8642/v1\n", "\nEnvironment=HAUS_BOTE_KEY=" + c.key + "\n"} {
			if !strings.Contains(unit, want) {
				t.Errorf("haus.container lacks %q:\n%s", strings.TrimSpace(want), unit)
			}
		}
	}
}

// newValues at its edges: an empty example adds nothing, a values.yml that
// does not end its line still gets whole lines, an empty values.yml gets
// every key, and a new value that is not one line is refused by name
// rather than half copied.
func TestNewValuesEdges(t *testing.T) {
	for _, c := range []struct{ have, example, want, err string }{
		{"A: 1\n", "", "A: 1\n", ""},
		{"A: 1", "A: 2\nB: 3 # b\n", "A: 1\nB: 3 # b\n", ""},
		{"A: 1", "A: 2\n", "A: 1", ""},           // nothing new: not a byte changed
		{"A: B\n", "B: 2\n", "A: B\nB: 2\n", ""}, // a value is not a key
		{"", "A: 2\nB: !secret S\n", "A: 2\nB: !secret S\n", ""},
		{"A: 1\n", "A: 2\nB: |\n  x\n", "", "B is not one line"},
		{"A: 1\n", "A: 2\nB:\n  x\n", "", "B is not one line"},
		{"A: [1\n", "B: 2\n", "", "values.yml"},
		{"A: 1\n", "B: [2\n", "", "values.example.yml"},
	} {
		path := filepath.Join(t.TempDir(), "values.yml")
		os.WriteFile(path, []byte(c.have), 0o644)
		err := newValues(path, []byte(c.example))
		got, _ := os.ReadFile(path)
		switch {
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%q + %q: err %v, want %q", c.have, c.example, err, c.err)
		case c.err == "" && (err != nil || string(got) != c.want):
			t.Errorf("%q + %q → %q, %v; want %q", c.have, c.example, got, err, c.want)
		}
	}
}
