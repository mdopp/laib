package proposal

import (
	"strings"
	"testing"
)

// Freigabe #317 read „Archiv Mdopp bekommt eine neue Version:
// aa2e41d7…c7 → d4f043ae…53“: a Werkstatt app is pinned by its commit,
// which says no more than a digest (#959), and a per-person instance was
// named by its service name. The card says „Archiv von mdopp bekommt ein
// Update“ and, with no version to name, what the update brings in the
// proposer's words; the exact image stays under Details.
func TestACommitPinIsNoVersionAndAnInstanceIsAPersons(t *testing.T) {
	const img = "ghcr.io/mdopp/archiv:"
	old, new := img+"aa2e41d7fc646e89fa6002159c2a0262bf9c5dc7", img+"d4f043ae1ede1425f7f873ac8a811f67966c3d53"
	p := Proposal{ID: 317, Actor: "claude", Reason: "Dein Archiv holt jetzt auch deine E-Mails ab, jede Stunde", Kind: "values", Class: "values", Tier: "apply",
		Files:   []string{"services/archiv-mdopp/values.yml"},
		Changes: []Change{{Op: "~", Service: "archiv-mdopp", Key: "ARCHIV_IMAGE", Old: old, New: new}}}
	d := Card(&p, "box.test")
	if want := "Archiv von mdopp bekommt ein Update: Dein Archiv holt jetzt auch deine E-Mails ab, jede Stunde"; d.Summary != want {
		t.Errorf("summary %q, want %q", d.Summary, want)
	}
	if text := cardText(d); strings.Contains(text, "aa2e41d7") || strings.Contains(text, "d4f043ae") || strings.Contains(text, "Mdopp") {
		t.Errorf("the card shows a commit or the raw name:\n%s", text)
	}
	if !strings.Contains(d.Details, new) {
		t.Errorf("the exact image belongs under Details: %q", d.Details)
	}

	// a technical reason does not reach the headline
	p.Reason = "values.yml: ARCHIV_IMAGE neu"
	if d := Card(&p, "box.test"); d.Summary != "Archiv von mdopp bekommt ein Update" {
		t.Errorf("summary %q", d.Summary)
	}
	// labels still name versions when the image has them
	p.Changes[0].OldVersion, p.Changes[0].NewVersion = "0.3.0", "0.4.0"
	if d := Card(&p, "box.test"); d.Summary != "Archiv von mdopp bekommt ein Update: 0.3.0 → 0.4.0" {
		t.Errorf("summary %q", d.Summary)
	}
	for svc, want := range map[string]string{"archiv-anna": "Archiv von anna", "archiv": "Archiv", "media": "Jellyfin", "paperless-ngx": "Paperless Ngx"} {
		if got := Named(svc); got != want {
			t.Errorf("Named(%q) = %q, want %q", svc, got, want)
		}
	}
}
