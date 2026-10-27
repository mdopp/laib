package proposal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// #785: a card names a built-in service the way the Box page does — the
// template's title — and a service without one by its id in capitals.
func TestACardNamesTheServiceByItsTemplatesTitle(t *testing.T) {
	if Named("media") != "Jellyfin" || Named("file-share") != "Netzlaufwerk" || Named("echo") != "Echo" {
		t.Errorf("media %q, file-share %q, echo %q", Named("media"), Named("file-share"), Named("echo"))
	}
	p := Proposal{ID: 1, Actor: "laib", Reason: "restart", Kind: "tree", Class: "apply", Files: []string{"services/media/values.yml"},
		Changes: []Change{{Op: "~", Service: "media", Key: "LANG", Old: "en", New: "de"}}}
	if s := cardText(Card(&p, "x.home")); !strings.Contains(s, "Jellyfin") || strings.Contains(s, "Media") {
		t.Errorf("the card does not say Jellyfin:\n%s", s)
	}
}

// #785, the shape of #698: a template update whose laib.yml differs only in
// its title or beschreibung changes nothing that runs; the box takes it
// over on main, files no proposal, and the Box page shows the new name.
func TestANameOnlyTemplateUpdateIsTakenOverWithoutAProposal(t *testing.T) {
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
	named := "name: echo\ntitle: Echo\nbeschreibung: Antwortet, was man sagt\n" + strings.TrimPrefix(contract, "name: echo\n")
	tpl := fstest.MapFS{"laib.yml": {Data: []byte(named)}, "echo.container": {Data: []byte(unit)}}

	_, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update echo", Edit{Update: &UpdateService{Name: "echo", From: "echo", Template: tpl}}, nil)
	if err == nil || !strings.Contains(err.Error(), "nothing to propose") {
		t.Fatalf("a new name alone is no proposal: %v", err)
	}
	if open, _ := List(inbox, true); len(open) > 0 {
		t.Errorf("the Inbox got %d proposals", len(open))
	}
	for _, f := range []string{"laib.yml", ".template/laib.yml"} {
		if b, _ := os.ReadFile(filepath.Join(dst, f)); string(b) != named {
			t.Errorf("services/echo/%s is %q, want the template's", f, b)
		}
	}
	if st, _ := git(ctx, repo, "status", "--porcelain"); st != "" {
		t.Errorf("the name is not committed: %s", st)
	}

	// a laib.yml change beyond the name keeps the owner's gate
	tpl["laib.yml"] = &fstest.MapFile{Data: []byte(strings.Replace(named, "data: []", "data: []\nafter: [auth]", 1))}
	if _, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update echo", Edit{Update: &UpdateService{Name: "echo", From: "echo", Template: tpl}}, nil); err != nil {
		t.Fatalf("a real change is a proposal: %v", err)
	}
}
