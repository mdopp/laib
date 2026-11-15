package registry

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

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
