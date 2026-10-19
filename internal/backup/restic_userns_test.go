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

// #391: the off-box repo is a restic copy from the local repo, which reads no
// service files: restic runs as core, not inside podman unshare, because its
// rclone backend is itself a podman container (the Fritz!Box speaks FTP, not
// sftp). The backend's settings and its password reach it as environment.
func TestTheOffsiteRepoRunsPlainWithItsEnvironment(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	for _, p := range []string{"podman", "restic"} {
		fake := "#!/bin/sh\necho \"" + p + " $* NAS=$RCLONE_CONFIG_NAS_HOST PASS=$RCLONE_CONFIG_NAS_PASS FROM=$RESTIC_FROM_PASSWORD\" >> " + log + "\n"
		if err := os.WriteFile(filepath.Join(bin, p), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	st := &state.State{}
	st.Box.Backup = state.Backup{Repo: "rclone:nas:laib", Plain: true, Env: map[string]string{"RCLONE_CONFIG_NAS_HOST": "192.168.178.1", "RCLONE_CONFIG_NAS_PASS": "obscured"}}
	if _, err := restic(context.Background(), st, "pw", "copy", "--from-repo", "/r"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	got := strings.TrimSpace(string(b))
	want := "restic -o rclone.program=podman -o rclone.args=run --rm -i --network host --env RCLONE_* " + RcloneImage + " serve restic --stdio copy --from-repo /r NAS=192.168.178.1 PASS=obscured FROM=pw"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
