package registry

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/sysd"
)

// ADR 0034: a newer digest is a proposal a human taps. Its card says what
// changed (the Operator's versionen has the box's model summarize Notes),
// and our own image is proposed only with a signature from this
// household's CI.

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

// Changelog is what changed in a GitHub repository between two versions or
// revisions of an image, as untrusted text; laib-haushalt reads it behind
// the door, the kernel holds no GitHub client (#797). main sets it; ""
// when nothing is found or the door does not answer.
var Changelog func(ctx context.Context, env *cli.Env, repo, oldVersion, newVersion, oldRev, newRev string) string

// Runs: the box runs this image by digest, among its tag's RepoDigests — a
// pin of what runs brings nothing new and needs no signature check.
func Runs(image string) bool {
	ref, digest, ok := strings.Cut(image, "@")
	out, _ := sysd.Run(context.Background(), "podman", "image", "inspect", "--format", "{{json .RepoDigests}}", ref)
	return ok && strings.Contains(out, "@"+digest)
}

// describe is --notes for a new digest: our own image's signature, the
// two versions and what changed between them.
func (u *ImageUpdate) describe(ctx context.Context, env *cli.Env) {
	if u.Service == "host" || u.Service == "laib" || u.Digest == "" {
		return
	}
	if Own(u.Image) {
		u.Unsigned = Verify(u.Image + ":" + u.Current + "@" + u.Digest)
	}
	old := imageLabels(u.value)
	if !strings.Contains(u.value, "@") { // floating: the box runs the local image
		out, _ := sysd.Run(ctx, "podman", "image", "inspect", "--format", "{{json .Labels}}", u.value)
		json.Unmarshal([]byte(out), &old)
	}
	labels := imageLabels(u.Image + ":" + u.Current + "@" + u.Digest)
	u.OldVersion, u.NewVersion = version(old), version(labels)
	repo, ok := strings.CutPrefix(strings.TrimSuffix(labels["org.opencontainers.image.source"], ".git"), "https://github.com/")
	if !ok || Changelog == nil {
		return
	}
	u.Source = "https://github.com/" + repo
	const rev = "org.opencontainers.image.revision"
	u.Notes = Changelog(ctx, env, repo, u.OldVersion, u.NewVersion, old[rev], labels[rev])
}
