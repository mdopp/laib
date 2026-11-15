package registry

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/sysd"
)

// ADR 0034: a newer digest is a proposal a human taps, and our own image is
// proposed only with a signature from this household's CI. ADR 0035: the
// kernel asks no registry and reads no changelog for it — laib-updater does
// — but what it is about to run it checks itself: the digest's form, and
// our own image's signature, with the pinned cosign.

// Cosign is the pinned tool that checks our own images' signatures.
const Cosign = "ghcr.io/sigstore/cosign/cosign:v2.4.1@sha256:b03690aa52bfe94054187142fba24dc54137650682810633901767d8a3e15b31"

// SignedBy is who may sign our own image (keyless, GitHub's OIDC): a
// workflow of one of the owner's repositories, run on main.
const SignedBy = `^https://github\.com/mdopp/[A-Za-z0-9_.-]+/\.github/workflows/[A-Za-z0-9_.-]+@refs/heads/main$`

// Own: an image the owner's repositories build and sign.
func Own(image string) bool { return strings.HasPrefix(image, "ghcr.io/mdopp/") }

// Verify is "" when an own image, by digest, carries SignedBy's signature;
// else why not, in words.
func Verify(image string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	name, digest, _ := strings.Cut(image, "@")
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name = name[:i]
	}
	if digest == "" {
		return "kein Digest, nichts zu prüfen"
	}
	out, err := sysd.Run(ctx, "podman", "run", "--rm", Cosign, "verify", "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
		"--certificate-identity-regexp", SignedBy, name+"@"+digest)
	if err != nil {
		return "keine gültige Signatur aus Laibs CI: " + cli.Tail(strings.TrimSpace(out), 200)
	}
	return ""
}

// Runs: the box runs this image by digest, among its tag's RepoDigests — a
// pin of what runs brings nothing new and needs no signature check.
func Runs(image string) bool {
	ref, digest, ok := strings.Cut(image, "@")
	out, _ := sysd.Run(context.Background(), "podman", "image", "inspect", "--format", "{{json .RepoDigests}}", ref)
	return ok && strings.Contains(out, "@"+digest)
}

// digestForm is what a pin must be: a name, its tag if any, and one sha256
// of 64 hex digits.
var digestForm = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*(?::[0-9]+/[a-z0-9._/-]+)?(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?@sha256:[0-9a-f]{64}$`)

// Check is "" when the kernel may run image in place of old (ADR 0035): a
// value with a digest has the digest's form, and our own image by a new
// digest carries our CI's signature — checked here, whatever laib-updater
// said. A digest the box already runs, or the old value already had, is
// nothing new.
func Check(old, image string) string {
	_, d, pinned := strings.Cut(image, "@")
	if !pinned {
		return ""
	}
	if !digestForm.MatchString(image) {
		return image + " ist kein Image mit gültigem Digest (name:tag@sha256: und 64 Hex-Zeichen)"
	}
	if !Own(image) || strings.HasSuffix(old, "@"+d) || Runs(image) {
		return ""
	}
	if why := Verify(image); why != "" {
		return image + " ist nicht von Laibs CI signiert: " + why
	}
	return ""
}
