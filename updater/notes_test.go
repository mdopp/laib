package updater

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
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
	serve(t, srv)
	realT := VersionTimeout
	VersionTimeout = 200 * time.Millisecond
	t.Cleanup(func() { VersionTimeout = realT })
	for image, want := range map[string]string{
		"ghcr.io/home-assistant/home-assistant:stable@sha256:new": "2026.10.4",
		"redis:7":                        "7.4.1",
		"x.io/a/nolabel:latest@sha256:n": "",
		"x.io/a/gone:latest@sha256:g":    "",
		"x.io/a/slow:latest@sha256:s":    "",
		"not an image":                   "",
	} {
		start := time.Now()
		out, code, _ := Run(context.Background(), &Env{}, Call{Argv: []string{"version", image}})
		if out != want || code != 0 {
			t.Errorf("%s: %q (%d), want %q", image, out, code, want)
		}
		if time.Since(start) > 2*time.Second {
			t.Errorf("%s: took %s, the timeout is %s", image, time.Since(start), VersionTimeout)
		}
	}
}

// ADR 0034 --notes: a new digest carries both versions (the running one from
// the local labels the kernel hands over when the value floats), the GitHub
// repository its label names, what changed there, and for our own image
// whether its signature holds — checked by cosign as the owner's CI on main
// signed it; a pin of what runs is not checked.
func TestNotesDescribeANewDigest(t *testing.T) {
	docs := map[string]any{
		"/v2/adguard/adguardhome/manifests/sha256:a2": map[string]any{"config": map[string]string{"digest": "sha256:ca"}},
		"/v2/adguard/adguardhome/blobs/sha256:ca": map[string]any{"config": map[string]any{"Labels": map[string]string{
			"org.opencontainers.image.version": "v0.107.56", "org.opencontainers.image.source": "https://github.com/AdguardTeam/AdGuardHome", "org.opencontainers.image.revision": "bbbbbbb"}}},
	}
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d, ok := docs[r.URL.Path]; ok {
			json.NewEncoder(w).Encode(d)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/tags/list"):
			json.NewEncoder(w).Encode(map[string]any{"tags": []string{}})
		case r.URL.Path == "/v2/adguard/adguardhome/manifests/latest":
			w.Header().Set("Docker-Content-Digest", "sha256:a2")
		case r.URL.Path == "/v2/mdopp/homes/manifests/latest":
			w.Header().Set("Docker-Content-Digest", "sha256:h2")
		case strings.HasPrefix(r.URL.Path, "/repos/"):
			asked = append(asked, r.URL.Path)
			json.NewEncoder(w).Encode([]map[string]string{{"tag_name": "v0.107.56", "body": "Security: CVE-2026-1"}, {"tag_name": "v0.107.55", "body": "läuft"}})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	serve(t, srv)
	realCosign := Cosign
	t.Cleanup(func() { Cosign = realCosign })
	var verified [][]string
	Cosign = func(_ context.Context, args ...string) (string, error) {
		verified = append(verified, args)
		return "Error: no matching signatures", errors.New("exit status 1")
	}
	got := map[string]Row{}
	for _, u := range Updates(context.Background(), &Env{GitHub: GitHub{API: srv.URL}}, []Pinned{
		{Service: "adguard", Key: "IMAGE", Value: "docker.io/adguard/adguardhome:latest", Have: `["docker.io/adguard/adguardhome@sha256:a1"]`,
			Labels: map[string]string{"org.opencontainers.image.version": "v0.107.55", "org.opencontainers.image.revision": "aaaaaaa"}},
		{Service: "homes", Key: "IMAGE", Value: "ghcr.io/mdopp/homes:latest@sha256:h1"},
		{Service: "homes2", Key: "IMAGE", Value: "ghcr.io/mdopp/homes:latest", Have: `["ghcr.io/mdopp/homes@sha256:h2"]`},
	}, true) {
		got[u.Service] = u
	}
	if u := got["adguard"]; u.OldVersion != "v0.107.55" || u.NewVersion != "v0.107.56" || u.Source != "https://github.com/AdguardTeam/AdGuardHome" || u.Notes != "## v0.107.56\nSecurity: CVE-2026-1" || u.Unsigned != "" {
		t.Fatalf("%+v", u)
	}
	// the soak's signal (ADR 0037): the release's date, for a third party only; it has none here
	if !slices.Equal(asked, []string{"/repos/AdguardTeam/AdGuardHome/releases", "/repos/AdguardTeam/AdGuardHome/releases/tags/v0.107.56"}) {
		t.Fatalf("GitHub was asked %q", asked)
	}
	if own, pin := got["homes"], got["homes2"]; !strings.Contains(own.Unsigned, "keine gültige Signatur") || !strings.Contains(own.Unsigned, "no matching signatures") || pin.Unsigned != "" || pin.Pin != "sha256:h2" {
		t.Fatalf("own %+v, pin %+v", own, pin)
	}
	if len(verified) != 1 || !slices.Contains(verified[0], "ghcr.io/mdopp/homes@sha256:h2") || !slices.Contains(verified[0], SignedBy) || !slices.Contains(verified[0], "https://token.actions.githubusercontent.com") {
		t.Fatalf("cosign is not run as pinned: %q", verified)
	}
	if !Own("ghcr.io/mdopp/homes") || Own("ghcr.io/mdopp-evil/homes") || Own("docker.io/mdopp/homes") {
		t.Fatal("Own names the owner's ghcr namespace only")
	}
}

// The kernel's door is the only caller: a call without its key is refused,
// and the answer is the door's reply (out, err, code).
func TestOnlyTheKernelAsks(t *testing.T) {
	srv := httptest.NewServer(Handler("k", &Env{}))
	t.Cleanup(srv.Close)
	post := func(key, body string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/call", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return resp, m
	}
	if resp, _ := post("", `{"argv":["updates"]}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a call without the key: %d", resp.StatusCode)
	}
	if resp, _ := post("wrong", `{"argv":["updates"]}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a call with another key: %d", resp.StatusCode)
	}
	if _, m := post("k", `{"argv":["updates"]}`); m["out"] != "[]" || m["code"] != float64(0) {
		t.Fatalf("no images: an empty list, %v", m)
	}
	if _, m := post("k", `{"argv":["pull"]}`); m["code"] == float64(0) || !strings.Contains(m["err"].(string), "usage") {
		t.Fatalf("an unknown question: %v", m)
	}
}
