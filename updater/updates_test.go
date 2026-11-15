package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeRegistry answers the Docker registry v2 tag list behind the
// anonymous token dance every public registry speaks.
func fakeRegistry(t *testing.T, tags map[string][]string) {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			json.NewEncoder(w).Encode(map[string]string{"token": "t-" + r.URL.Query().Get("scope")})
			return
		}
		repo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/"), "/tags/list")
		if r.Header.Get("Authorization") != "Bearer t-repository:"+repo+":pull" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="registry",scope="repository:`+repo+`:pull"`)
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags[repo]})
	}))
	t.Cleanup(srv.Close)
	serve(t, srv)
}

func serve(t *testing.T, srv *httptest.Server) {
	real := RegistryURL
	RegistryURL = func(string) string { return srv.URL }
	t.Cleanup(func() { RegistryURL = real })
}

func rows(images ...Pinned) map[string]Row {
	got := map[string]Row{}
	for _, u := range Updates(context.Background(), &Env{}, images, false) {
		got[u.Service] = u
	}
	return got
}

// #287: which pinned images have a newer patch or minor release — read
// from the registry, the tag's own shape kept (v1.10.1 → v1.10.3, v1.11.2);
// a floating tag (latest, a bare major) has none.
func TestImageUpdatesFindPatchAndMinor(t *testing.T) {
	fakeRegistry(t, map[string][]string{
		"traefik/whoami":         {"v1.9.0", "v1.10.1", "v1.10.3", "v1.10.3-rc1", "v1.11.2", "v1.11.0", "v2.0.0", "latest"},
		"library/redis":          {"6.2", "6.4", "7.0", "6.2-alpine"},
		"advplyr/audiobookshelf": {"2.1.0"},
	})
	got := rows(Pinned{Service: "whoami", Key: "IMAGE", Value: "docker.io/traefik/whoami:v1.10.1"}, Pinned{Service: "redis", Key: "IMAGE", Value: "redis:6.2"},
		Pinned{Service: "abs", Key: "IMAGE", Value: "ghcr.io/advplyr/audiobookshelf:latest"}, Pinned{Service: "caddy", Key: "IMAGE", Value: "ghcr.io/mdopp/laib-caddy:2"},
		Pinned{Service: "nothing", Key: "IMAGE", Value: "not an image"})
	if u := got["whoami"]; u.Current != "v1.10.1" || u.Patch != "v1.10.3" || u.Minor != "v1.11.2" || u.Key != "IMAGE" || u.Image != "docker.io/traefik/whoami" {
		t.Fatalf("whoami: %+v", u)
	}
	if u := got["redis"]; u.Patch != "" || u.Minor != "6.4" {
		t.Fatalf("redis: %+v", u)
	}
	for _, s := range []string{"abs", "caddy"} {
		if u, ok := got[s]; ok && (u.Patch != "" || u.Minor != "") {
			t.Fatalf("%s floats and got an update: %+v", s, u)
		}
	}
	if _, ok := got["nothing"]; ok {
		t.Fatal("a value that names no image got a row")
	}
}

// #528: a floating tag is compared by the registry's digest against the
// local image's RepoDigests (the kernel hands them over as Have), a pin
// against its digest. A floating tag the box runs as the registry serves it
// carries that digest as Pin, for the proposal that pins it (ADR 0034).
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
	serve(t, srv)
	got := rows(
		Pinned{Service: "home-assistant", Key: "IMAGE", Value: "ghcr.io/home-assistant/home-assistant:stable", Have: `["ghcr.io/home-assistant/home-assistant@sha256:ha1"]`},
		Pinned{Service: "frage", Key: "IMAGE", Value: "ghcr.io/mdopp/laib-frage:latest", Have: `["ghcr.io/mdopp/laib-frage@sha256:fr0-instance","ghcr.io/mdopp/laib-frage@sha256:fr1"]`},
		Pinned{Service: "llama", Key: "IMAGE", Value: "ghcr.io/ggml-org/llama.cpp:server-cuda-b1@sha256:ll1"},
		Pinned{Service: "paperless", Key: "IMAGE", Value: "docker.io/library/postgres:16@sha256:pg1"})
	if u := got["home-assistant"]; u.Digest != "sha256:ha2" || u.Pin != "" || u.Current != "stable" || u.Image != "ghcr.io/home-assistant/home-assistant" {
		t.Fatalf("a data service's floating tag moved: a proposal by digest: %+v", u)
	}
	if u := got["frage"]; u.Digest != "" || u.Pin != "sha256:fr1" {
		t.Fatalf("frage runs the tag's digest (among its RepoDigests): current, and pinned to it: %+v", u)
	}
	if u := got["llama"]; u.Digest != "sha256:ll2" || u.Pin != "" {
		t.Fatalf("a pin whose tag moved: %+v", u)
	}
	if u, ok := got["paperless"]; !ok || u.Digest != "" || u.Pin != "" || u.Error != "" {
		t.Fatalf("a pin that is the tag's digest is current: %+v", u)
	}
}

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
	serve(t, srv)
	got := rows(Pinned{Service: "reitti", Key: "IMAGE", Value: "docker.io/dedicatedcode/reitti:5.9.1", Have: `["docker.io/dedicatedcode/reitti@sha256:r591"]`},
		Pinned{Service: "pinned", Key: "IMAGE", Value: "docker.io/dedicatedcode/reitti:5.9.1@sha256:r591"})
	if u := got["reitti"]; u.Pin != "sha256:r591" || u.Digest != "" || u.Patch != "5.9.2" || u.PatchDigest != "sha256:r592" {
		t.Fatalf("a version tag the box runs is pinned to that digest, its patch carries its own: %+v", u)
	}
	if u := got["pinned"]; u.Pin != "" || u.Digest != "" || u.PatchDigest != "sha256:r592" {
		t.Fatalf("a current pin needs no pin card; its patch still carries the digest: %+v", u)
	}
}

// #368: the host OS image is pinned in the state (host/image: "# tag: <tag>"
// and the rebase reference by digest); the row says when the tag points to
// a newer digest.
func TestUpdatesSeeANewerHostImageDigest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/ublue-os/ucore-minimal/manifests/stable-nvidia" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:b111")
	}))
	t.Cleanup(srv.Close)
	serve(t, srv)
	if _, ok := Host(context.Background(), ""); ok {
		t.Fatal("no host/image: no host row")
	}
	file := func(digest string) string {
		return "# tag: stable-nvidia\nostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal@" + digest + "\n"
	}
	u, ok := Host(context.Background(), file("sha256:a287"))
	if !ok || u.Service != "host" || u.Key != "stable-nvidia" || u.Image != "ostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal" || u.Current != "sha256:a287" || u.Patch != "sha256:b111" {
		t.Fatalf("host row: %+v", u)
	}
	if u, _ := Host(context.Background(), file("sha256:b111")); u.Patch != "" {
		t.Fatalf("the pinned digest is the tag's: no update, got %+v", u)
	}
}
