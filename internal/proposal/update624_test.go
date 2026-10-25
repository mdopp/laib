package proposal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// ADR 0027 (#624, part 2): the llama template's lease moved from a host
// unit (llama-lease.service) into a container (llama-lease.container).
// A template update takes the host unit out of the service, as it does a
// container the template no longer ships, so the box runs only the new one.
func TestTemplateUpdateRemovesAHostUnitTheTemplateNoLongerShips(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	dst := filepath.Join(repo, "services", "lm")
	os.MkdirAll(dst, 0o755)
	os.WriteFile(filepath.Join(dst, "laib.yml"), []byte("name: lm\ntemplate: {registry: builtin, name: lm, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {none: a stub}\n"), 0o644)
	os.WriteFile(filepath.Join(dst, "lm.container"), []byte("[Container]\nImage=x\n"), 0o644)
	os.WriteFile(filepath.Join(dst, "lm-lease.service"), []byte("[Service]\nExecStart=/var/opt/laib/laib-lease\n\n[Install]\nWantedBy=default.target\n"), 0o644)
	os.WriteFile(filepath.Join(dst, "values.yml"), []byte("{}\n"), 0o644)
	git(ctx, repo, "add", "-A")
	git(ctx, repo, "commit", "-q", "-m", "lm, installed")
	tpl := fstest.MapFS{
		"laib.yml":           {Data: []byte("name: lm\ntemplate: {registry: builtin, name: lm, version: 2}\nroutes: []\nsecrets: []\ndata: []\nhealth: {none: a stub}\n")},
		"lm.container":       {Data: []byte("[Container]\nImage=x\n")},
		"lm-lease.container": {Data: []byte("[Container]\nImage=ghcr.io/mdopp/laib-lease:latest\n")},
	}
	p, err := CreateFromEdit(ctx, repo, inbox, "human:core", "template update lm", Edit{Update: &UpdateService{Name: "lm", Template: tpl}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Approve(ctx, repo, inbox, p, "human:core"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "lm-lease.service")); err == nil {
		t.Fatal("the host unit the template dropped is still in the service")
	}
	if _, err := os.Stat(filepath.Join(dst, "lm-lease.container")); err != nil {
		t.Fatal("the container that replaces it is not there")
	}
}
