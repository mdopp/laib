package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
)

// #665: the Inbox rotated MQTT_PASSWORD and SAMBA_PASSWORD, which devices
// hold: the door lock lost MQTT. `laib secret zurueck <NAME>` puts back the
// value the secret had before its last rotation, from the state repo's
// history, and applies the services that use it; humans only, never the
// value in an output or the journal.

func secretRun(t *testing.T, b *rbox, args ...string) (int, string) {
	t.Helper()
	var w bytes.Buffer
	b.env.Out, b.env.ErrOut = &w, &w
	return cmdSecret(context.Background(), b.env, args), w.String()
}

func TestZurueckPutsBackTheValueBeforeTheLastRotation(t *testing.T) {
	b := newRbox(t)
	sec, _ := secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	sec["WHOAMI_TOKEN"] = "the-devices-hold-665"
	secrets.Save(b.env.Paths.Secrets, b.env.Paths.Key, sec)
	proposal.CommitSecrets(context.Background(), b.env.Paths.State, "set WHOAMI_TOKEN")
	if rc, out := secretRun(t, b, "zurueck", "WHOAMI_TOKEN"); rc == cli.ExitOK {
		t.Fatalf("zurück without a rotation ended green: %s", out)
	}
	if rc, out := secretRun(t, b, "rotate", "WHOAMI_TOKEN"); rc != cli.ExitOK {
		t.Fatalf("rotate rc %d: %s", rc, out)
	}
	rotated, _ := secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	sec, _ = secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	sec["USAGE_METRICS_TOKEN"] = "set-after-665" // another secret changed since: it stays
	secrets.Save(b.env.Paths.Secrets, b.env.Paths.Key, sec)
	proposal.CommitSecrets(context.Background(), b.env.Paths.State, "set USAGE_METRICS_TOKEN")

	b.env.Actor = "claude"
	if rc, _ := secretRun(t, b, "zurueck", "WHOAMI_TOKEN"); rc == cli.ExitOK {
		t.Fatal("an agent put a secret back")
	}
	b.env.Actor = "human:test"
	rc, out := secretRun(t, b, "zurueck", "WHOAMI_TOKEN")
	if rc != cli.ExitOK {
		t.Fatalf("zurück rc %d: %s", rc, out)
	}
	now, _ := secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	if now["WHOAMI_TOKEN"] != "the-devices-hold-665" || now["USAGE_METRICS_TOKEN"] != "set-after-665" {
		t.Fatal("not the value before the rotation, or another secret went back too")
	}
	if !strings.Contains(out, "whoami") {
		t.Fatalf("the services that use it are not named: %s", out)
	}
	j, _ := filepath.Glob(filepath.Join(b.env.Paths.Journal, "*"))
	found := false
	for _, f := range j {
		raw, _ := os.ReadFile(f)
		found = found || strings.Contains(string(raw), "secret zurück WHOAMI_TOKEN")
	}
	if !found {
		t.Fatal("no journal line secret zurück WHOAMI_TOKEN")
	}
	noValueAnywhere(t, b, out, "the-devices-hold-665", rotated["WHOAMI_TOKEN"])
}

func TestZurueckRefusesADataBoundSecret(t *testing.T) {
	b := newRbox(t)
	rotatingAuth(t, b) // LLDAP_ADMIN_PASSWORD: rotated inside the app, and back on red
	if rc, out := secretRun(t, b, "zurueck", "LLDAP_ADMIN_PASSWORD"); rc == cli.ExitOK || !strings.Contains(out, "App") {
		t.Fatalf("a data-bound secret was put back: rc %d: %s", rc, out)
	}
}

// Jellyfin: media owns JELLYFIN_ADMIN_PASSWORD, music-assistant declares it
// external as a consumer; the owner's declaration decides, and the consumer
// applies with the new value.
func TestRotateASecretOneServiceOwnsAndOthersConsume(t *testing.T) {
	b := newRbox(t)
	decl := filepath.Join(b.env.Paths.State, "services", "usage-metrics", "laib.yml")
	raw, _ := os.ReadFile(decl)
	os.WriteFile(decl, []byte(strings.Replace(string(raw), "secrets:\n", "secrets:\n  - name: WHOAMI_TOKEN\n    external: true\n", 1)), 0o644)
	rc, out := secretRun(t, b, "rotate", "WHOAMI_TOKEN")
	if rc != cli.ExitOK || !strings.Contains(out, "usage-metrics") || !strings.Contains(out, "whoami") {
		t.Fatalf("an owned secret with an external consumer: rc %d: %s", rc, out)
	}
	if rc, out := secretRun(t, b, "rotate", "DNS_API_TOKEN"); rc == cli.ExitOK || !strings.Contains(out, "external") {
		t.Fatalf("a secret only external declarations name rotated: %s", out)
	}
}
