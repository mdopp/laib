package diag

import (
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #696: a missing owner's key whose feature is in use points to its own
// form on the box page; one whose use Laib cannot see stays quiet.
func TestAMissingKeyInUseLinksItsForm(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	owner := &state.OwnerKey{Label: "L", Warum: "W", Hilfe: "H"}
	caddy, who := st.Services["caddy"], st.Services["whoami"]
	for i := range caddy.Contract.Secrets {
		if caddy.Contract.Secrets[i].Name == "DNS_API_TOKEN" {
			caddy.Contract.Secrets[i].Owner = owner
		}
	}
	who.Contract.Secrets = append(who.Contract.Secrets, state.Secret{Name: "WHO_MAIL", External: true, Owner: owner}, state.Secret{Name: "WHO_DISCOGS", External: true, Owner: owner})
	who.Contract.Requires = map[string][]string{"MAIL_HOST": {"MAIL_PASSWORD"}}
	who.Values["MAIL_PASSWORD"] = state.Value{Secret: "WHO_MAIL"}
	who.Values["DISCOGS"] = state.Value{Secret: "WHO_DISCOGS"}

	got := map[string]Finding{}
	for _, f := range keyFindings(st, map[string]string{}) {
		got[f.Topic] = f
	}
	if f := got["DNS_API_TOKEN"]; f.Severity != "gelb" || f.Fix != "Schlüssel hinterlegen: https://laib.example.home/schluessel?name=DNS_API_TOKEN" {
		t.Errorf("the DNS-01 token, missing: %+v", f)
	}
	if _, ok := got["WHO_MAIL"]; ok {
		t.Error("a key whose requiring value is not set is a finding")
	}
	if _, ok := got["WHO_DISCOGS"]; ok {
		t.Error("a key whose use Laib cannot see nags")
	}
	who.Values["MAIL_HOST"] = state.Value{Literal: "mail.example"}
	if fs := keyFindings(st, map[string]string{"DNS_API_TOKEN": "x"}); len(fs) != 1 || fs[0].Topic != "WHO_MAIL" || fs[0].Service != "whoami" {
		t.Errorf("only the required, missing key: %+v", fs)
	}
}
