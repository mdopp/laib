package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// A database's files belong to the container's user, which rootless podman
// maps onto a subuid of core: restic as plain core cannot read them
// ("open …/pgtest/pgdata: permission denied", vm-install run 38308352527),
// so every backup silently misses the databases and a restore cannot put
// their owners back. restic runs inside podman's user namespace.
func TestResticRunsInPodmansUserNamespace(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	fake := "#!/bin/sh\necho \"podman $* RESTIC_REPOSITORY=$RESTIC_REPOSITORY RESTIC_PASSWORD=$RESTIC_PASSWORD\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	st := &state.State{}
	st.Box.Backup.Repo = "/mnt/backup/restic"
	if _, err := restic(context.Background(), st, "pw", "snapshots", "--json"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	got := strings.TrimSpace(string(b))
	if !strings.HasPrefix(got, "podman unshare restic snapshots --json") {
		t.Fatalf("restic must run as `podman unshare restic …`, got %q", got)
	}
	if !strings.Contains(got, "RESTIC_REPOSITORY=/mnt/backup/restic") || !strings.Contains(got, "RESTIC_PASSWORD=pw") {
		t.Fatalf("repository and password reach restic through the environment, never argv: %q", got)
	}
}
