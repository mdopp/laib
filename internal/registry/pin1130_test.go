package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// Ablauf L-53. #1130: seven values on the box carried a version tag and no digest
// (reitti:5.9.1 after a patch proposal, immich-public-proxy:4.1.2, …), and
// „Images festhalten“ never offered them: only a floating tag got its Pin.
// A version tag the box runs gets it too, and a patch release comes with its
// digest, so the proposal that takes it keeps the value pinned.
func TestAVersionTagIsPinnedAndAPatchKeepsItsDigest(t *testing.T) {
	digests := map[string]string{"/v2/dedicatedcode/reitti/manifests/5.9.1": "sha256:r591", "/v2/dedicatedcode/reitti/manifests/5.9.2": "sha256:r592"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d, ok := digests[r.URL.Path]; ok {
			w.Header().Set("Docker-Content-Digest", d)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/tags/list") {
			json.NewEncoder(w).Encode(map[string]any{"tags": []string{"5.9.0", "5.9.1", "5.9.2"}})
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)
	real, realRun := RegistryURL, sysd.Run
	RegistryURL = func(string) string { return srv.URL }
	t.Cleanup(func() { RegistryURL, sysd.Run = real, realRun })
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		if name == "podman" && args[0] == "image" {
			return `["docker.io/dedicatedcode/reitti@sha256:r591"]`, nil
		}
		return "", nil
	}
	val := func(v string) map[string]state.Value { return map[string]state.Value{"IMAGE": {Literal: v}} }
	st := &state.State{Dir: t.TempDir(), Services: map[string]*state.Service{
		"reitti": {Name: "reitti", Values: val("docker.io/dedicatedcode/reitti:5.9.1")},
		"pinned": {Name: "pinned", Values: val("docker.io/dedicatedcode/reitti:5.9.1@sha256:r591")},
	}}
	got := map[string]ImageUpdate{}
	for _, u := range ImageUpdates(context.Background(), st) {
		got[u.Service] = u
	}
	if u := got["reitti"]; u.Pin != "sha256:r591" || u.Digest != "" || u.Patch != "5.9.2" || u.PatchDigest != "sha256:r592" {
		t.Fatalf("a version tag the box runs is pinned to that digest, its patch carries its own: %+v", u)
	}
	if u := got["pinned"]; u.Pin != "" || u.Digest != "" || u.PatchDigest != "sha256:r592" {
		t.Fatalf("a current pin needs no pin card; its patch still carries the digest: %+v", u)
	}
}
