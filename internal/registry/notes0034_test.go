package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/sysd"
)

// ADR 0034, decision 3: our own image counts as ours only with a signature
// from a workflow of the owner's repositories on main, checked by the
// pinned cosign; what cosign refuses is a reason in words, never a pass.
func TestVerifyChecksOurCIsSignature(t *testing.T) {
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	var argv []string
	signed := false
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		argv = append([]string{name}, args...)
		if !signed {
			return "Error: no matching signatures: none of the expected identities matched", errors.New("exit status 1")
		}
		return "Verification for ghcr.io/mdopp/homes@sha256:h2 -- The cosign claims were validated", nil
	}
	if why := Verify("ghcr.io/mdopp/homes:latest@sha256:h2"); !strings.Contains(why, "keine gültige Signatur") || !strings.Contains(why, "no matching signatures") {
		t.Fatalf("an unsigned image passed: %q", why)
	}
	for _, want := range []string{"podman", Cosign, "verify", SignedBy, "https://token.actions.githubusercontent.com", "ghcr.io/mdopp/homes@sha256:h2"} {
		if !slices.Contains(argv, want) {
			t.Errorf("cosign is not run as pinned: %q lacks %q", argv, want)
		}
	}
	signed = true
	if why := Verify("ghcr.io/mdopp/homes:latest@sha256:h2"); why != "" {
		t.Fatalf("a signed image: %q", why)
	}
	if why := Verify("ghcr.io/mdopp/homes:latest"); why == "" {
		t.Fatal("a tag without a digest passed")
	}
	if !Own("ghcr.io/mdopp/homes") || Own("ghcr.io/mdopp-evil/homes") || Own("docker.io/mdopp/homes") {
		t.Fatal("Own names the owner's ghcr namespace only")
	}
}

// --notes: a new digest carries both versions (the running one from the
// local image when the value floats), the GitHub repository its label
// names, what laib-haushalt read there, and for our own image whether its
// signature holds; a pin of what runs is not checked.
func TestNotesDescribeANewDigest(t *testing.T) {
	docs := map[string]any{
		"/v2/adguard/adguardhome/manifests/sha256:a2": map[string]any{"config": map[string]string{"digest": "sha256:ca"}},
		"/v2/adguard/adguardhome/blobs/sha256:ca": map[string]any{"config": map[string]any{"Labels": map[string]string{
			"org.opencontainers.image.version": "v0.107.56", "org.opencontainers.image.source": "https://github.com/AdguardTeam/AdGuardHome", "org.opencontainers.image.revision": "bbbbbbb"}}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d, ok := docs[r.URL.Path]; ok {
			json.NewEncoder(w).Encode(d)
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)
	realURL, realRun, realCL := RegistryURL, sysd.Run, Changelog
	t.Cleanup(func() { RegistryURL, sysd.Run, Changelog = realURL, realRun, realCL })
	RegistryURL = func(string) string { return srv.URL }
	var verified []string
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		switch {
		case args[0] == "run":
			verified = append(verified, args[len(args)-1])
			return "no signatures found", errors.New("exit status 1")
		case args[0] == "image" && strings.Contains(strings.Join(args, " "), ".Labels"):
			return `{"org.opencontainers.image.version":"v0.107.55","org.opencontainers.image.revision":"aaaaaaa"}`, nil
		}
		return "", nil
	}
	var asked []string
	Changelog = func(_ context.Context, _ *cli.Env, repo, oldV, newV, oldRev, newRev string) string {
		asked = []string{repo, oldV, newV, oldRev, newRev}
		return "## v0.107.56\nSecurity: CVE-2026-1"
	}
	u := ImageUpdate{Service: "adguard", Key: "IMAGE", Image: "docker.io/adguard/adguardhome", Current: "latest", Digest: "sha256:a2", value: "docker.io/adguard/adguardhome:latest"}
	u.describe(context.Background(), &cli.Env{})
	if u.OldVersion != "v0.107.55" || u.NewVersion != "v0.107.56" || u.Source != "https://github.com/AdguardTeam/AdGuardHome" || u.Notes != "## v0.107.56\nSecurity: CVE-2026-1" || u.Unsigned != "" {
		t.Fatalf("%+v", u)
	}
	if !slices.Equal(asked, []string{"AdguardTeam/AdGuardHome", "v0.107.55", "v0.107.56", "aaaaaaa", "bbbbbbb"}) {
		t.Fatalf("laib-haushalt was asked %q", asked)
	}
	own := ImageUpdate{Service: "homes", Key: "IMAGE", Image: "ghcr.io/mdopp/homes", Current: "latest", Digest: "sha256:h2", value: "ghcr.io/mdopp/homes:latest@sha256:h1"}
	own.describe(context.Background(), &cli.Env{})
	pin := ImageUpdate{Service: "homes2", Key: "IMAGE", Image: "ghcr.io/mdopp/homes", Current: "latest", Pin: "sha256:h1", value: "ghcr.io/mdopp/homes:latest"}
	pin.describe(context.Background(), &cli.Env{})
	if !strings.Contains(own.Unsigned, "keine gültige Signatur") || pin.Unsigned != "" || !slices.Equal(verified, []string{"ghcr.io/mdopp/homes@sha256:h2"}) {
		t.Fatalf("own %q, pin %q, verified %q", own.Unsigned, pin.Unsigned, verified)
	}
}
