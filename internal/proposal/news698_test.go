package proposal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// #698, #119–#122 on the box (v0.34.0): template updates whose only change
// was the new news file, each a card „… ändert sich nichts Sichtbares“. A
// diff of news alone changes nothing that runs: the box takes it over on
// main, as bookkeeping, and files no proposal.
func TestANewsOnlyTemplateUpdateIsTakenOverWithoutAProposal(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	dst := filepath.Join(repo, "services", "echo")
	contract := "name: echo\ntemplate: {registry: builtin, name: echo, version: 1}\nroutes: []\nsecrets: []\ndata: []\n"
	unit := "[Container]\nImage=docker.io/echo:1\n"
	os.MkdirAll(filepath.Join(dst, ".template"), 0o755)
	for _, d := range []string{dst, filepath.Join(dst, ".template")} {
		os.WriteFile(filepath.Join(d, "laib.yml"), []byte(contract), 0o644)
		os.WriteFile(filepath.Join(d, "echo.container"), []byte(unit), 0o644)
	}
	os.WriteFile(filepath.Join(dst, "values.yml"), []byte("{}\n"), 0o644)
	git(ctx, repo, "add", "-A")
	git(ctx, repo, "commit", "-q", "-m", "echo, installed")
	news := "# what each change brings\nEcho spricht leiser\n"
	tpl := fstest.MapFS{"laib.yml": {Data: []byte(contract)}, "echo.container": {Data: []byte(unit)}, "news": {Data: []byte(news)}}

	_, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update echo", Edit{Update: &UpdateService{Name: "echo", From: "echo", Template: tpl}}, nil)
	if err == nil || !strings.Contains(err.Error(), "nothing to propose") {
		t.Fatalf("news alone is no proposal: %v", err)
	}
	if open, _ := List(inbox, true); len(open) > 0 {
		t.Errorf("the Inbox got %d proposals", len(open))
	}
	for _, f := range []string{"news", ".template/news"} {
		if b, _ := os.ReadFile(filepath.Join(dst, f)); string(b) != news {
			t.Errorf("services/echo/%s is %q, want the template's news", f, b)
		}
	}
	if st, _ := git(ctx, repo, "status", "--porcelain"); st != "" {
		t.Errorf("the news is not committed: %s", st)
	}
	if files, _ := git(ctx, repo, "show", "--name-only", "--format=", "HEAD"); strings.TrimSpace(files) != "services/echo/.template/news\nservices/echo/news" {
		t.Errorf("the commit carries more than the news: %q", files)
	}
	if b, _ := git(ctx, repo, "branch", "--list", "proposal/*"); strings.TrimSpace(b) != "" {
		t.Errorf("a proposal branch is left: %s", b)
	}

	// anything that runs keeps the owner's gate, the news rides along
	tpl["echo.container"] = &fstest.MapFile{Data: []byte("[Container]\nImage=docker.io/echo:2\n")}
	tpl["news"] = &fstest.MapFile{Data: []byte("# what each change brings\nEcho antwortet schneller\nEcho spricht leiser\n")}
	p, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update echo", Edit{Update: &UpdateService{Name: "echo", From: "echo", Template: tpl}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.News) != 1 || p.News[0] != "Echo antwortet schneller" {
		t.Errorf("the next update reads only its own news: %q", p.News)
	}
}

// A card that carries news never also says nothing visible changes.
func TestACardWithNewsDoesNotSayNothingVisibleChanges(t *testing.T) {
	p := Proposal{ID: 120, Actor: "laib", Reason: "template update haus", Kind: "template-update", Class: "apply",
		Files: []string{"services/haus/haus.container", "services/haus/news"}, News: []string{"Haus darf für dich Podcasts abonnieren"}}
	c := Card(&p, "dopp.cloud")
	if s := strings.Join(c.Impact, " "); strings.Contains(s, "nichts Sichtbares") || !strings.Contains(s, "was oben steht") {
		t.Errorf("impact contradicts the headline: %v", c.Impact)
	}
}
