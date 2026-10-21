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

// #528: `laib updates` saw only version tags — three rows on the Kraftwerk,
// while home-assistant:stable, vaultwarden:latest and the digest pins
// (Authelia, Paperless …) went unchecked. A floating tag is compared by the
// registry's digest against the local image's RepoDigests, a pin against its
// digest; Auto says which the nightly run takes. Against a fake registry
// that answers the manifest HEAD-less GET with Docker-Content-Digest.
func TestUpdatesCompareTheRegistryDigestOfTagsAndPins(t *testing.T) {
	digests := map[string]string{"/v2/home-assistant/home-assistant/manifests/stable": "sha256:ha2", "/v2/mdopp/laib-frage/manifests/latest": "sha256:fr1",
		"/v2/ggml-org/llama.cpp/manifests/server-cuda-b1": "sha256:ll2", "/v2/library/postgres/manifests/16": "sha256:pg1"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d, ok := digests[r.URL.Path]; ok {
			w.Header().Set("Docker-Content-Digest", d)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/tags/list") {
			json.NewEncoder(w).Encode(map[string]any{"tags": []string{}})
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)
	real := RegistryURL
	RegistryURL = func(string) string { return srv.URL }
	t.Cleanup(func() { RegistryURL = real })
	realRun := sysd.Run
	t.Cleanup(func() { sysd.Run = realRun })
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		if name == "podman" && args[0] == "image" { // the box has the old stable and the current latest
			return map[string]string{"ghcr.io/home-assistant/home-assistant:stable": `["ghcr.io/home-assistant/home-assistant@sha256:ha1"]`,
				"ghcr.io/mdopp/laib-frage:latest": `["ghcr.io/mdopp/laib-frage@sha256:fr0-instance","ghcr.io/mdopp/laib-frage@sha256:fr1"]`}[args[len(args)-1]], nil
		}
		return "", nil
	}
	svc := func(name, img, extra string) *state.Service {
		return &state.Service{Name: name, Values: map[string]state.Value{"IMAGE": {Literal: img}},
			Units: map[string]string{name + ".container": "[Container]\nImage=${IMAGE}\n" + extra + "\n"}}
	}
	dir := t.TempDir()
	st := &state.State{Dir: dir, Services: map[string]*state.Service{
		"home-assistant": svc("home-assistant", "ghcr.io/home-assistant/home-assistant:stable", ""),
		"frage":          svc("frage", "ghcr.io/mdopp/laib-frage:latest", "AutoUpdate=registry"),
		"llama":          svc("llama", "ghcr.io/ggml-org/llama.cpp:server-cuda-b1@sha256:ll1", ""),
		"paperless":      svc("paperless", "docker.io/library/postgres:16@sha256:pg1", ""),
	}}
	got := map[string]ImageUpdate{}
	for _, u := range ImageUpdates(context.Background(), st) {
		got[u.Service] = u
	}
	if u := got["home-assistant"]; u.Digest != "sha256:ha2" || u.Auto || u.Current != "stable" || u.Image != "ghcr.io/home-assistant/home-assistant" {
		t.Fatalf("a data service's floating tag moved: a proposal by digest: %+v", u)
	}
	if u := got["frage"]; u.Digest != "" || !u.Auto {
		t.Fatalf("frage runs the tag's digest (among its RepoDigests): current, and the night's: %+v", u)
	}
	if u := got["llama"]; u.Digest != "sha256:ll2" || u.Auto {
		t.Fatalf("a pin whose tag moved: %+v", u)
	}
	if u, ok := got["paperless"]; !ok || u.Digest != "" || u.Error != "" {
		t.Fatalf("a pin that is the tag's digest is current: %+v", u)
	}

}
