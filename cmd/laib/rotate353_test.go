package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/secrets"
)

// #353: `laib secret rotate` wrote a new value and re-applied, but LLDAP's
// admin password, Postgres's and the others the data was made with are read
// only when the app creates its data: the app was locked out, the old value
// gone. A secret the template marks dataBound is refused by rotate, with the
// way that works, and nothing changes; any other secret still rotates.
func TestRotateRefusesADataBoundSecret(t *testing.T) {
	b := newRbox(t)
	decl := filepath.Join(b.env.Paths.State, "services", "auth", "laib.yml")
	raw, _ := os.ReadFile(decl)
	text := strings.Replace(string(raw), "  - name: LLDAP_ADMIN_PASSWORD\n", "  - name: LLDAP_ADMIN_PASSWORD\n    dataBound: true\n", 1)
	if text == string(raw) {
		t.Fatal("the golden auth declares no LLDAP_ADMIN_PASSWORD")
	}
	os.WriteFile(decl, []byte(text), 0o644)
	sec, _ := secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	sec["LLDAP_ADMIN_PASSWORD"] = "made-the-data"
	secrets.Save(b.env.Paths.Secrets, b.env.Paths.Key, sec)

	var errw bytes.Buffer
	b.env.ErrOut = &errw
	if rc := cmdSecret(context.Background(), b.env, []string{"rotate", "LLDAP_ADMIN_PASSWORD"}); rc == cli.ExitOK {
		t.Fatal("a data-bound secret must not rotate")
	}
	if !strings.Contains(errw.String(), "LLDAP_ADMIN_PASSWORD") || !strings.Contains(errw.String(), "laib secret set") {
		t.Fatalf("the refusal says why and what works: %q", errw.String())
	}
	sec, _ = secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	if sec["LLDAP_ADMIN_PASSWORD"] != "made-the-data" {
		t.Fatal("the refused rotate changed the value")
	}
	old := sec["AUTHELIA_JWT_SECRET"]
	if rc := cmdSecret(context.Background(), b.env, []string{"rotate", "AUTHELIA_JWT_SECRET"}); rc != cli.ExitOK {
		t.Fatalf("a secret the app reads on every start still rotates: %s", errw.String())
	}
	sec, _ = secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	if sec["AUTHELIA_JWT_SECRET"] == old {
		t.Fatal("not rotated")
	}
}
