package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func keyIn(t *testing.T, dir string) string {
	t.Helper()
	k := filepath.Join(dir, "age.key")
	if _, err := EnsureKey(k); err != nil {
		t.Fatal(err)
	}
	return k
}

// Every value a hook or a human may hand in must come back byte for byte:
// secrets.age is written with Go's %q and read back as YAML.
func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	key := keyIn(t, dir)
	path := filepath.Join(dir, "secrets.age")
	in := map[string]string{
		"EMPTY":     "",
		"PLAIN":     Generate(),
		"QUOTES":    `a"b'c`,
		"BACKSLASH": `C:\path\to\x`,
		"NEWLINES":  "line1\nline2\r\n",
		"TAB":       "a\tb",
		"COLON":     "key: value",
		"HASH":      "# not a comment",
		"SPACES":    "  leading and trailing  ",
		"UNICODE":   "Grüße 🔑 ß",
		"DOLLAR":    "${secret:OTHER} $HOME",
		"YAMLISH":   "- [a, b]\n{c: d}",
		"CONTROL":   "bell\a vt\v nul\x00 esc\x1b",
		"BINARY":    "a\xff\xfeb", // not UTF-8: a key file piped into `laib secret set`
		"LONG":      strings.Repeat("x", 4096),
	}
	if err := Save(path, key, in); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("got %d secrets back, saved %d", len(out), len(in))
	}
	for k, want := range in {
		if got := out[k]; got != want {
			t.Errorf("%s: saved %q, loaded %q", k, want, got)
		}
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	m, err := Load(filepath.Join(dir, "secrets.age"), keyIn(t, dir))
	if err != nil || len(m) != 0 {
		t.Fatalf("missing file: got %v, %v; want an empty map", m, err)
	}
}

func TestLoadWithAnotherKeyFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.age")
	if err := Save(path, keyIn(t, dir), map[string]string{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "age.key")
	if _, err := EnsureKey(other); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, other); err == nil {
		t.Fatal("secrets.age decrypted with a key it was not encrypted to")
	}
}

func TestLoadCorruptFileFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.age")
	if err := os.WriteFile(path, []byte("not an age file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, keyIn(t, dir))
	if err == nil || !strings.Contains(err.Error(), "secrets.age") {
		t.Fatalf("corrupt secrets.age: want an error naming the file, got %v", err)
	}
}

func TestLoadWithoutKeyFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.age")
	key := keyIn(t, dir)
	if err := Save(path, key, map[string]string{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, key); err == nil {
		t.Fatal("secrets.age loaded without its key")
	}
}

func TestEnsureKeyIsPrivateAndStable(t *testing.T) {
	key := filepath.Join(t.TempDir(), "sub", "age.key")
	r1, err := EnsureKey(key)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(key)
	r2, err := EnsureKey(key)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(key)
	if r1 != r2 || string(before) != string(after) {
		t.Fatal("a second EnsureKey changed the identity")
	}
	if !strings.HasPrefix(r1, "age1") {
		t.Errorf("recipient %q is not an age recipient", r1)
	}
	st, err := os.Stat(key)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("age.key mode %o, want 600", st.Mode().Perm())
	}
}

func TestSaveIsPrivateAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.age")
	if err := Save(path, keyIn(t, dir), map[string]string{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("secrets.age mode %o, want 600", st.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("secrets.age.tmp left behind")
	}
}

func TestGenerateIsRandomAndURLSafe(t *testing.T) {
	a, b := Generate(), Generate()
	if a == b {
		t.Fatal("two generated secrets are equal")
	}
	if len(a) != 43 || strings.ContainsAny(a, "+/=") {
		t.Errorf("generated %q: want 43 URL-safe base64 characters", a)
	}
}
