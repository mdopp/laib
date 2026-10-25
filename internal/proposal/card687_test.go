package proposal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// #687, the card the owner got (#114 on the box, 2026-10-25): a template
// update of stimme. Headline „Stimme: HA_TOKEN wird ein Geheimnis
// (STIMME_HA_TOKEN)“, why „Laib schlägt vor: „template update stimme““,
// five „Achtung: …“ lines and 49 code lines behind Details. The owner:
// „häufig noch technisch. Und sehr lang.“ What it should have said: what
// the update brings the household, from the template's news; the review's
// reasons in one plain sentence, the technical part behind Details, capped.
func TestTheTemplateUpdateCardSaysWhatItBringsInFiveLines(t *testing.T) {
	var lines []string
	for i := range 49 {
		lines = append(lines, fmt.Sprintf("ändert X%d=%d in stimme/stimme-names.service, was Laib nicht einschätzen kann", i, i))
	}
	p := Proposal{ID: 114, Actor: "laib", Reason: "template update stimme", Kind: "template-update", Class: "human",
		Files: []string{"services/stimme/laib.yml", "services/stimme/news", "services/stimme/stimme-names.py", "services/stimme/stimme-names.service", "services/stimme/stimme-names.timer", "services/stimme/stimme-whisper.container", "services/stimme/values.yml"},
		Reasons: []string{
			"erwartet ein Geheimnis von außen (STIMME_HA_TOKEN in stimme)",
			"services/stimme/stimme-names.service läuft direkt auf der Box, nicht im Container (Host-Unit)",
			"services/stimme/stimme-names.timer läuft direkt auf der Box, nicht im Container (Host-Unit)",
			"führt einen Befehl direkt auf der Box aus (ExecStartPre in stimme/stimme-whisper.container)",
			"ändert 49 Zeilen in stimme/stimme-names.service (eine Datei, die Laib nicht einschätzen kann)",
		},
		Changes: []Change{{Op: "~", Service: "stimme", Key: "HA_TOKEN", New: "!secret STIMME_HA_TOKEN"}},
		Lines:   lines,
		News:    []string{"Joshua lernt die Namen deiner Geräte"},
	}
	c := Card(&p, "dopp.cloud")
	if c.Summary != "Joshua lernt die Namen deiner Geräte" {
		t.Errorf("the headline is what the update brings, got %q", c.Summary)
	}
	if !strings.Contains(c.Why, "Joshua lernt die Namen deiner Geräte") || !strings.Contains(c.Why, "Laib") || !strings.Contains(c.Why, "Stimme") {
		t.Errorf("why: who proposes the new template of which service, and what it brings: %q", c.Why)
	}
	visible := []string{c.Summary, c.Reversible, c.Why, c.RiskWhy}
	for _, l := range c.Changes {
		visible = append(visible, l.Text)
	}
	visible = append(visible, c.Impact...)
	for _, s := range visible {
		for _, word := range []string{"Host-Unit", "ExecStartPre", "Zeilen in", "HA_TOKEN", ".service", ".container", ".py", "Achtung", "template update", "Geheimnis von außen"} {
			if strings.Contains(s, word) {
				t.Errorf("visible %q carries the technical %q", s, word)
			}
		}
	}
	if n := 1 + len(c.Changes) + len(c.Impact) + 3; n > 6 {
		t.Errorf("the visible card is %d lines, want about five: %+v", n, c)
	}
	if !strings.Contains(strings.Join(c.Impact, " "), "direkt auf der Box") {
		t.Errorf("the reasons in plain words, as one sentence: %v", c.Impact)
	}
	for _, want := range []string{"ExecStartPre", "Host-Unit", "STIMME_HA_TOKEN", "HA_TOKEN", "X0=0", "und 37 weitere"} {
		if !strings.Contains(c.Details, want) {
			t.Errorf("Details lack %q: %s", want, c.Details)
		}
	}
	if strings.Contains(c.Details, "X12=12") {
		t.Errorf("the line list behind Details is capped: %s", c.Details)
	}

	// a template without news: no quoted command, the service's new template
	p.News = nil
	if c := Card(&p, "dopp.cloud"); strings.Contains(c.Why, "template update") || !strings.Contains(c.Why, "neue Vorlage von Stimme") {
		t.Errorf("why without news: %q", c.Why)
	}
}

// #687: the sentence's home is the template's news file, one line per
// change, newest first. A template update reads the lines the box's copy
// does not have yet — per change, not per template forever — with the
// house persona's name filled in.
func TestATemplateUpdateReadsOnlyTheNewNews(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	dst := filepath.Join(repo, "services", "echo")
	os.MkdirAll(dst, 0o755)
	contract := "name: echo\ntemplate: {registry: builtin, name: echo, version: 1}\nroutes: []\nsecrets: []\ndata: []\n"
	os.WriteFile(filepath.Join(dst, "laib.yml"), []byte(contract), 0o644)
	os.WriteFile(filepath.Join(dst, "echo.container"), []byte("[Container]\nImage=docker.io/echo:1\n"), 0o644)
	os.WriteFile(filepath.Join(dst, "values.yml"), []byte("{}\n"), 0o644)
	os.WriteFile(filepath.Join(dst, "news"), []byte("# what each change brings\nEcho antwortet schneller\n"), 0o644)
	git(ctx, repo, "add", "-A")
	git(ctx, repo, "commit", "-q", "-m", "echo, installed")
	tpl := fstest.MapFS{
		"laib.yml":       {Data: []byte(contract)},
		"echo.container": {Data: []byte("[Container]\nImage=docker.io/echo:2\n")},
		"news":           {Data: []byte("# what each change brings\n${PERSONA_DISPLAY} hört Echo zu\nEcho spricht leiser\n\nEcho antwortet schneller\n")},
	}
	p, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update echo", Edit{Update: &UpdateService{Name: "echo", From: "echo", Template: tpl}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Joshua hört Echo zu", "Echo spricht leiser"}; !slices.Equal(p.News, want) {
		t.Errorf("news %q, want only what the box does not have, %q", p.News, want)
	}
	if !slices.Contains(p.Files, "services/echo/news") {
		t.Errorf("the box keeps the template's news, so the next update reads only its own: %v", p.Files)
	}
}
