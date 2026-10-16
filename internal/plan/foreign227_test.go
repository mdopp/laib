package plan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/state"
)

// kraftwerkBox is the basic golden state (auth in a pod, dataDir
// /mnt/data/stacks as on the Kraftwerk) plus the file-share built-in, and a
// Quadlet root with ServiceBay's files in it (#227).
func kraftwerkBox(t *testing.T, root map[string]string) (*state.State, boxfs.Paths) {
	t.Helper()
	home := t.TempDir()
	repo := filepath.Join(home, "state")
	copyDir(t, "../../testdata/golden/basic/state", repo)
	fs := filepath.Join(repo, "services", "file-share")
	copyDir(t, "../../templates/file-share", fs)
	b, _ := os.ReadFile(filepath.Join(fs, "values.example.yml"))
	os.WriteFile(filepath.Join(fs, "values.yml"), b, 0o644)
	box, _ := os.ReadFile(filepath.Join(repo, "box.yml"))
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte(strings.Replace(string(box), "hostNetwork: [caddy]", "hostNetwork: [caddy, file-share]", 1)), 0o644)
	st, err := state.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	paths := boxfs.Paths{Home: home, State: repo, Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r")}
	os.MkdirAll(paths.Quadlet, 0o755)
	for name, body := range root {
		os.WriteFile(filepath.Join(paths.Quadlet, name), []byte(body), 0o644)
	}
	return st, paths
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func conflictsFor(t *testing.T, root map[string]string) []string {
	t.Helper()
	st, paths := kraftwerkBox(t, root)
	p, err := Compute(context.Background(), st, nil, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	return p.Conflicts
}

func hasConflict(cs []string, service, file string) bool {
	for _, c := range cs {
		if strings.HasPrefix(c, service+":") && strings.Contains(c, file) {
			return true
		}
	}
	return false
}

// ServiceBay's auth.kube runs Laib's auth whatever units Laib generates for
// it (auth-pod, auth-lldap, auth-authelia): the base name is the service.
func TestServiceBayKubeOfAPodServiceIsAConflict(t *testing.T) {
	kube := "[Kube]\nYaml=%s.yml\nAutoUpdate=registry\n\n[Install]\nWantedBy=default.target\n"
	for _, name := range []string{"auth", "file-share"} {
		root := map[string]string{name + ".kube": strings.ReplaceAll(kube, "%s", name), name + ".yml": "apiVersion: v1\nkind: Pod\n"}
		st, paths := kraftwerkBox(t, root)
		p, err := Compute(context.Background(), st, nil, paths, "")
		if err != nil {
			t.Fatal(err)
		}
		if !hasConflict(p.Conflicts, name, name+".kube") {
			t.Fatalf("%s.kube is not a KONFLIKT for %s: %v", name, name, p.Conflicts)
		}
		for _, u := range p.Unmanaged {
			if strings.HasPrefix(u, name+".") {
				t.Fatalf("%s is reported as merely unmanaged", u)
			}
		}
	}
}

// A foreign unit under another name still collides when it publishes a host
// port the service declares (claude-dev's 2222 vs the Werkstatt's) or mounts
// one of the service's own data paths.
func TestForeignUnitOnAServicesPortOrDataIsAConflict(t *testing.T) {
	cs := conflictsFor(t, map[string]string{
		"ldap-proxy.container": "[Container]\nImage=x\nPublishPort=127.0.0.1:3890:3890\n",
		"sync.kube":            "[Kube]\nYaml=sync.yml\n",
		"sync.yml":             "spec:\n  containers:\n    - name: s\n      ports:\n        - containerPort: 8384\n          hostPort: 8384\n",
		"lldap-backup.kube":    "[Kube]\nYaml=lldap-backup.yml\n",
		"lldap-backup.yml":     "spec:\n  volumes:\n    - name: db\n      hostPath:\n        path: /mnt/data/stacks/auth/lldap\n        type: Directory\n",
		"fb.container":         "[Container]\nImage=x\nVolume=/mnt/data/stacks/file-share/filebrowser:/database:Z\n",
	})
	for _, want := range [][2]string{{"auth", "ldap-proxy.container"}, {"file-share", "sync.kube"}, {"auth", "lldap-backup.kube"}, {"file-share", "fb.container"}} {
		if !hasConflict(cs, want[0], want[1]) {
			t.Errorf("%s is not a KONFLIKT for %s: %v", want[1], want[0], cs)
		}
	}
}

// The shared tree is meant to be shared (ADR 0017): ServiceBay's Immich and
// Jellyfin reading file-share/data do not block file-share, and a foreign
// unit on unrelated ports and paths is unmanaged, not a conflict.
func TestForeignUnitOnTheSharedTreeIsNoConflict(t *testing.T) {
	cs := conflictsFor(t, map[string]string{
		"immich.kube":     "[Kube]\nYaml=immich.yml\n",
		"immich.yml":      "spec:\n  containers:\n    - name: i\n      ports:\n        - hostPort: 2283\n  volumes:\n    - name: photos\n      hostPath:\n        path: /mnt/data/stacks/file-share/data\n",
		"media.container": "[Container]\nImage=x\nPublishPort=8096:8096\nVolume=/mnt/data/stacks/file-share/data:/media:ro\nVolume=/mnt/data/stacks/media/jellyfin-config:/config:Z\n",
	})
	if len(cs) != 0 {
		t.Fatalf("%v", cs)
	}
}
