package proposal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func goldenRepo(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	repo := filepath.Join(home, "state")
	src := "../../testdata/golden/basic/state"
	filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(repo, rel), 0o755)
		}
		b, _ := os.ReadFile(p)
		return os.WriteFile(filepath.Join(repo, rel), b, 0o644)
	})
	if err := EnsureRepo(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	return repo, filepath.Join(home, "inbox")
}

func TestSetKeepsSecretsAndLoads(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	p, err := CreateFromEdit(ctx, repo, inbox, "ops", "newer image", Edit{Set: map[string]string{"services/whoami/values.yml:IMAGE": "docker.io/traefik/whoami:v1.11"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := Diff(ctx, repo, p)
	if !strings.Contains(d, "+IMAGE: docker.io/traefik/whoami:v1.11") || !strings.Contains(d, "-IMAGE: docker.io/traefik/whoami:v1.10") {
		t.Fatal(d)
	}
	if strings.Contains(d, "+TOKEN") || strings.Contains(d, "-TOKEN") {
		t.Fatal("untouched secret line changed:\n" + d)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "services/whoami/values.yml")); !strings.Contains(string(b), "v1.10") {
		t.Fatal("main checkout changed")
	}
}

func TestPatchThatBreaksTheStateIsRefused(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	bad := "--- a/services/whoami/laib.yml\n+++ b/services/whoami/laib.yml\n@@ -1 +1 @@\n-name: whoami\n+name: nope\n"
	if _, err := CreateFromEdit(ctx, repo, inbox, "ops", "break it", Edit{Patch: bad}, nil); err == nil || !strings.Contains(err.Error(), "does not load") {
		t.Fatalf("accepted a broken state: %v", err)
	}
	out, _ := git(ctx, repo, "branch", "--list", "proposal/*")
	if strings.TrimSpace(out) != "" {
		t.Fatal("branch left behind: " + out)
	}
	if _, err := os.Stat(filepath.Join(inbox, "1.json")); err == nil {
		t.Fatal("inbox entry left behind")
	}
}

func TestAddServiceFromTemplate(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	tpl := fstest.MapFS{
		"laib.yml":           {Data: []byte("name: echo\ntemplate: {registry: builtin, name: echo, version: 1}\nroutes: [{host: echo, port: 8081, exposure: internal, auth: one_factor}]\nsecrets: []\ndata: []\nports: [{port: 8081, lan: false}]\n")},
		"values.example.yml": {Data: []byte("IMAGE: x\n")},
		"echo.container":     {Data: []byte("[Container]\nImage=${IMAGE}\n")},
	}
	p, err := CreateFromEdit(ctx, repo, inbox, "ops", "echo for the kids", Edit{Add: &AddService{Name: "echo2", Template: tpl, Values: map[string]string{"IMAGE": "docker.io/echo:1"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := Diff(ctx, repo, p)
	for _, want := range []string{"services/echo2/laib.yml", "name: echo2", "IMAGE: docker.io/echo:1", "services/echo2/echo.container"} {
		if !strings.Contains(d, want) {
			t.Fatalf("missing %q in\n%s", want, d)
		}
	}
	if strings.Contains(d, "values.example.yml") {
		t.Fatal("example file copied as is")
	}
}

func TestAddWiresRolesAndCarveOut(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	// drop the roles so the add has to wire them
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: example.home\ndataDir: /mnt/data/stacks\nlanIp: 192.168.1.10\nroles: {}\ngpu: {present: false}\n"), 0o644)
	os.RemoveAll(filepath.Join(repo, "services", "auth"))
	os.RemoveAll(filepath.Join(repo, "services", "caddy"))
	os.RemoveAll(filepath.Join(repo, "services", "whoami"))
	git(ctx, repo, "add", "-A")
	git(ctx, repo, "commit", "-q", "-m", "bare")
	tpl := fstest.MapFS{
		"laib.yml":           {Data: []byte("name: dnsbox\nprovides: [dns]\nhostNetwork: true\nroutes: []\nsecrets: []\ndata: []\nports: []\n")},
		"values.example.yml": {Data: []byte("IMAGE: x\n")},
		"dnsbox.container":   {Data: []byte("[Container]\nImage=${IMAGE}\nNetwork=host\n")},
	}
	// an uncommitted shell edit in the main checkout must not block the merge
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: example.home\ndataDir: /mnt/data/stacks\nlanIp: 192.168.1.10\nroles: {}\ngpu: {present: false}\ndns: {provider: cloudflare}\n"), 0o644)
	p, err := CreateFromEdit(ctx, repo, inbox, "human:core", "dns", Edit{Add: &AddService{Name: "dnsbox", Template: tpl, Values: map[string]string{}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Approve(ctx, repo, inbox, p, "human:core"); err != nil {
		t.Fatal("a human may approve their own --add:", err)
	}
	b, _ := os.ReadFile(filepath.Join(repo, "box.yml"))
	if !strings.Contains(string(b), "dns: dnsbox") || !strings.Contains(string(b), "- dnsbox") || !strings.Contains(string(b), "provider: cloudflare") {
		t.Fatalf("box.yml not wired or the shell edit lost:\n%s", b)
	}
}

// Both hook phases must land executable: only post-deploy did, and apply
// skipped a 0644 pre-start in silence — Authelia started without its
// configuration.yml on the first VM run of #70 (#36).
func TestAddCopiesBothHooksExecutable(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	tpl := fstest.MapFS{
		"laib.yml":           {Data: []byte("name: hooked\ntemplate: {registry: builtin, name: hooked, version: 1}\nroutes: []\nsecrets: []\ndata: []\n")},
		"values.example.yml": {Data: []byte("IMAGE: x\n")},
		"hooked.container":   {Data: []byte("[Container]\nImage=${IMAGE}\n")},
		"pre-start":          {Data: []byte("#!/bin/sh\necho pre\n")},
		"post-deploy":        {Data: []byte("#!/bin/sh\necho post\n")},
	}
	p, err := CreateFromEdit(ctx, repo, inbox, "human:test", "hooked", Edit{Add: &AddService{Name: "hooked", Template: tpl, Values: map[string]string{}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Approve(ctx, repo, inbox, p, "human:other"); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"pre-start", "post-deploy"} {
		info, err := os.Stat(filepath.Join(repo, "services", "hooked", h))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&0o111 == 0 {
			t.Fatalf("%s is not executable (%v): apply would skip it", h, info.Mode())
		}
	}
}
