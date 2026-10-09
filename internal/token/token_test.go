package token

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMintLookupExpireRevoke(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	plain, rec, err := s.Mint("claude", 8*time.Hour, "session")
	if err != nil || !strings.HasPrefix(plain, "laib_claude_") {
		t.Fatal(plain, err)
	}
	if strings.Contains(string(mustRead(t, s.Path)), plain) {
		t.Fatal("plain token stored")
	}
	if got, ok := s.Lookup(plain, time.Now()); !ok || got.Hand != "claude" {
		t.Fatal("lookup failed")
	}
	if _, ok := s.Lookup(plain, time.Now().Add(9*time.Hour)); ok {
		t.Fatal("expired token accepted")
	}
	if _, ok := s.Lookup("laib_claude_nope", time.Now()); ok {
		t.Fatal("unknown token accepted")
	}
	s2, _ := Load(s.Path)
	if err := s2.Revoke(rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Lookup(plain, time.Now()); ok {
		t.Fatal("revoked token accepted")
	}
	if s2.Revoke("missing") == nil {
		t.Fatal("revoking a missing id succeeded")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := readFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
