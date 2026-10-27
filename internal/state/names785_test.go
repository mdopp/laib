package state

import "testing"

// #785: the template's plain name and description reach the contract.
func TestTheContractCarriesTheTitleAndBeschreibung(t *testing.T) {
	repo := box46(t, "")
	writeSvc(t, repo, "media", "name: media\ntitle: Jellyfin\nbeschreibung: Filme, Serien, Musik\ntemplate: {registry: builtin, name: media, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {http: http://127.0.0.1:1/}\n", map[string]string{"media.container": "[Container]\nImage=${IMAGE}\n"})
	st, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if c := st.Services["media"].Contract; c.Title != "Jellyfin" || c.Beschreibung != "Filme, Serien, Musik" {
		t.Errorf("title %q, beschreibung %q", c.Title, c.Beschreibung)
	}
}
