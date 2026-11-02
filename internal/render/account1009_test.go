package render

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// laib#1009: a secret reaches a container as a file, never in its
// environment: Secret=${VALUE},type=mount,target=<file> names the podman
// secret mounted at /run/secrets/<file>, which apply creates from stdin like
// an Environment= line's. Any other shape is refused, and the error never
// shows the value.
func TestASecretAsAFile(t *testing.T) {
	values := map[string]state.Value{"PW": {Secret: "PW"}, "PW2": {Secret: "PW2"}, "U": {Literal: "x"}}
	secrets := map[string]string{"PW": "pw-v1009", "PW2": "pw2-v1009"}
	st := shareBox(map[string]string{"a.container": "[Container]\nImage=x\nSecret=${PW},type=mount,target=radicale-password\n"}, values)
	out, err := Render(st, secrets)
	if err != nil {
		t.Fatal(err)
	}
	unit := out["quadlet/share/a.container"]
	name := podmanSecretName("share", "a", "radicale-password")
	if !strings.Contains(unit, "\nSecret="+name+",type=mount,target=radicale-password\n") || strings.Contains(unit, "pw-v1009") {
		t.Errorf("the unit names the podman secret, never the value:\n%s", unit)
	}
	if got := PodmanSecrets(st, secrets)[name]; got != "pw-v1009" {
		t.Errorf("apply creates the podman secret %s with the value: %q", name, got)
	}
	if _, err := Render(st, map[string]string{}); err != nil || strings.Contains(must(Render(st, map[string]string{}))["quadlet/share/a.container"], "\nSecret=") {
		t.Errorf("an unset secret is no file (podman refuses an empty secret): %v", err)
	}
	for _, unit := range []struct{ file, line string }{
		{"a.container", "Secret=${PW},type=env,target=PW"},
		{"a.container", "Secret=${PW},target=pw"},
		{"a.container", "Secret=${PW}${PW2},type=mount,target=pw"},
		{"a.container", "Secret=x${PW},type=mount,target=pw"},
		{"a.container", "Secret=${PW},type=mount,target=/etc/pw"},
		{"a.container", "Secret=${PW},type=mount,target=pw,target=x"},
		{"a.service", "Secret=${PW},type=mount,target=pw"},
	} {
		head := "[Container]\nImage=x\n"
		if !strings.HasSuffix(unit.file, ".container") {
			head = "[Service]\n"
		}
		for _, sec := range []map[string]string{nil, secrets} {
			_, err := Render(shareBox(map[string]string{unit.file: head + unit.line + "\n"}, values), sec)
			if err == nil || strings.Contains(err.Error(), "pw-v1009") {
				t.Errorf("%s: %q: refused without the value, got %v", unit.file, unit.line, err)
			}
		}
	}
}

// The identity service's list: who declares which account, names only.
func TestTheAccountsFileNamesEveryAccountAndNoPassword(t *testing.T) {
	st, out := perPerson986(t, map[string]string{"NOTIZBUCH_DAV_PASSWORD_ANNA": "pw-anna-v1009"})
	var got []state.Account
	if err := json.Unmarshal([]byte(out["identity/accounts.json"]), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (state.Account{User: "notizbuch-anna", Secret: "NOTIZBUCH_DAV_PASSWORD_ANNA", Service: "notizbuch-anna"}) || got[1].User != "notizbuch-martin" {
		t.Errorf("one account per instance, its name with the person's: %+v", got)
	}
	if strings.Contains(out["identity/accounts.json"], "pw-anna") {
		t.Error("the list carries a password")
	}
	st.Services["notizbuch-martin"].Contract.Secrets = nil
	st.Services["notizbuch-anna"].Contract.Secrets = nil
	if out, _ := Render(st, nil); out["identity/accounts.json"] != "[]\n" {
		t.Errorf("no account: an empty list, so the identity service removes the last one: %q", out["identity/accounts.json"])
	}
}

func must(out Output, err error) Output {
	if err != nil {
		panic(err)
	}
	return out
}
