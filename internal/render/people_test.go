package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #177, ADR 0022 §3: box.yml people reach the service that carries
// ${PEOPLE_REV} as render/people/people.json, and a people change restarts
// it. The notify server ships as a .py file next to its unit.
func TestPeopleReachTheServiceThatUsesThem(t *testing.T) {
	repo := t.TempDir()
	w := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(repo, p)), 0o755)
		os.WriteFile(filepath.Join(repo, p), []byte(s), 0o644)
	}
	w("policy.yml", "tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n")
	c := "name: %s\ntemplate: {registry: builtin, name: %s, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {none: test}\n"
	w("services/melder/laib.yml", strings.ReplaceAll(c, "%s", "melder"))
	w("services/melder/melder.container", "[Container]\nImage=x\nEnvironment=PEOPLE_REV=${PEOPLE_REV}\nVolume=/mnt/data/laib/render/people:/people:ro,z\nVolume=./notify.py:/app/notify.py:ro,z\n")
	w("services/melder/notify.py", "print('hi')\n")
	render := func(box string) Output {
		w("box.yml", "domain: x.home\ndataDir: /d\nroles: {}\nhostNetwork: []\n"+box)
		st, err := state.Load(repo)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Render(st, nil)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	a := render("people: {mama: [\"signal:+4917012345678\"]}\n")
	if a["people/people.json"] != `{"mama":["signal:+4917012345678"]}`+"\n" {
		t.Fatalf("people.json: %q (%v)", a["people/people.json"], keys(a))
	}
	if a["quadlet/melder/notify.py"] != "print('hi')\n" {
		t.Fatalf("the .py file goes next to the unit as is: %v", keys(a))
	}
	if strings.Contains(a["quadlet/melder/melder.container"], "${PEOPLE_REV}") {
		t.Fatal("PEOPLE_REV not substituted")
	}
	b := render("people: {mama: [\"telegram:1\"]}\n")
	if a["quadlet/melder/melder.container"] == b["quadlet/melder/melder.container"] {
		t.Fatal("a people change does not touch the unit that carries PEOPLE_REV")
	}
	if e := render(""); e["people/people.json"] != "{}\n" {
		t.Fatalf("no people still renders the file the unit mounts: %q", e["people/people.json"])
	}
}

// A box where no unit carries PEOPLE_REV renders no people file.
func TestNoPeopleOutputWithoutAUser(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := Render(st, nil)
	for k := range out {
		if strings.HasPrefix(k, "people/") {
			t.Fatalf("%s rendered on a box without a people user", k)
		}
	}
}
