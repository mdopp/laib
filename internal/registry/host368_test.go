package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #368: the host OS image is pinned in the state (host/image: "# tag: <tag>"
// and the rebase reference by digest). laib updates says when the tag points
// to a newer digest; the Operator's weekly skill proposes it, a human
// approves, and the box stages and reboots into it through the fleet lock.
func TestUpdatesSeeANewerHostImageDigest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/ublue-os/ucore-minimal/manifests/stable-nvidia" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:b111")
	}))
	t.Cleanup(srv.Close)
	real := RegistryURL
	RegistryURL = func(string) string { return srv.URL }
	t.Cleanup(func() { RegistryURL = real })
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "host"), 0o755)
	st := &state.State{Dir: dir, Services: map[string]*state.Service{}}
	if ups := ImageUpdates(context.Background(), st); len(ups) != 0 {
		t.Fatalf("no host/image: no host row, got %+v", ups)
	}
	pin := func(digest string) {
		os.WriteFile(filepath.Join(dir, "host", "image"), []byte("# tag: stable-nvidia\nostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal@"+digest+"\n"), 0o644)
	}
	pin("sha256:a287")
	ups := ImageUpdates(context.Background(), st)
	if len(ups) != 1 {
		t.Fatalf("want one host row, got %+v", ups)
	}
	u := ups[0]
	if u.Service != "host" || u.Key != "stable-nvidia" || u.Image != "ostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal" || u.Current != "sha256:a287" || u.Patch != "sha256:b111" {
		t.Fatalf("host row: %+v", u)
	}
	pin("sha256:b111")
	if ups := ImageUpdates(context.Background(), st); len(ups) != 1 || ups[0].Patch != "" {
		t.Fatalf("the pinned digest is the tag's: no update, got %+v", ups)
	}
}
