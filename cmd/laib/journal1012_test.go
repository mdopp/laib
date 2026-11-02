package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/sysd"
)

// #1012: a look printed Authelia's rendered configuration, and the tail the
// journal keeps held the end of its private key; laib context --full handed
// it to every reader. Fake material, assembled so no scanner takes it for a key.
func TestALookJournalsNoKeyNoTokenNoSecret(t *testing.T) {
	env, _, _ := envBox(t)
	prevKnown := journal.Known
	t.Cleanup(func() { journal.Known = prevKnown })
	knowSecrets(env)
	known := "known-value-of-the-box-1012"
	if err := secrets.Save(env.Paths.Secrets, env.Paths.Key, map[string]string{"AUTH_SESSION_SECRET": known}); err != nil {
		t.Fatal(err)
	}
	keyLine := "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7fake1012key"
	token := "sb_" + strings.Repeat("a1B2", 8)
	var cfg strings.Builder
	cfg.WriteString("identity_providers:\n  oidc:\n    jwks:\n      - key: |\n          -----BEGIN " + "PRIVATE KEY-----\n")
	for range 40 { // longer than the tail the journal keeps: the BEGIN is cut off, as on the box
		cfg.WriteString("          " + keyLine + "\n")
	}
	cfg.WriteString("          -----END " + "PRIVATE KEY-----\nsession:\n  secret: " + known + "\nnotifier:\n  token: " + token + "\n")
	prevRun := sysd.Run
	t.Cleanup(func() { sysd.Run = prevRun })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "podman" && len(args) > 0 && args[0] == "exec" {
			return cfg.String(), nil
		}
		return prevRun(ctx, name, args...)
	}
	leaks := []string{keyLine[20:44], "PRIVATE KEY", known, "a1B2a1B2"}
	look := &cliEnv{Actor: "claude", Paths: env.Paths, JSON: true, Out: &strings.Builder{}, ErrOut: &strings.Builder{}}
	if rc := envelope.Look(context.Background(), look, []string{"whoami", "--", "cat", "/config/configuration.yml"}); rc != 0 {
		t.Fatalf("look: exit %d", rc)
	}
	journalHasNone(t, env, leaks)

	// a line written before #1012, as it stood on the box: scrubbed once, and
	// context --full shows none of it
	old := `{"t":"` + "2026-11-01T23:47:00Z" + `","actor":"claude","verb":"look","service":"auth","result":"info","detail":"…` +
		strings.ReplaceAll(cfg.String()[len(cfg.String())-1400:], "\n", `\n`) + `"}` + "\n"
	os.WriteFile(filepath.Join(env.Paths.Journal, "2026-11-01.jsonl"), []byte(old), 0o644)
	if n, err := journal.ScrubOnce(env.Paths.Journal); err != nil || n != 1 {
		t.Fatalf("scrubbed %d (%v), want the one old line", n, err)
	}
	journalHasNone(t, env, leaks)
	full := runJSON(t, env, cmdContext, "--full", "--since", "100000h")
	if !strings.Contains(full, "configuration.yml") {
		t.Fatal("context --full does not show the look at all: the check below cannot fail")
	}
	for _, leak := range leaks {
		if strings.Contains(full, leak) {
			t.Fatalf("context --full shows %q", leak)
		}
	}
}

func journalHasNone(t *testing.T, env *cliEnv, leaks []string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(env.Paths.Journal, "*.jsonl"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, leak := range leaks {
			if strings.Contains(string(b), leak) {
				t.Fatalf("%q in the journal %s", leak, filepath.Base(f))
			}
		}
	}
}
