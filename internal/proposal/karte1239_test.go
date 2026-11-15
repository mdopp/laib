package proposal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// #1239: a pure comment change in the templates would have made 13 cards,
// each with a restart. A template update whose units and laib.yml run as
// before files nothing and leaves the box's copy as it is; a change that
// runs differently still comes as a card.
// Ablauf L-56
func TestATemplateUpdateOfOnlyCommentsFilesNothingAndChangesNothing(t *testing.T) {
	ctx := context.Background()
	repo, inbox := goldenRepo(t)
	dst := filepath.Join(repo, "services", "echo")
	contract := "name: echo\ntemplate: {registry: builtin, name: echo, version: 1}\nroutes: []\nsecrets: []\ndata: []\n"
	unit := "# Echo, as the template has it\n[Container]\nImage=docker.io/echo:1\n"
	os.MkdirAll(filepath.Join(dst, ".template"), 0o755)
	for _, d := range []string{dst, filepath.Join(dst, ".template")} {
		os.WriteFile(filepath.Join(d, "laib.yml"), []byte(contract), 0o644)
		os.WriteFile(filepath.Join(d, "echo.container"), []byte(unit), 0o644)
	}
	os.WriteFile(filepath.Join(dst, "values.yml"), []byte("{}\n"), 0o644)
	git(ctx, repo, "add", "-A")
	git(ctx, repo, "commit", "-q", "-m", "echo, installed")
	head, _ := git(ctx, repo, "rev-parse", "HEAD")

	tpl := fstest.MapFS{
		"laib.yml":       {Data: []byte("# the contract\nname: echo\ntemplate: {registry: builtin, name: echo, version: 1}\nroutes: []   # none\nsecrets: []\ndata: []\n")},
		"echo.container": {Data: []byte("# Echo answers what it hears.\n; and nothing else\n[Container]\n\n  Image=docker.io/echo:1\n")},
		"README.md":      {Data: []byte("Echo, explained anew.\n")},
	}
	_, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update echo", Edit{Update: &UpdateService{Name: "echo", From: "echo", Template: tpl}}, nil)
	if !errors.Is(err, ErrInert) {
		t.Fatalf("a comment-only template update must be inert, got %v", err)
	}
	if open, _ := List(inbox, true); len(open) > 0 {
		t.Errorf("the Inbox got %d cards for comments", len(open))
	}
	if now, _ := git(ctx, repo, "rev-parse", "HEAD"); now != head {
		t.Error("main moved: the next apply would restart echo for a comment")
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "echo.container")); string(b) != unit {
		t.Errorf("the box's unit changed: %q", b)
	}

	// what runs differently keeps its card: a unit's line, a hook, the contract
	for name, data := range map[string]string{
		"echo.container": "# Echo answers what it hears.\n[Container]\nImage=docker.io/echo:2\n",
		"pre-start":      "#!/bin/sh\n# writes nothing yet\n",
		"laib.yml":       "name: echo\ntemplate: {registry: builtin, name: echo, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {url: /}\n",
	} {
		t.Run(name, func(t *testing.T) {
			changed := fstest.MapFS{}
			for k, v := range tpl {
				changed[k] = v
			}
			changed[name] = &fstest.MapFile{Data: []byte(data), Mode: 0o755}
			p, err := CreateFromEdit(ctx, repo, inbox, "laib", "template update echo", Edit{Update: &UpdateService{Name: "echo", From: "echo", Template: changed}}, nil)
			if err != nil {
				t.Fatalf("%s runs differently and must come as a card: %v", name, err)
			}
			Reject(ctx, repo, inbox, p, "human:core", "test")
		})
	}
}

// #1239: the box's template updates of one release are one card, on every
// surface the same: the lowest number holds every member's news by
// service, the others leave the list; a decided member stands for itself.
// Ablauf L-55
func TestTheTemplateUpdatesOfOneReleaseAreOneCard(t *testing.T) {
	member := func(id int, svc, news, class string) *Proposal {
		return &Proposal{ID: id, Actor: "laib", Reason: "template update " + svc, Kind: "template-update", Class: class, Status: "open", Tier: "apply",
			Karte: "Laib 2.31.0", Files: []string{"services/" + svc + "/" + svc + ".container", "services/" + svc + "/news"}, News: []string{news}}
	}
	other := &Proposal{ID: 590, Actor: "claude", Reason: "Jellyfin: neue Version", Kind: "values", Class: "values", Status: "open"}
	decided := member(570, "audiobookshelf", "Hörbücher laden schneller", "template-update")
	decided.Status = "approved"
	ps := []*Proposal{other, member(587, "paperless", "Paperless erkennt Rechnungen besser", "template-update"),
		member(575, "home-assistant", "Home Assistant findet den Drucker", "template-update"), member(580, "media", "Filme starten schneller", "human"), decided}

	folded := Fold(ps)
	if len(folded) != 3 {
		t.Fatalf("want the other card, one template card and the decided one, got %d", len(folded))
	}
	c := folded[1]
	if c.ID != 575 || c.Class != "human" || len(c.News) != 3 {
		t.Fatalf("the card is the lowest number, human when one member is, with every news line: #%d %s %q", c.ID, c.Class, c.News)
	}
	if ps[2].Reason != "template update home-assistant" || len(ps[2].News) != 1 {
		t.Error("Fold changed a stored proposal")
	}
	d := Card(c, "dopp.cloud")
	if d.Summary != "Neue Vorlagen aus Laib 2.31.0 für Home Assistant, Jellyfin, Paperless" {
		t.Errorf("headline: %q", d.Summary)
	}
	lines := ""
	for _, l := range d.Changes {
		lines += l.Text + "\n"
	}
	for _, want := range []string{"Home Assistant: Home Assistant findet den Drucker", "Jellyfin: Filme starten schneller", "Paperless: Paperless erkennt Rechnungen besser"} {
		if !strings.Contains(lines, want) {
			t.Errorf("the card lacks %q:\n%s", want, lines)
		}
	}
	if s := strings.Join(d.Impact, " "); !strings.Contains(s, "nimmt Laib nur ihn zurück") {
		t.Errorf("the card does not say a red service goes back alone: %v", d.Impact)
	}
	if strings.Contains(d.Summary+d.Why+strings.Join(d.Impact, ""), "template update") {
		t.Errorf("the card speaks in commands: %q / %q", d.Summary, d.Why)
	}

	many := []*Proposal{}
	for i, s := range []string{"auth", "bote", "media", "immich", "paperless"} {
		many = append(many, member(600+i, s, "neu", "template-update"))
	}
	if c := Fold(many)[0]; c.Class != "template-update" {
		t.Errorf("no member only a human may approve, yet the card is %s", c.Class)
	}
	one := Card(Fold(many[:1])[0], "")
	if len(one.Changes) != 1 || !strings.HasSuffix(one.Changes[0].Text, ": neu") {
		t.Errorf("a card of one template still shows its news line: %+v", one.Changes)
	}
	if s := Card(Fold(many)[0], "").Summary; !strings.HasPrefix(s, "Neue Vorlagen aus Laib 2.31.0 für ") || !strings.HasSuffix(s, " und 3 weitere Dienste") {
		t.Errorf("five services in one headline: %q", s)
	}
}
