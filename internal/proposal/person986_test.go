package proposal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// #986: laib add notizbuch --person martin, then --person anna, writes the
// two services the golden case perperson renders: each its person and its
// port offset after the name line, the lowest offset free. A template
// update keeps both; a person on a plain template, a per-person template
// without one, or a person outside box.yml people is refused.
func TestAddForAPersonMakesTheirInstance(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	f, _ := os.OpenFile(filepath.Join(repo, "box.yml"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("people:\n  martin: [\"signal:+4915100000001\"]\n  anna: [\"signal:+4915100000002\"]\n")
	f.Close()
	tpl := os.DirFS("../../testdata/perperson/notizbuch")
	read := func(root, svc string) string {
		b, _ := os.ReadFile(filepath.Join(root, "services", svc, "laib.yml"))
		return string(b)
	}
	add := func(a *AddService) error {
		p, err := CreateFromEdit(ctx, repo, inbox, "human:core", "add "+a.Name, Edit{Add: a}, nil)
		if err == nil {
			err = Approve(ctx, repo, inbox, p, "human:core")
		}
		return err
	}
	for _, person := range []string{"martin", "anna"} {
		if err := add(&AddService{Name: "notizbuch-" + person, Person: person, Template: tpl}); err != nil {
			t.Fatal(err)
		}
		if got, want := read(repo, "notizbuch-"+person), read("../../testdata/golden/perperson/state", "notizbuch-"+person); got != want {
			t.Errorf("services/notizbuch-%s/laib.yml:\n%s\nwant (the golden case's):\n%s", person, got, want)
		}
	}
	for want, a := range map[string]*AddService{
		"is added with --person":          {Name: "notizbuch", Template: tpl},
		"only it":                         {Name: "plain-martin", Person: "martin", Template: fstest.MapFS{"laib.yml": {Data: []byte("name: plain\n")}}},
		`person "lena" is not in box.yml`: {Name: "notizbuch-lena", Person: "lena", Template: tpl},
		"services/notizbuch-anna exists":  {Name: "notizbuch-anna", Person: "anna", Template: tpl},
	} {
		if err := add(a); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", a.Name, want, err)
		}
	}
	v2 := fstest.MapFS{}
	for _, f := range []string{"laib.yml", "notizbuch.container", "notizbuch-sync.timer", "notizbuch-sync.service", "values.example.yml"} {
		b, _ := os.ReadFile(filepath.Join("../../testdata/perperson/notizbuch", f))
		v2[f] = &fstest.MapFile{Data: []byte(strings.Replace(string(b), "version: 1", "version: 2", 1))}
	}
	p, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update notizbuch-anna", Edit{Update: &UpdateService{Name: "notizbuch-anna", From: "notizbuch", Template: v2}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings) > 0 {
		t.Errorf("an update of an instance is no conflict: %v", p.Warnings)
	}
	if err := Approve(ctx, repo, inbox, p, "human:core"); err != nil {
		t.Fatal(err)
	}
	if got := read(repo, "notizbuch-anna"); !strings.HasPrefix(got, "name: notizbuch-anna\nperson: anna\nportOffset: 1\n") || !strings.Contains(got, "version: 2") {
		t.Errorf("the update lost the instance:\n%s", got)
	}
}
