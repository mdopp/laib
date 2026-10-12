package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

// secrets.age files written before the yaml.Marshal change (Go %q quoting)
// must still load.
func TestOldFormatStillLoads(t *testing.T) {
	d := t.TempDir()
	k := filepath.Join(d, "age.key")
	p := filepath.Join(d, "secrets.age")
	rcpt, err := EnsureKey(k)
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.ParseX25519Recipient(rcpt)
	if err != nil {
		t.Fatal(err)
	}
	var enc bytes.Buffer
	w, err := age.Encrypt(&enc, r)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("A: \"x\\\"y\"\nB: \"line1\\nline2\"\nC: \"\"\n"))
	w.Close()
	if err := os.WriteFile(p, enc.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Load(p, k)
	if err != nil {
		t.Fatal(err)
	}
	if m["A"] != `x"y` || m["B"] != "line1\nline2" || m["C"] != "" || len(m) != 3 {
		t.Fatalf("old format loaded as %q", m)
	}
}
