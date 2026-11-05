package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
)

// fake hashes, assembled so no scanner mistakes the test for a credential
var (
	bcrypt1054 = "$2" + "a$10$" + strings.Repeat("N9qo8uLOickgx2ZMRZoMye", 3)[:53]
	argon1054  = "$argon" + "2id$v=19$m=65536,t=3,p=4$" + "c2FsdHNhbHRzYWx0" + "$" + strings.Repeat("aGFzaGhhc2g", 4)
)

// #1054: laib read promises "secrets redacted", and returned AdGuard's admin
// password hash: only the values in secrets.age were taken out, and a hash
// is never one of them. Now a password hash (bcrypt, argon2, sha-crypt) and
// the value of a key named password, passwd or hash are taken out too; a
// placeholder and an ordinary line stay. A file read so is never written
// back over the original: that would overwrite the hash with the mark.
func TestReadRedactsPasswordHashesAndWriteRefusesTheMark(t *testing.T) {
	env, data := testBox(t)
	dir := filepath.Join(data, "whoami", "conf")
	os.MkdirAll(dir, 0o755)
	file := filepath.Join(dir, "AdGuardHome.yaml")
	orig := "http:\n  address: 0.0.0.0:3000\nusers:\n  - name: admin\n    password: " + bcrypt1054 + "\n" +
		"  - name: kiosk\n    password: '" + argon1054 + "'\n" +
		"auth:\n  passwd = kurz-fake\n  password_hash: \"" + strings.Repeat("ab12", 8) + "\"\n" +
		"  hash_algorithm: bcrypt\n  password: ${secret:ADGUARD_PASSWORD}\n  note: " + bcrypt1054[:7] + "\n"
	if err := os.WriteFile(file, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	env.Out = &out
	if code := verb(t, "read").Run(context.Background(), env, []string{"whoami", file}); code != cli.ExitOK {
		t.Fatalf("read: exit %d", code)
	}
	got := out.String()
	for _, leak := range []string{bcrypt1054[7:30], argon1054[30:], "kurz-fake", "ab12ab12"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q left in what read answers:\n%s", leak, got)
		}
	}
	for _, keep := range []string{"name: admin", "address: 0.0.0.0:3000", "hash_algorithm: bcrypt", "password: ${secret:ADGUARD_PASSWORD}", "note: " + bcrypt1054[:7] + "\n"} {
		if !strings.Contains(got, keep) {
			t.Errorf("an ordinary line changed, %q is gone:\n%s", keep, got)
		}
	}
	if strings.Count(got, journal.Redacted) != 4 {
		t.Errorf("four values taken out, each marked:\n%s", got)
	}

	var errOut strings.Builder
	env.In, env.ErrOut = strings.NewReader(got), &errOut
	if code := verb(t, "write").Run(context.Background(), env, []string{"whoami", file}); code == cli.ExitOK {
		t.Fatal("a redacted read was written back")
	}
	if b, _ := os.ReadFile(file); string(b) != orig {
		t.Fatalf("the file changed:\n%s", b)
	}
	if !strings.Contains(errOut.String(), journal.Redacted) {
		t.Errorf("the refusal does not say why: %s", errOut.String())
	}
}
