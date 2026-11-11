package proposal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// #1166: a template update that only writes reasons into laib.yml's
// exceptions: block changes nothing that runs; the box takes it over as it
// does a new name (#785), with no card for the owner. A change after the
// block, or a unit's, keeps the gate.
func TestAnExceptionsOnlyTemplateUpdateIsTakenOverWithoutAProposal(t *testing.T) {
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
	reasons := contract + "exceptions:     # #1166\n  echo.container:\n\n    unhardened: \"unknown, kept until measured\"\n"
	tpl := fstest.MapFS{"laib.yml": {Data: []byte(reasons)}, "echo.container": {Data: []byte(unit)}}

	_, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update echo", Edit{Update: &UpdateService{Name: "echo", From: "echo", Template: tpl}}, nil)
	if err == nil || !strings.Contains(err.Error(), "nothing to propose") {
		t.Fatalf("reasons alone are no proposal: %v", err)
	}
	if open, _ := List(inbox, true); len(open) > 0 {
		t.Errorf("the Inbox got %d proposals", len(open))
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "laib.yml")); string(b) != reasons {
		t.Errorf("services/echo/laib.yml is %q, want the template's", b)
	}

	// a key after the block is no reason: the owner's gate
	tpl["laib.yml"] = &fstest.MapFile{Data: []byte(reasons + "after: [auth]\n")}
	if _, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update echo", Edit{Update: &UpdateService{Name: "echo", From: "echo", Template: tpl}}, nil); err != nil {
		t.Fatalf("a real change is a proposal: %v", err)
	}
}
