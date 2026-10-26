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

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
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
	env := &cli.Env{Paths: boxfs.Paths{Journal: t.TempDir()}}
	fs := servicebayFindings(env, st, sec)
	if len(fs) != 3 || !slices.Equal(fs[2].Repair, []string{"servicebay", "remove", "--trotzdem"}) || fs[2].Severity != "info" {
		t.Fatalf("want one finding per carried secret, then the owner's way to remove the dir anyway: %+v", fs)
	}
	fs = fs[:2]
	for _, f := range fs {
		if f.Probe != "servicebay" || f.Severity != "gelb" {
			t.Fatalf("a carried secret is a yellow servicebay finding: %+v", f)
		}
		if strings.Contains(f.Detail+f.Fix, "-pw-665") {
			t.Fatalf("a value in a finding: %+v", f)
		}
	}
	// the owner works in the Inbox, not at the console: a secret laib can rotate is a repair a human approves
	if fs[0].Service != "immich" || !strings.Contains(fs[0].Detail, "IMMICH_DB_PASSWORD") || !slices.Equal(fs[0].Repair, []string{"secret", "rotate", "IMMICH_DB_PASSWORD"}) {
		t.Fatalf("immich's database password rotates as an approved repair: %+v", fs[0])
	}
	if !inbox.Repairable(st, fs[0].Repair) || inbox.Repairable(st, []string{"secret", "rotate", "JELLYFIN_ADMIN_PASSWORD"}) || inbox.Repairable(st, []string{"secret", "rotate", "NOPE"}) {
		t.Fatalf("only a declared secret laib can rotate is repairable")
	}
	if fs[1].Service != "media" || fs[1].Repair != nil || !strings.Contains(fs[1].Fix, "in der App") {
		t.Fatalf("a data-bound secret without a hook is the owner's, in the app: %+v", fs[1])
	}

	sec = map[string]string{"IMMICH_DB_PASSWORD": "new-one", "JELLYFIN_ADMIN_PASSWORD": "new-two"}
	fs = servicebayFindings(env, st, sec)
	if len(fs) != 1 || !slices.Equal(fs[0].Repair, []string{"servicebay", "remove"}) || !strings.Contains(fs[0].Detail, dir) {
		t.Fatalf("with nothing carried left, the dir is named with its removal: %+v", fs)
	}
	t.Setenv("LAIB_SERVICEBAY_DIR", filepath.Join(dir, "gone"))
	if fs := servicebayFindings(env, st, sec); len(fs) != 0 {
		t.Fatalf("no dir, no finding: %+v", fs)
	}
}

// The box: 575 files of the old dir unreadable for laib, so a carried secret
// the readable part does not show may still be there; until the journal shows
// it rotated or set it is a finding with its repair (#665).
func TestUnreadFilesKeepTheCarriedSecretsLaibHasNotRenewed(t *testing.T) {
	dir := oldDir(t)
	locked := filepath.Join(dir, "history", "locked.kube")
	os.MkdirAll(locked, 0o755)
	os.WriteFile(filepath.Join(locked, "x.bak"), []byte("x"), 0o000)
	if f, err := os.Open(filepath.Join(locked, "x.bak")); err == nil { // root reads anything
		f.Close()
		t.Skip("running as root: no unreadable file")
	}
	t.Setenv("LAIB_SERVICEBAY_DIR", dir)
	st := &state.State{Services: map[string]*state.Service{
		"paperless": {Name: "paperless", Contract: state.Contract{Secrets: []state.Secret{{Name: "PAPERLESS_DB_PASSWORD", DataBound: true, Rotate: true}}}},
	}}
	env := &cli.Env{Paths: boxfs.Paths{Journal: t.TempDir()}}
	sec := map[string]string{"PAPERLESS_DB_PASSWORD": "today-665"}
	// the move itself ran laib secret set (the box's journal, 2026-10-18): that is no renewal
	journal.Append(env.Paths.Journal, journal.Entry{Actor: "human:core", Verb: "secret", Result: "info", Detail: "set PAPERLESS_DB_PASSWORD"})
	fs := servicebayFindings(env, st, sec)
	if len(fs) < 1 || fs[0].Service != "paperless" || !slices.Equal(fs[0].Repair, []string{"secret", "rotate", "PAPERLESS_DB_PASSWORD"}) || strings.Contains(fs[0].Detail, "today-665") {
		t.Fatalf("an unrenewed carried secret behind unreadable files is a repair: %+v", fs)
	}
	journal.Append(env.Paths.Journal, journal.Entry{Actor: "human:owner", Verb: "secret", Result: "info", Detail: "secret rotate PAPERLESS_DB_PASSWORD"})
	for _, f := range servicebayFindings(env, st, sec) {
		if f.Service == "paperless" {
			t.Fatalf("a rotated secret is no finding any more: %+v", f)
		}
	}
}
