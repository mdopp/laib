package proposal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// #511: `template update llama` set full.stops and voiceUnits back to
// voice/*, `template update bote` the owner's trust back to untrusted. A
// line the owner changed on the box survives the update: the box's file,
// the template as it was copied in and the new template merge three ways.
// Where the two change the same line, or a service from before #511 has no
// base, the template wins and the proposal says so in a warning.
func TestTemplateUpdateKeepsTheOwnersLines(t *testing.T) {
	ctx := context.Background()
	unit := "[Container]\nImage=${IMAGE}\n"
	presets := func(stops, voice, thinking string) string {
		return "{\n  \"presets\": [{\"alias\": \"a\", \"thinking\": " + thinking + "}],\n  \"padding\": 1,\n  \"more\": 2,\n  \"full\": {\"stops\": [" + stops + "]},\n  \"voiceUnits\": [" + voice + "]\n}\n"
	}
	hook := func(trust, extra string) string {
		return "#!/bin/sh\necho one\necho two\necho three\necho \"trust: " + trust + "\"\necho four\necho five\n" + extra
	}
	tpl := func(version, presetsJSON, postDeploy string) fstest.MapFS {
		return fstest.MapFS{
			"laib.yml":           {Data: []byte("name: lm\ntemplate: {registry: builtin, name: lm, version: " + version + "}\nroutes: []\nsecrets: []\ndata: []\n")},
			"values.example.yml": {Data: []byte("IMAGE: docker.io/echo:1\n")},
			"lm.container":       {Data: []byte(unit)},
			"presets.json":       {Data: []byte(presetsJSON)},
			"post-deploy":        {Data: []byte(postDeploy)},
		}
	}
	v1 := tpl("1", presets(`"voice/whisper"`, `"voice/whisper"`, "true"), hook("untrusted", ""))
	read := func(repo, f string) string {
		b, _ := os.ReadFile(filepath.Join(repo, "services", "lm", f))
		return string(b)
	}
	setup := func(t *testing.T, ownPresets, ownHook string) (string, string) {
		repo, inbox := goldenRepo(t)
		p, err := CreateFromEdit(ctx, repo, inbox, "laib", "add lm", Edit{Add: &AddService{Name: "lm", Template: v1}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := Approve(ctx, repo, inbox, p, "human:core"); err != nil {
			t.Fatal(err)
		}
		if got := read(repo, ".template/presets.json"); got != string(v1["presets.json"].Data) {
			t.Fatalf("add records no base: %q", got)
		}
		// the owner decides on the box
		os.WriteFile(filepath.Join(repo, "services", "lm", "presets.json"), []byte(ownPresets), 0o644)
		os.WriteFile(filepath.Join(repo, "services", "lm", "post-deploy"), []byte(ownHook), 0o755)
		git(ctx, repo, "commit", "-qam", "owner: no voice yet, the Bote trusted")
		return repo, inbox
	}
	update := func(t *testing.T, repo, inbox string, tpl fstest.MapFS) *Proposal {
		p, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update lm", Edit{Update: &UpdateService{Name: "lm", Template: tpl}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := Approve(ctx, repo, inbox, p, "human:core"); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("the owner's lines survive", func(t *testing.T) {
		repo, inbox := setup(t, presets("", "", "true"), hook("full", ""))
		v2 := tpl("2", presets(`"voice/whisper"`, `"voice/whisper"`, "false"), hook("untrusted", "echo new\n"))
		p := update(t, repo, inbox, v2)
		if len(p.Warnings) != 0 {
			t.Errorf("a clean merge warns: %v", p.Warnings)
		}
		if got, want := read(repo, "presets.json"), presets("", "", "false"); got != want {
			t.Errorf("presets.json:\n%s\nwant\n%s", got, want)
		}
		if got, want := read(repo, "post-deploy"), hook("full", "echo new\n"); got != want {
			t.Errorf("post-deploy:\n%s\nwant\n%s", got, want)
		}
		if info, _ := os.Stat(filepath.Join(repo, "services", "lm", "post-deploy")); info.Mode()&0o111 == 0 {
			t.Error("the merged hook lost its executable bit")
		}
		if got := read(repo, ".template/post-deploy"); got != string(v2["post-deploy"].Data) {
			t.Errorf("the base is not the new template: %q", got)
		}
	})

	t.Run("the same line changed on both sides", func(t *testing.T) {
		repo, inbox := setup(t, presets("", "", "true"), hook("full", ""))
		p := update(t, repo, inbox, tpl("2", presets(`"model/embed"`, `"voice/whisper"`, "true"), hook("untrusted", "")))
		if w := strings.Join(p.Warnings, " "); !strings.Contains(w, "presets.json") || !strings.Contains(w, "dieselben Zeilen") || strings.Contains(w, "post-deploy") {
			t.Errorf("the conflict is not named, or the clean file is: %v", p.Warnings)
		}
		if got := read(repo, "presets.json"); !strings.Contains(got, `"model/embed"`) {
			t.Errorf("on a conflict the template wins: %s", got)
		}
		if got := read(repo, "post-deploy"); got != hook("full", "") {
			t.Errorf("a file the template did not change is the owner's: %s", got)
		}
	})

	t.Run("a service from before #511", func(t *testing.T) {
		repo, inbox := setup(t, presets("", "", "true"), hook("full", ""))
		os.RemoveAll(filepath.Join(repo, "services", "lm", ".template"))
		git(ctx, repo, "commit", "-qam", "installed before #511")
		if _, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update lm", Edit{Update: &UpdateService{Name: "lm", Template: tpl("1", presets("", "", "true"), hook("full", ""))}}, nil); err == nil || !strings.Contains(err.Error(), "nothing to propose") {
			t.Fatalf("recording the base alone is no proposal: %v", err)
		}
		p := update(t, repo, inbox, tpl("2", presets(`"voice/whisper"`, `"voice/whisper"`, "false"), hook("untrusted", "")))
		if w := strings.Join(p.Warnings, " "); !strings.Contains(w, "post-deploy, presets.json") || !strings.Contains(w, "vor #511") {
			t.Errorf("without a base the overwritten files must be named: %v", p.Warnings)
		}
		if got := read(repo, ".template/presets.json"); got == "" {
			t.Error("the update records no base for the next one")
		}
	})
}
