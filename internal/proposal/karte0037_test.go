package proposal

import "testing"

// ADR 0037 §7: „hoch“ is a new service, a key, a public path or no login, a
// script or unit on the host, the destroy tier — each with the words its
// approve button says; anything else has none.
func TestHighCardsNameWhatTheirApproveDoes(t *testing.T) {
	for _, c := range []struct {
		p    *Proposal
		want string
	}{
		{&Proposal{Class: "new-service", Kind: "new-service", Tier: "apply", Adds: []string{"mosquitto"}}, "Neuen Dienst freigeben"},
		{&Proposal{Class: "human", Kind: "tree", Tier: "apply", Mints: []string{"bote"}}, "Schlüssel freigeben"},
		{&Proposal{Class: "human", Kind: "tree", Tier: "apply", Reasons: []string{"macht media aus dem Internet erreichbar (media)"}}, "Öffentlichen Pfad freigeben"},
		{&Proposal{Class: "human", Kind: "patch", Tier: "apply", Reasons: []string{"führt einen Befehl direkt auf der Box aus (ExecStart in x.container)"}}, "Skript auf der Box freigeben"},
		{&Proposal{Class: "human", Kind: "patch", Tier: "apply", Reasons: []string{"legt eine Unit an (Host-Unit)"}}, "Skript auf der Box freigeben"},
		{&Proposal{Class: "human", Kind: "tree", Tier: "destroy"}, "Endgültig freigeben"},
		{&Proposal{Class: "values", Kind: "values", Tier: "apply"}, ""},
		{&Proposal{Class: "human", Kind: "tree", Tier: "apply", Reasons: []string{"ändert box.yml (dns)"}, Files: []string{"box.yml"}}, ""},
	} {
		d := Card(c.p, "box.test")
		if d.Approve != c.want || (d.Risk == "hoch") != (c.want != "") {
			t.Errorf("%+v: risk %s, approve %q, want %q", c.p, d.Risk, d.Approve, c.want)
		}
	}
}
