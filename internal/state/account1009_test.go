package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// laib#1009: a secret may be a service account's password (account:),
// seeded by the identity service. Its name carries the instance's person,
// and the load refuses a name LDAP cannot address, a person's own login,
// two services claiming one account, an account on an external secret, and
// one on a box without an identity role.
func TestAServiceAccountIsItsInstancesAndNobodyElses(t *testing.T) {
	st, err := Load(perPerson986(t))
	if err != nil {
		t.Fatal(err)
	}
	if a := st.Accounts(); len(a) != 2 || a[0] != (Account{"notizbuch-anna", "NOTIZBUCH_DAV_PASSWORD_ANNA", "notizbuch-anna"}) || a[1].User != "notizbuch-martin" {
		t.Errorf("one account per instance, its secret the instance's: %+v", a)
	}
	for want, edit := range map[string]func(dir string){
		"no name LDAP": func(d string) { replace(t, d, "services/notizbuch-anna/laib.yml", "notizbuch-${PERSON}", "notiz,ou=x") },
		"a person's":   func(d string) { replace(t, d, "services/notizbuch-anna/laib.yml", "notizbuch-${PERSON}", "lena") },
		"another service's": func(d string) {
			replace(t, d, "services/notizbuch-anna/laib.yml", "notizbuch-${PERSON}", "notizbuch-martin")
		},
		"wants a generated": func(d string) {
			replace(t, d, "services/notizbuch-anna/laib.yml", "account:", "external: true\n    account:")
		},
		"and box.yml roles.ident": func(d string) { replace(t, d, "box.yml", "  identity: auth\n", "") },
	} {
		dir := perPerson986(t)
		edit(dir)
		if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "account") {
			t.Errorf("%s: the load must refuse it, got %v", want, err)
		}
	}
}

func replace(t *testing.T, dir, file, old, new string) {
	t.Helper()
	p := filepath.Join(dir, file)
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s has no %q", file, old)
	}
	os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644)
}
