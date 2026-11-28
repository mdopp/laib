package updater

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// SignedBy is who may sign our own image (keyless, GitHub's OIDC): a
// workflow of one of the owner's repositories, run on main. The kernel
// holds the same pattern (registry.SignedBy) and checks again before it
// runs a digest; this check only keeps an unsigned one off the card.
const SignedBy = `^https://github\.com/mdopp/[A-Za-z0-9_.-]+/\.github/workflows/[A-Za-z0-9_.-]+@refs/heads/main$`

// Cosign runs the cosign binary of this program's image (the kernel's
// pinned registry.Cosign, copied in by the Containerfile); tests swap it.
var Cosign = func(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "/cosign", args...).CombinedOutput()
	return string(out), err
}

// Own: an image the owner's repositories build and sign.
func Own(image string) bool { return strings.HasPrefix(image, "ghcr.io/mdopp/") }

// verify is "" when an own image, by digest, carries SignedBy's signature;
// else why not, in words.
func verify(ctx context.Context, name, digest string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := Cosign(ctx, "verify", "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
		"--certificate-identity-regexp", SignedBy, name+"@"+digest)
	if err != nil {
		return "keine gültige Signatur aus Laibs CI: " + tail(strings.TrimSpace(out), 200)
	}
	return ""
}

// describe is --notes for a new digest (ADR 0034): our own image's
// signature, the two versions and what changed between them. The labels of
// what runs: a pinned value's from the registry, a floating one's as the
// kernel read them from the local image.
func describe(ctx context.Context, env *Env, u *Row, p Pinned) {
	if u.Digest == "" {
		return
	}
	if Own(u.Image) {
		u.Unsigned = verify(ctx, u.Image, u.Digest)
	}
	old := p.Labels
	if strings.Contains(p.Value, "@") {
		old = imageLabels(p.Value)
	}
	labels := imageLabels(u.Image + ":" + u.Current + "@" + u.Digest)
	u.OldVersion, u.NewVersion = version(old), version(labels)
	repo, ok := strings.CutPrefix(strings.TrimSuffix(labels["org.opencontainers.image.source"], ".git"), "https://github.com/")
	if !ok || !ghRepo.MatchString(repo) {
		return
	}
	u.Source = "https://github.com/" + repo
	const rev = "org.opencontainers.image.revision"
	u.Notes = env.GitHub.Changelog(ctx, repo, u.OldVersion, u.NewVersion, old[rev], labels[rev])
	if !Own(u.Image) { // the soak's public signal (ADR 0037): our own images have their CI instead
		u.Issues, u.IssueLinks = env.GitHub.Issues(ctx, repo, u.NewVersion)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + strings.ToValidUTF8(s[len(s)-n:], "")
}
