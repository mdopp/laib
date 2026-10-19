package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/secrets"
)

// #441 (owner, 2026-10-19): the admin logins Laib generated reach the owner
// safely. A template marks a secret that is a human's login (`login:` with
// user, host, path, note). The recovery sheet lists which logins exist and
// where, without a password; `laib recovery-sheet --vaultwarden <file>`
// writes them with their passwords as a Bitwarden JSON import file, mode
// 0600, never over an existing file, never to the terminal, humans only.
func loginBox(t *testing.T) (*cbox, string) {
	t.Helper()
	c := newCbox(t)
	resticRecorder(t, c)
	dir := filepath.Join(c.env.Paths.State, "services", "whoami")
	y, _ := os.ReadFile(filepath.Join(dir, "laib.yml"))
	os.WriteFile(filepath.Join(dir, "laib.yml"), []byte(strings.Replace(string(y), "  - name: WHOAMI_TOKEN\n", "  - name: WHOAMI_TOKEN\n    login: {user: \"${ADMIN_USER}\", host: whoami, path: /admin, note: die Admin-Seite}\n", 1)), 0o644)
	v, _ := os.ReadFile(filepath.Join(dir, "values.yml"))
	os.WriteFile(filepath.Join(dir, "values.yml"), append(v, []byte("ADMIN_USER: chef\n")...), 0o644)
	sec, _ := secrets.Load(c.env.Paths.Secrets, c.env.Paths.Key)
	sec["WHOAMI_TOKEN"] = "s3cret-441"
	secrets.Save(c.env.Paths.Secrets, c.env.Paths.Key, sec)
	st, err := cli.LoadState(c.env)
	if err != nil {
		t.Fatal(err)
	}
	return c, "https://whoami." + st.Box.Domain + "/admin"
}

func TestTheRecoverySheetListsTheLoginsWithoutTheirPasswords(t *testing.T) {
	c, url := loginBox(t)
	out := captureStdout(t, func() {
		if rc := backup.RecoverySheet(context.Background(), c.env, nil); rc != cli.ExitOK {
			t.Errorf("rc %d", rc)
		}
	})
	for _, want := range []string{"Anmeldungen", "whoami", "chef", url, "--vaultwarden"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the sheet lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s3cret-441") {
		t.Fatalf("the sheet prints a login's password:\n%s", out)
	}
}

func TestTheVaultwardenExportIsABitwardenImportFileOnlyTheOwnerReads(t *testing.T) {
	c, url := loginBox(t)
	file := filepath.Join(t.TempDir(), "laib-logins.json")
	for _, env := range []*cliEnv{{Paths: c.env.Paths, Actor: "ops"}, {Paths: c.env.Paths, Actor: "human:core", MCP: true}} {
		if rc := backup.RecoverySheet(context.Background(), env, []string{"--vaultwarden", file}); rc == cli.ExitOK {
			t.Fatalf("%s (mcp %v) got the export", env.Actor, env.MCP)
		}
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("a refused export wrote the file")
	}
	out := captureStdout(t, func() {
		if rc := backup.RecoverySheet(context.Background(), c.env, []string{"--vaultwarden", file}); rc != cli.ExitOK {
			t.Errorf("rc %d", rc)
		}
	})
	if strings.Contains(out, "s3cret-441") {
		t.Fatalf("the export printed a password:\n%s", out)
	}
	fi, err := os.Stat(file)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the export must be a 0600 file: %v %v", err, fi)
	}
	var exp struct {
		Encrypted bool `json:"encrypted"`
		Folders   []struct{ ID, Name string }
		Items     []struct {
			FolderID string `json:"folderId"`
			Type     int    `json:"type"`
			Name     string `json:"name"`
			Notes    string `json:"notes"`
			Login    struct {
				Username string `json:"username"`
				Password string `json:"password"`
				URIs     []struct {
					URI string `json:"uri"`
				} `json:"uris"`
			} `json:"login"`
		} `json:"items"`
	}
	b, _ := os.ReadFile(file)
	if err := json.Unmarshal(b, &exp); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	if exp.Encrypted || len(exp.Folders) != 1 || len(exp.Items) != 1 {
		t.Fatalf("want one folder and one login, unencrypted (Bitwarden's import format): %s", b)
	}
	it := exp.Items[0]
	if it.Type != 1 || it.FolderID != exp.Folders[0].ID || it.Login.Username != "chef" || it.Login.Password != "s3cret-441" || len(it.Login.URIs) != 1 || it.Login.URIs[0].URI != url || !strings.Contains(it.Notes, "WHOAMI_TOKEN") {
		t.Fatalf("the login item is wrong: %+v", it)
	}
	// never over a file that is there: a second run refuses and leaves it
	os.WriteFile(file, []byte("mine"), 0o600)
	if rc := backup.RecoverySheet(context.Background(), c.env, []string{"--vaultwarden", file}); rc == cli.ExitOK {
		t.Fatal("the export overwrote an existing file")
	}
	if b, _ := os.ReadFile(file); string(b) != "mine" {
		t.Fatal("the existing file was changed")
	}
}
