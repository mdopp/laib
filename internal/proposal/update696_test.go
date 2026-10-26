package proposal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #696 on the box: a haus from before haus#138 has DISCOGS_TOKEN: !secret
// HAUS_DISCOGS_TOKEN. The new template declares HAUS_DISCOGS_KEY and
// _SECRET instead and its example has no DISCOGS_TOKEN, so the kept key
// referenced an undeclared secret and the update never loaded. A key the
// example no longer has whose !secret the new contract no longer declares
// goes; the owner's own plain value and a !secret still declared stay.
func TestTemplateUpdateDropsAValueWhoseSecretTheTemplateRetired(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	box, _ := os.ReadFile(filepath.Join(repo, "box.yml"))
	os.WriteFile(filepath.Join(repo, "box.yml"), append(box, []byte("laib: {listen: 0.0.0.0}\n")...), 0o644)
	src := filepath.Join("..", "..", "templates", "haus")
	dst := filepath.Join(repo, "services", "haus")
	os.MkdirAll(dst, 0o755)
	entries, _ := os.ReadDir(src)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(src, e.Name()))
		name, s := e.Name(), string(b)
		switch name {
		case "README.md":
			continue
		case "values.example.yml": // the haus from before haus#138, plus two of the owner's own
			name = "values.yml"
			s = strings.Replace(s, "DISCOGS_KEY: !secret HAUS_DISCOGS_KEY", "DISCOGS_TOKEN: !secret HAUS_DISCOGS_TOKEN", 1)
			s = strings.Replace(s, "DISCOGS_SECRET: !secret HAUS_DISCOGS_SECRET", "", 1)
			s += "MINE: hallo # the owner's\nPROXY_TOO: !secret HAUS_PROXY_SECRET\n"
		case "laib.yml":
			s = strings.Replace(s, "name: HAUS_DISCOGS_KEY", "name: HAUS_DISCOGS_TOKEN", 1)
			s = strings.Replace(s, "name: HAUS_DISCOGS_SECRET", "name: HAUS_DISCOGS_OTHER", 1)
		case "haus.container":
			s = strings.Replace(s, "Environment=HAUS_DISCOGS_KEY=${DISCOGS_KEY}\nEnvironment=HAUS_DISCOGS_SECRET=${DISCOGS_SECRET}", "Environment=HAUS_DISCOGS_TOKEN=${DISCOGS_TOKEN}", 1)
		}
		os.WriteFile(filepath.Join(dst, name), []byte(s), 0o644)
	}
	if _, err := state.Load(repo); err != nil {
		t.Fatalf("the haus from before haus#138 must load: %v", err)
	}
	git(ctx, repo, "add", "-A")
	git(ctx, repo, "commit", "-q", "-m", "haus, from before haus#138")
	p, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update haus", Edit{Update: &UpdateService{Name: "haus", Template: os.DirFS(src)}}, nil)
	if err != nil {
		t.Fatalf("the haus template update must propose on a box with HAUS_DISCOGS_TOKEN: %v", err)
	}
	if err := Approve(ctx, repo, inbox, p, "human:core"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "values.yml"))
	for _, want := range []string{"DISCOGS_KEY: !secret HAUS_DISCOGS_KEY", "DISCOGS_SECRET: !secret HAUS_DISCOGS_SECRET", "MINE: hallo # the owner's", "PROXY_TOO: !secret HAUS_PROXY_SECRET"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("values.yml lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "DISCOGS_TOKEN") {
		t.Errorf("values.yml keeps the retired secret's key:\n%s", b)
	}
}

// The card says it in Details: a value a template update takes away.
func TestACardSaysAValueTheTemplateTookAway(t *testing.T) {
	p := &Proposal{Files: []string{"services/haus/values.yml"}, News: []string{"Haus kann mehr"},
		Changes: []Change{{Op: "-", Service: "haus", Key: "DISCOGS_TOKEN", Old: "!secret HAUS_DISCOGS_TOKEN"}}}
	if d := Card(p, "example.org"); !strings.Contains(d.Details, "Haus: DISCOGS_TOKEN entfällt, die Vorlage braucht es nicht mehr (bisher ein Geheimnis (HAUS_DISCOGS_TOKEN))") {
		t.Errorf("Details does not say the value goes: %s", d.Details)
	}
}

// newValues' drop at its edges: only a !secret the contract no longer
// declares and the example no longer has; never a plain value, never
// without the contract.
func TestNewValuesDropsOnlyARetiredSecret(t *testing.T) {
	for _, c := range []struct {
		have, example string
		declared      map[string]bool
		want          string
	}{
		{"A: 1\nOLD: !secret GONE # x\nB: 2\n", "A: 3\n", map[string]bool{}, "A: 1\nB: 2\n"},
		{"A: 1\nOLD: !secret GONE\n", "A: 3\nN: !secret NEW\n", map[string]bool{"NEW": true}, "A: 1\nN: !secret NEW\n"},
		{"OLD: GONE\n", "A: 3\n", map[string]bool{}, "OLD: GONE\nA: 3\n"},                             // a plain value stays
		{"OLD: !secret KEPT\n", "A: 3\n", map[string]bool{"KEPT": true}, "OLD: !secret KEPT\nA: 3\n"}, // still declared
		{"OLD: !secret GONE\n", "OLD: !secret GONE\n", map[string]bool{}, "OLD: !secret GONE\n"},      // the example has it: the loader says why
		{"OLD: !secret GONE\n", "A: 3\n", nil, "OLD: !secret GONE\nA: 3\n"},                           // no contract read: nothing goes
	} {
		path := filepath.Join(t.TempDir(), "values.yml")
		os.WriteFile(path, []byte(c.have), 0o644)
		err := newValues(path, []byte(c.example), c.declared)
		if got, _ := os.ReadFile(path); err != nil || string(got) != c.want {
			t.Errorf("%q + %q → %q, %v; want %q", c.have, c.example, got, err, c.want)
		}
	}
}
