package updater

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// Pinned is one image value of the box as the kernel hands it over (ADR
// 0035): where it stands and what the box knows of it locally — for a
// value without a digest, podman's RepoDigests of the local image (Have),
// and with --notes its local labels (Labels). The kernel asks podman; this
// program never runs it.
type Pinned struct {
	Service string            `json:"service"`
	Key     string            `json:"key"`
	Value   string            `json:"value"`
	Have    string            `json:"have,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
}

// Row is one line of laib updates (registry.ImageUpdate in the kernel; the
// two meet over HTTP, never by import).
type Row struct {
	Service     string   `json:"service"`
	Key         string   `json:"key"`
	Image       string   `json:"image"`
	Current     string   `json:"current"`
	Patch       string   `json:"patch,omitempty"`
	Minor       string   `json:"minor,omitempty"`
	Error       string   `json:"error,omitempty"`
	Digest      string   `json:"digest,omitempty"`
	Pin         string   `json:"pin,omitempty"`
	PatchDigest string   `json:"patchDigest,omitempty"`
	OldVersion  string   `json:"oldVersion,omitempty"`
	NewVersion  string   `json:"newVersion,omitempty"`
	Source      string   `json:"source,omitempty"`
	Notes       string   `json:"notes,omitempty"`
	Unsigned    string   `json:"unsigned,omitempty"`
	Issues      int      `json:"issues,omitempty"`     // ADR 0037: the upstream's issues since the release naming the new version
	IssueLinks  []string `json:"issueLinks,omitempty"` // up to three of them, for the card's sources
}

var semverTag = regexp.MustCompile(`^(v?)(\d+)\.(\d+)(?:\.(\d+))?$`)

// Updates (#287) is, for every pinned image, the newest patch release (same
// major.minor) and the newest minor release (same major) the registry has,
// the tag's shape kept. For a floating tag and a digest pin, the
// registry's digest of the tag when the box runs another one (#528), and
// for a value without a digest the digest it runs (Pin, #1130). With
// notes, a new digest is described (ADR 0034).
func Updates(ctx context.Context, env *Env, images []Pinned, notes bool) []Row {
	out := []Row{}
	for _, p := range images {
		host, repo, tag := splitImage(p.Value)
		if host == "" {
			continue
		}
		ref, pin, _ := strings.Cut(p.Value, "@")
		u := Row{Service: p.Service, Key: p.Key, Image: ref[:strings.LastIndex(ref, ":")], Current: tag}
		u.Digest, u.Pin, u.Error = newDigest(ctx, host, repo, tag, pin, p.Have)
		if cur := semverTag.FindStringSubmatch(tag); cur != nil {
			releases(ctx, &u, host, repo, cur)
		}
		if notes {
			describe(ctx, env, &u, p)
		}
		out = append(out, u)
	}
	return out
}

// releases sets the newest patch and minor release of a version tag.
func releases(ctx context.Context, u *Row, host, repo string, cur []string) {
	tags, err := registryTags(ctx, host, repo)
	if err != nil {
		u.Error = err.Error()
	}
	num := func(m []string, i int) int { n, _ := strconv.Atoi(m[i]); return n }
	less := func(a, b []string) bool {
		return num(a, 3) < num(b, 3) || num(a, 3) == num(b, 3) && num(a, 4) < num(b, 4)
	}
	bestP, bestM := cur, cur
	for _, t := range tags {
		m := semverTag.FindStringSubmatch(t)
		if m == nil || m[1] != cur[1] || (m[4] == "") != (cur[4] == "") || num(m, 2) != num(cur, 2) {
			continue // another shape (v-prefix, components) or another major
		}
		if num(m, 3) == num(cur, 3) && less(bestP, m) {
			bestP = m
		}
		if num(m, 3) > num(cur, 3) && less(bestM, m) {
			bestM = m
		}
	}
	if bestP[0] != u.Current {
		u.Patch = bestP[0]
		u.PatchDigest, _ = tagDigest(ctx, host, repo, u.Patch) // proposed with it: a patch stays pinned (#1130)
	}
	if bestM[0] != u.Current {
		u.Minor = bestM[0]
	}
}

// newDigest is the registry's digest of the tag: moved when the box runs
// another — the pin's, else the local image's (have: podman keeps the index
// digest among its RepoDigests) — and same, for an unpinned tag, when it
// runs this one; and the error in words.
func newDigest(ctx context.Context, host, repo, tag, pin, have string) (moved, same, errText string) {
	if pin != "" {
		have = pin
	}
	d, err := tagDigest(ctx, host, repo, tag)
	if err != nil {
		return "", "", err.Error()
	}
	if d != "" && !strings.Contains(have, d) {
		return d, "", ""
	} else if pin != "" {
		return "", "", ""
	}
	return "", d, ""
}

// Host (#368): state/host/image is "# tag: <tag>" and the rebase reference
// by digest; Patch is the tag's digest when it moved. ok false without a pin.
func Host(ctx context.Context, file string) (u Row, ok bool) {
	ref := ""
	for _, l := range strings.Split(file, "\n") {
		if t, found := strings.CutPrefix(l, "# tag: "); found {
			u.Key = strings.TrimSpace(t)
		} else if l = strings.TrimSpace(l); ref == "" && l != "" && !strings.HasPrefix(l, "#") {
			ref = l
		}
	}
	name, digest, found := strings.Cut(ref, "@")
	if !found || u.Key == "" {
		return u, false
	}
	u.Service, u.Image, u.Current = "host", name, digest
	host, repo, _ := strings.Cut(name[strings.LastIndex(name, ":")+1:], "/")
	u.Patch, _, u.Error = newDigest(ctx, host, repo, u.Key, digest, "")
	return u, true
}
