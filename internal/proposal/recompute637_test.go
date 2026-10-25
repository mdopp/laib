package proposal

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// #637: a --set whose key someone else changed since it was proposed is not
// re-applied over that change: the intent itself conflicts, and the Inbox
// says why in German.
func TestASetWhoseKeyChangedMeanwhileGoesStale(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	key := "services/whoami/values.yml:IMAGE"
	a, err := CreateFromEdit(ctx, repo, inbox, "ops", "v1.11", Edit{Set: map[string]string{key: "docker.io/traefik/whoami:v1.11"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CreateFromEdit(ctx, repo, inbox, "pi", "v1.12", Edit{Set: map[string]string{key: "docker.io/traefik/whoami:v1.12"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApproveChecked(ctx, repo, inbox, b, "human:core", nil, &Recompute{}); err != nil {
		t.Fatal(err)
	}
	if err := ApproveChecked(ctx, repo, inbox, a, "human:core", nil, &Recompute{}); err == nil {
		t.Fatal("v1.11 overwrote the v1.12 approved after it was proposed")
	}
	a, _ = Get(inbox, a.ID)
	if a.Status != "stale" || !strings.Contains(a.StaleReason, "IMAGE wurde seit dem Vorschlag geändert") || !strings.Contains(a.StaleReason, "v1.12") {
		t.Fatalf("%s: %q", a.Status, a.StaleReason)
	}
	if v, _ := os.ReadFile(filepath.Join(repo, "services/whoami/values.yml")); !strings.Contains(string(v), "v1.12") {
		t.Fatalf("main lost the approved value:\n%s", v)
	}
}

// #637: a template update that no longer merges is computed again from the
// template against the current main — the owner's value set in between
// stays, the template's new keys come too. When the template itself moved
// on since the owner saw the proposal, the approval does not cover it: the
// old one is stale and the new content is filed fresh.
func TestATemplateUpdateIsRecomputedFromTheTemplate(t *testing.T) {
	ctx := context.Background()
	tpl := func(version string) fstest.MapFS {
		return fstest.MapFS{
			"laib.yml":           {Data: []byte("name: echo\ntemplate: {registry: builtin, name: echo, version: " + version + "}\nroutes: []\nsecrets: []\ndata: []\n")},
			"values.example.yml": {Data: []byte("IMAGE: docker.io/echo:1\nOLD: theirs\nNEW: v" + version + "\n")},
			"echo.container":     {Data: []byte("[Container]\nImage=${IMAGE}\nEnvironment=OLD=${OLD}\nEnvironment=NEW=${NEW}\n")},
		}
	}
	install := func(t *testing.T) (string, string, *Proposal) {
		repo, inbox := goldenRepo(t)
		dst := filepath.Join(repo, "services", "echo")
		os.MkdirAll(dst, 0o755)
		os.WriteFile(filepath.Join(dst, "laib.yml"), []byte("name: echo\ntemplate: {registry: builtin, name: echo, version: 1}\nroutes: []\nsecrets: []\ndata: []\n"), 0o644)
		os.WriteFile(filepath.Join(dst, "echo.container"), []byte("[Container]\nImage=${IMAGE}\nEnvironment=OLD=${OLD}\n"), 0o644)
		os.WriteFile(filepath.Join(dst, "values.yml"), []byte("IMAGE: docker.io/echo:2\nOLD: mine\n"), 0o644)
		git(ctx, repo, "add", "-A")
		git(ctx, repo, "commit", "-q", "-m", "echo, installed")
		up, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update echo", Edit{Update: &UpdateService{Name: "echo", From: "echo", Template: tpl("2")}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		// meanwhile the owner adds a value at the end of values.yml, where the update adds NEW
		set, err := CreateFromEdit(ctx, repo, inbox, "ops", "mehr", Edit{Set: map[string]string{"services/echo/values.yml:EXTRA": "x"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := ApproveChecked(ctx, repo, inbox, set, "human:core", nil, nil); err != nil {
			t.Fatal(err)
		}
		return repo, inbox, up
	}

	repo, inbox, up := install(t)
	re := &Recompute{Template: func(string) (fs.FS, error) { return tpl("2"), nil }}
	if err := ApproveChecked(ctx, repo, inbox, up, "human:core", nil, re); err != nil {
		t.Fatalf("the update was not recomputed: %v", err)
	}
	if up, _ = Get(inbox, up.ID); up.Status != "approved" || !up.Recomputed {
		t.Fatalf("%s, recomputed %v", up.Status, up.Recomputed)
	}
	v, _ := os.ReadFile(filepath.Join(repo, "services/echo/values.yml"))
	c, _ := os.ReadFile(filepath.Join(repo, "services/echo/echo.container"))
	if !strings.Contains(string(v), "EXTRA: x") || !strings.Contains(string(v), "NEW: v2") || !strings.Contains(string(c), "NEW=${NEW}") {
		t.Fatalf("not the template on the current main:\n%s\n%s", v, c)
	}
	if out, _ := git(ctx, repo, "rev-list", "--parents", "-n1", up.Merge); len(strings.Fields(out)) != 3 {
		t.Fatalf("the recomputed approve is no merge, rollback reverts it with -m 1: %s", out)
	}

	repo, inbox, up = install(t)
	re = &Recompute{Template: func(string) (fs.FS, error) { return tpl("3"), nil }}
	if err := ApproveChecked(ctx, repo, inbox, up, "human:core", nil, re); err == nil {
		t.Fatal("a newer template than the owner saw was applied on their approval")
	}
	up, _ = Get(inbox, up.ID)
	fresh, err := Get(inbox, up.Refiled)
	if up.Status != "stale" || err != nil || !strings.Contains(up.StaleReason, "geändert seit deiner Freigabe — neu vorgeschlagen als #") || fresh.Status != "open" || fresh.Reason != up.Reason || fresh.Actor != up.Actor {
		t.Fatalf("%s %q → %+v (%v)", up.Status, up.StaleReason, fresh, err)
	}
	if v, _ := os.ReadFile(filepath.Join(repo, "services/echo/values.yml")); strings.Contains(string(v), "NEW") {
		t.Fatalf("main changed without an approval:\n%s", v)
	}
}
