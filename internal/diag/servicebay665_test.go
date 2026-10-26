package diag

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #665: ServiceBay's data dir keeps the passwords Laib carried at the move:
// in plaintext in its unit history (history/<file>/<time>.bak) and sealed
// in config.json with secret.key beside it. diag names every secret whose
// current value is still one of them, by name and service with the verb
// that fixes it, and never the value; once none is, it names the dir with
// its removal as a repair.

// seal as ServiceBay's secrets.ts does: AES-256-GCM, a 16-byte IV, enc:v1:iv:tag:ct in hex
func seal(t *testing.T, key []byte, plain string) string {
	block, _ := aes.NewCipher(key)
	gcm, err := cipher.NewGCMWithNonceSize(block, 16)
	if err != nil {
		t.Fatal(err)
	}
	iv := []byte("0123456789abcdef")
	out := gcm.Seal(nil, iv, []byte(plain), nil)
	ct, tag := out[:len(out)-16], out[len(out)-16:]
	return "enc:v1:" + hex.EncodeToString(iv) + ":" + hex.EncodeToString(tag) + ":" + hex.EncodeToString(ct)
}

func oldDir(t *testing.T) string {
	dir := t.TempDir()
	key := []byte("0123456789abcdef0123456789abcdef")
	os.WriteFile(filepath.Join(dir, "secret.key"), key, 0o600)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"installedSecrets":[{"varName":"DB_PASSWORD","password":"`+seal(t, key, "sealed-db-pw-665")+`"}]}`), 0o600)
	h := filepath.Join(dir, "history", "immich.kube")
	os.MkdirAll(h, 0o755)
	os.WriteFile(filepath.Join(h, "2026-10-01_10-00-00-000.bak"), []byte("env:\n  - name: JELLYFIN_ADMIN_PASSWORD\n    value: plain-jf-pw-665\n"), 0o600)
	return dir
}

func TestCarriedFindsPlainAndSealedValues(t *testing.T) {
	dir := oldDir(t)
	sec := map[string]string{
		"IMMICH_DB_PASSWORD":           "sealed-db-pw-665",
		"JELLYFIN_ADMIN_PASSWORD":      "plain-jf-pw-665",
		"AUTHELIA_JWT_SECRET":          "rotated-long-ago",
		"SHORT":                        "env", // shorter than a password: a word in any file, never a match
		"JELLYFIN_ADMIN_PASSWORD.next": "x",   // a pending rotation is no secret of its own
	}
	names, unread, err := Carried(dir, sec, 0)
	if err != nil || unread != 0 {
		t.Fatalf("err %v, unread %d", err, unread)
	}
	if !slices.Equal(names, []string{"IMMICH_DB_PASSWORD", "JELLYFIN_ADMIN_PASSWORD"}) {
		t.Fatalf("carried: %v", names)
	}
	if _, unread, _ := Carried(dir, sec, 10); unread == 0 {
		t.Fatal("past its budget the probe says what it did not read")
	}
	if _, _, err := Carried(filepath.Join(dir, "gone"), sec, 0); !os.IsNotExist(err) {
		t.Fatalf("a removed dir is no finding: %v", err)
	}
}

func TestServiceBayFindingsNameTheSecretNotItsValue(t *testing.T) {
	dir := oldDir(t)
	t.Setenv("LAIB_SERVICEBAY_DIR", dir)
	st := &state.State{Services: map[string]*state.Service{
		"immich": {Name: "immich", Contract: state.Contract{Secrets: []state.Secret{{Name: "IMMICH_DB_PASSWORD", DataBound: true, Rotate: true}}}},
		"media":  {Name: "media", Contract: state.Contract{Secrets: []state.Secret{{Name: "JELLYFIN_ADMIN_PASSWORD", DataBound: true}}}},
	}}
	sec := map[string]string{"IMMICH_DB_PASSWORD": "sealed-db-pw-665", "JELLYFIN_ADMIN_PASSWORD": "plain-jf-pw-665"}
	fs := servicebayFindings(st, sec)
	if len(fs) != 2 {
		t.Fatalf("want one finding per carried secret: %+v", fs)
	}
	for _, f := range fs {
		if f.Probe != "servicebay" || f.Severity != "gelb" || f.Repair != nil {
			t.Fatalf("a carried secret is a yellow servicebay finding without a repair (rotation is a human's at the console): %+v", f)
		}
		if strings.Contains(f.Detail+f.Fix, "-pw-665") {
			t.Fatalf("a value in a finding: %+v", f)
		}
	}
	if fs[0].Service != "immich" || !strings.Contains(fs[0].Detail, "IMMICH_DB_PASSWORD") || !strings.Contains(fs[0].Fix, "rotate-carried.sh immich") {
		t.Fatalf("immich's database password rotates through its hook: %+v", fs[0])
	}
	if fs[1].Service != "media" || !strings.Contains(fs[1].Fix, "in der App") {
		t.Fatalf("a data-bound secret without a hook is the owner's, in the app: %+v", fs[1])
	}

	sec = map[string]string{"IMMICH_DB_PASSWORD": "new-one", "JELLYFIN_ADMIN_PASSWORD": "new-two"}
	fs = servicebayFindings(st, sec)
	if len(fs) != 1 || !slices.Equal(fs[0].Repair, []string{"servicebay", "remove"}) || !strings.Contains(fs[0].Detail, dir) {
		t.Fatalf("with nothing carried left, the dir is named with its removal: %+v", fs)
	}
	t.Setenv("LAIB_SERVICEBAY_DIR", filepath.Join(dir, "gone"))
	if fs := servicebayFindings(st, sec); len(fs) != 0 {
		t.Fatalf("no dir, no finding: %+v", fs)
	}
}
