package apply

import (
	"slices"
	"testing"

	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/render"
)

// #523: after `template update frage` the restart ran the old
// laib-frage:latest until someone pulled by hand: the tag had not changed,
// so apply pulled nothing. A floating image of a service the plan restarts
// is pulled; a pinned one, or one of a service not in the plan, is not.
func TestAFloatingImageOfARestartedServiceIsPulled(t *testing.T) {
	unit := func(img, extra string) string { return "[Container]\nImage=" + img + "\n" + extra + "\n[Install]\nWantedBy=default.target\n" }
	out := render.Output(map[string]string{
		"quadlet/frage/frage.container":       unit("ghcr.io/mdopp/laib-frage:latest", "AutoUpdate=registry"),
		"quadlet/frage/frage-searx.container": unit("docker.io/searxng/searxng:2026.10.1", "AutoUpdate=registry"),
		"quadlet/frage/frage-pin.container":   unit("ghcr.io/x/y:latest@sha256:00", "AutoUpdate=registry"),
		"quadlet/frage/frage-tag.container":   unit("ghcr.io/x/tagged:1.2", ""),
		"quadlet/frage/frage-bare.container":  unit("localhost:5000/bare", ""),
		"quadlet/media/media.container":       unit("ghcr.io/x/media:latest", "AutoUpdate=registry"),
	})
	// only frage's hook changed; the units are the same as on the box
	p := &plan.Plan{Services: []string{"frage"}, Output: out, Changes: []plan.Change{{Path: "quadlet/frage/pre-start", Service: "frage", Kind: "change"}}}
	got := imagesToPull(p, map[string]string{})
	want := []string{"docker.io/searxng/searxng:2026.10.1", "ghcr.io/mdopp/laib-frage:latest", "localhost:5000/bare"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("pulled %v, want %v", got, want)
	}
	// an unchanged pinned unit of a changed service: nothing to pull
	p = &plan.Plan{Services: []string{"frage"}, Output: render.Output(map[string]string{"quadlet/frage/frage-tag.container": out["quadlet/frage/frage-tag.container"]}),
		Changes: []plan.Change{{Path: "quadlet/frage/frage-tag.container", Service: "frage", Kind: "change"}}}
	if got := imagesToPull(p, map[string]string{"quadlet/frage/frage-tag.container": "ghcr.io/x/tagged:1.2"}); len(got) != 0 {
		t.Fatalf("a pinned tag that did not change is pulled: %v", got)
	}
}
