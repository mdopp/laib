package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

// #959: a digest proposal's card says the version, not the digest — read
// from the registry: the manifest (an index's for this platform), then the
// config blob's labels, the OCI one first, else a vendor's. Best effort: a
// registry that fails or is slow gives "", and the card says it without.
func TestImageVersionReadsTheLabelsBehindADigest(t *testing.T) {
	slow := make(chan struct{})
	docs := map[string]any{
		// home-assistant: a multi-arch index; the other platform first, so the pick is shown
		"/v2/home-assistant/home-assistant/manifests/sha256:new": map[string]any{"mediaType": "application/vnd.oci.image.index.v1+json", "manifests": []any{
			map[string]any{"digest": "sha256:other", "platform": map[string]string{"os": "linux", "architecture": "s390x"}},
			map[string]any{"digest": "sha256:mine", "platform": map[string]string{"os": "linux", "architecture": runtime.GOARCH}}}},
		"/v2/home-assistant/home-assistant/manifests/sha256:mine":  map[string]any{"config": map[string]string{"digest": "sha256:cfg"}},
		"/v2/home-assistant/home-assistant/manifests/sha256:other": map[string]any{"config": map[string]string{"digest": "sha256:wrong"}},
		"/v2/home-assistant/home-assistant/blobs/sha256:cfg":       map[string]any{"config": map[string]any{"Labels": map[string]string{"io.hass.version": "2026.10.4"}}},
		"/v2/home-assistant/home-assistant/blobs/sha256:wrong":     map[string]any{"config": map[string]any{"Labels": map[string]string{"io.hass.version": "s390x"}}},
		// a plain manifest by tag, the OCI label before the vendor's
		"/v2/library/redis/manifests/7":    map[string]any{"config": map[string]string{"digest": "sha256:r"}},
		"/v2/library/redis/blobs/sha256:r": map[string]any{"config": map[string]any{"Labels": map[string]string{"org.label-schema.version": "x", "org.opencontainers.image.version": "7.4.1"}}},
		// no labels at all
		"/v2/a/nolabel/manifests/sha256:n": map[string]any{"config": map[string]string{"digest": "sha256:c"}},
		"/v2/a/nolabel/blobs/sha256:c":     map[string]any{"config": map[string]any{}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/a/slow/manifests/sha256:s" {
			<-slow
			return
		}
		if d, ok := docs[r.URL.Path]; ok {
			json.NewEncoder(w).Encode(d)
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(slow) }) // before srv.Close, which waits for the slow handler
	real, realT := RegistryURL, VersionTimeout
	RegistryURL, VersionTimeout = func(string) string { return srv.URL }, 200*time.Millisecond
	t.Cleanup(func() { RegistryURL, VersionTimeout = real, realT })
	for image, want := range map[string]string{
		"ghcr.io/home-assistant/home-assistant:stable@sha256:new": "2026.10.4",
		"redis:7":                        "7.4.1",
		"x.io/a/nolabel:latest@sha256:n": "",
		"x.io/a/gone:latest@sha256:g":    "",
		"x.io/a/slow:latest@sha256:s":    "",
		"not an image":                   "",
	} {
		start := time.Now()
		if got := ImageVersion(image); got != want {
			t.Errorf("%s: %q, want %q", image, got, want)
		}
		if time.Since(start) > 2*time.Second {
			t.Errorf("%s: took %s, the timeout is %s", image, time.Since(start), VersionTimeout)
		}
	}
}
