package proposal

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mdopp/laib/internal/state"
)

// On the box 2026-11-09: the scan from before ADR 0031 has HAUSHALT_TOKEN:
// !secret HAUS_HAUSHALT_TOKEN. The new template declares
// HOMES_HAUSHALT_TOKEN with was: HAUS_HAUSHALT_TOKEN, so the kept reference
// named an undeclared secret and `laib template update scan` never loaded;
// a values-only proposal failed the other way. The update renames the
// reference with the secret (laib#1021), in the same proposal.
func TestTemplateUpdateRenamesAReferenceToASecretsFormerName(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	box, _ := os.ReadFile(filepath.Join(repo, "box.yml"))
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte(strings.Replace(string(box), "hostNetwork: [caddy]", "hostNetwork: [caddy, scan]", 1)), 0o644)
	src := filepath.Join("..", "..", "templates", "scan")
	dst := filepath.Join(repo, "services", "scan")
	os.MkdirAll(dst, 0o755)
	tmpl := fstest.MapFS{} // the scan template without its after: (paperless and file-share are not in the golden state)
	entries, _ := os.ReadDir(src)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(src, e.Name()))
		b = regexp.MustCompile(`(?m)^after:.*$`).ReplaceAll(b, nil)
		tmpl[e.Name()] = &fstest.MapFile{Data: b}
		name, s := e.Name(), string(b)
		switch name {
		case "README.md":
			continue
		case "values.example.yml": // the scan from before ADR 0031
			name = "values.yml"
			s = strings.Replace(s, "HAUSHALT_TOKEN: !secret HOMES_HAUSHALT_TOKEN", "HAUSHALT_TOKEN: !secret HAUS_HAUSHALT_TOKEN # Homes' bearer", 1)
		case "laib.yml":
			s = strings.Replace(s, "name: HOMES_HAUSHALT_TOKEN", "name: HAUS_HAUSHALT_TOKEN", 1)
			s = strings.Replace(s, "was: HAUS_HAUSHALT_TOKEN", "", 1)
		}
		os.WriteFile(filepath.Join(dst, name), []byte(s), 0o644)
	}
	if _, err := state.Load(repo); err != nil {
		t.Fatalf("the scan from before ADR 0031 must load: %v", err)
	}
	git(ctx, repo, "add", "-A")
	git(ctx, repo, "commit", "-q", "-m", "scan, from before ADR 0031")
	p, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update scan", Edit{Update: &UpdateService{Name: "scan", Template: tmpl}}, nil)
	if err != nil {
		t.Fatalf("the scan template update must propose on a box with HAUS_HAUSHALT_TOKEN: %v", err)
	}
	if err := Approve(ctx, repo, inbox, p, "human:core"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "values.yml"))
	if !strings.Contains(string(b), "\nHAUSHALT_TOKEN: !secret HOMES_HAUSHALT_TOKEN # Homes' bearer\n") || strings.Contains(string(b), "HAUS_HAUSHALT_TOKEN") {
		t.Errorf("values.yml does not name the renamed secret:\n%s", b)
	}
}
