package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fake key material, assembled so no scanner mistakes the test for a key
var (
	pemBegin1012 = "-----BEGIN " + "PRIVATE KEY-----"
	pemEnd1012   = "-----END " + "PRIVATE KEY-----"
	keyLine1012  = "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7fake1012key"
)

func TestScrubTakesKeysAndTokensAndKeepsTheRest(t *testing.T) {
	known := map[string]string{"AUTH_SESSION_SECRET": "known-value-of-the-box-1012"}
	cases := map[string]string{
		"whole block":       "jwks:\n  - key: |\n      " + pemBegin1012 + "\n      " + keyLine1012 + "\n      " + pemEnd1012 + "\n",
		"tail cut the head": "…" + keyLine1012[20:] + "\n      " + keyLine1012 + "\n      " + pemEnd1012 + "\nserver:\n",
		"head cut the tail": "key: |\n  " + pemBegin1012 + "\n  " + keyLine1012 + "\n  " + keyLine1012[:30] + "…",
		"escaped newlines":  `{"key":"` + pemBegin1012 + `\n` + keyLine1012 + `\n` + pemEnd1012 + `"}`,
		"escaped tail":      keyLine1012[9:] + `\n` + keyLine1012 + `\n` + pemEnd1012 + `\n"}`,
		"known value":       "session:\n  secret: known-value-of-the-box-1012\n",
		"servicebay token":  "Authorization: sb_" + strings.Repeat("a1B2", 8),
		"laib token":        "token laib_claude_" + strings.Repeat("Zx9_", 8),
		"bearer":            "curl -H 'Authorization: Bearer " + strings.Repeat("q7W", 8) + "'",
		"key value":         "OIDC_HMAC_SECRET=" + strings.Repeat("h4mac", 4),
		"age identity":      "AGE-SECRET-KEY-1" + strings.Repeat("QX7", 20),
	}
	for name, in := range cases {
		out := Scrub(in, known)
		for _, leak := range []string{keyLine1012[20:40], "PRIVATE KEY", "known-value-of-the-box", "a1B2a1B2", "Zx9_Zx9_", "q7Wq7W", "h4mach4mac", "QX7QX7"} {
			if strings.Contains(out, leak) {
				t.Errorf("%s: %q left in %q", name, leak, out)
			}
		}
		if !strings.Contains(out, Redacted) && !strings.Contains(out, "${secret:") {
			t.Errorf("%s: nothing marked where something was taken: %q", name, out)
		}
	}
	for _, keep := range []string{
		"set NOTIFY_TOKEN",
		"password: ${secret:LLDAP_ADMIN_PASSWORD}",
		"secret: von der App nicht übernommen",
		"exit status 1\nError: no such file or directory: /config/configuration.yml",
		"mint 3f2a for claude ttl=24h",
		"",
	} {
		if out := Scrub(keep, known); out != keep {
			t.Errorf("an ordinary line changed: %q became %q", keep, out)
		}
	}
}

func TestAppendScrubsEveryVerbsLine(t *testing.T) {
	dir := t.TempDir()
	prev := Known
	t.Cleanup(func() { Known = prev })
	Known = func() map[string]string { return map[string]string{"X_SECRET": "known-value-of-the-box-1012"} }
	for _, verb := range []string{"look", "exec", "backup", "apply"} {
		Append(dir, Entry{Actor: "claude", Verb: verb, Result: "red", Reason: "cat known-value-of-the-box-1012", Detail: keyLine1012 + "\n" + pemEnd1012})
	}
	raw := readAll(t, dir)
	for _, leak := range []string{"known-value-of-the-box", keyLine1012[20:40], "PRIVATE KEY"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("%q written to the journal", leak)
		}
	}
}

func TestScrubOnceCleansOldFilesOnce(t *testing.T) {
	dir := t.TempDir()
	prev := Known
	t.Cleanup(func() { Known = prev })
	Known = func() map[string]string { return map[string]string{"X_SECRET": "known-value-of-the-box-1012"} }
	old := `{"t":"2026-11-01T23:47:00Z","actor":"claude","verb":"look","service":"auth","reason":"cat /config/configuration.yml","result":"info","detail":"…` + keyLine1012[9:] + `\n      ` + keyLine1012 + `\n      ` + pemEnd1012 + `\nsession:\n  secret: known-value-of-the-box-1012\n"}` + "\n" +
		`{"t":"2026-11-01T23:48:00Z","actor":"claude","verb":"note","result":"info","detail":"alles gut"}` + "\n" +
		`not json sb_` + strings.Repeat("a1B2", 8) + "\n"
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "2026-11-01.jsonl"), []byte(old), 0o644)
	n, err := ScrubOnce(dir)
	if err != nil || n != 2 {
		t.Fatalf("scrubbed %d entries, err %v; want 2", n, err)
	}
	raw := readAll(t, dir)
	for _, leak := range []string{"known-value-of-the-box", keyLine1012[20:40], "PRIVATE KEY", "a1B2a1B2"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("%q left in the old journal", leak)
		}
	}
	es, skipped, err := ReadCounted(dir, time.Time{})
	if err != nil || len(es) != 2 || skipped != 1 || es[1].Detail != "alles gut" || es[0].Service != "auth" {
		t.Fatalf("the scrub broke the journal: %+v skipped %d err %v", es, skipped, err)
	}
	if n, err := ScrubOnce(dir); n != 0 || err != nil {
		t.Fatalf("the second run scrubbed %d (%v)", n, err)
	}
}

func readAll(t *testing.T, dir string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	var b strings.Builder
	for _, f := range files {
		c, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(c)
	}
	return b.String()
}
