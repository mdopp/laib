package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/sysd"
)

// laib#1021: Haus is renamed Homes. The running service moves in one
// change: services/haus goes, services/homes comes, and what the family
// made stays — the paired phones in the data dir, the keys the owner typed
// in. `was:` on a data path and on a secret carries them: the old units stop
// first, then the old place is renamed to the new one before anything mounts
// it, and the new secret takes the old one's value instead of a fresh one.
func TestARenamedServiceTakesItsDataAndSecretsAlong(t *testing.T) {
	ctx := context.Background()
	env, data := testBox(t)
	write := func(svc string, files map[string]string) {
		t.Helper()
		dir := filepath.Join(env.Paths.State, "services", svc)
		os.MkdirAll(dir, 0o755)
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
			t.Fatal(err)
		}
	}
	write("alt", map[string]string{
		"laib.yml":      "name: alt\nsecrets:\n  - name: ALT_KEY\ndata:\n  - path: ${DATA_DIR}/alt\n",
		"values.yml":    "IMAGE: docker.io/traefik/whoami:v1.10\nKEY: !secret ALT_KEY\n",
		"alt.container": "[Container]\nImage=${IMAGE}\nEnvironment=KEY=${KEY}\nVolume=${DATA_DIR}/alt:/data:Z\n\n[Install]\nWantedBy=default.target\n",
	})
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	if code := apply.ApplyLocked(ctx, env, st, "", "alt"); code != cli.ExitOK {
		t.Fatalf("first apply: exit %d", code)
	}
	if err := os.WriteFile(filepath.Join(data, "alt", "devices.json"), []byte(`{"phone":"hash"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil || sec["ALT_KEY"] == "" {
		t.Fatalf("no ALT_KEY after the first apply: %v", err)
	}
	old := sec["ALT_KEY"]

	neu := map[string]string{
		"laib.yml":      "name: neu\nsecrets:\n  - name: NEU_KEY\n    was: ALT_KEY\ndata:\n  - path: ${DATA_DIR}/neu\n    was: ${DATA_DIR}/alt\n",
		"values.yml":    "IMAGE: docker.io/traefik/whoami:v1.10\nKEY: !secret NEU_KEY\n",
		"neu.container": "[Container]\nImage=${IMAGE}\nEnvironment=KEY=${KEY}\nVolume=${DATA_DIR}/neu:/data:Z\n\n[Install]\nWantedBy=default.target\n",
	}
	// a former place another service still keeps is its data: the load refuses
	write("neu", neu)
	if _, err := cli.LoadState(env); err == nil || !strings.Contains(err.Error(), "kept by no service (alt)") {
		t.Fatalf("neu taking alt's live data dir loaded: %v", err)
	}

	// the rename: alt goes in the same change
	if err := os.RemoveAll(filepath.Join(env.Paths.State, "services", "alt")); err != nil {
		t.Fatal(err)
	}
	write("neu", neu)
	st, err = cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	stoppedBeforeMove := false
	prev := sysd.Run
	t.Cleanup(func() { sysd.Run = prev })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && strings.Join(args, " ") == "--user stop alt.service" {
			_, errAlt := os.Stat(filepath.Join(data, "alt", "devices.json"))
			_, errNeu := os.Stat(filepath.Join(data, "neu"))
			stoppedBeforeMove = errAlt == nil && os.IsNotExist(errNeu)
		}
		return prev(ctx, name, args...)
	}
	if code := apply.ApplyLocked(ctx, env, st, "", "alt heißt jetzt neu"); code != cli.ExitOK {
		t.Fatalf("the rename's apply: exit %d", code)
	}
	if !stoppedBeforeMove {
		t.Fatal("alt's unit was not stopped while its data was still in place")
	}
	b, err := os.ReadFile(filepath.Join(data, "neu", "devices.json"))
	if err != nil || string(b) != `{"phone":"hash"}` {
		t.Fatalf("the paired phones did not move to neu: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(data, "alt")); !os.IsNotExist(err) {
		t.Fatalf("the former place is still there: %v", err)
	}
	sec, err = secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		t.Fatal(err)
	}
	if sec["NEU_KEY"] != old {
		t.Fatal("NEU_KEY is not ALT_KEY's value: the owner would type the key in again")
	}

	// a second apply leaves it alone: the new place holds the data now
	if code := apply.ApplyLocked(ctx, env, st, "", "again"); code != cli.ExitOK {
		t.Fatalf("second apply: exit %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "neu", "devices.json")); string(b) != `{"phone":"hash"}` {
		t.Fatalf("the second apply touched the moved data: %q", b)
	}
}
