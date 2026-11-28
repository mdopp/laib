package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ADR 0034: what changed between the running image and a newer digest, for
// the nightly card's summary — the releases from the new version down to
// the old (not the old one itself, not a newer one), else the commits
// between the revisions; without a token (ADR 0035: the program that reads
// the net holds no credential). A rate limit stops further asks until
// GitHub's reset, and nothing found is empty, never an error.
func TestChangelogReadsTheReleasesBetweenTwoVersions(t *testing.T) {
	var paths, auth []string
	limited := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths, auth = append(paths, r.URL.Path), append(auth, r.Header.Get("Authorization"))
		switch {
		case limited:
			w.Header().Set("X-RateLimit-Reset", "0")
			w.WriteHeader(http.StatusForbidden)
		case strings.HasSuffix(r.URL.Path, "/releases"):
			json.NewEncoder(w).Encode([]map[string]string{{"tag_name": "v0.108.0", "body": "zu neu"}, {"tag_name": "v0.107.57", "body": "Security: CVE-2026-2"},
				{"tag_name": "v0.107.56", "body": "Fix DNS"}, {"tag_name": "v0.107.55", "body": "läuft schon"}})
		case strings.Contains(r.URL.Path, "/compare/aaaaaaa...bbbbbbb"):
			json.NewEncoder(w).Encode(map[string]any{"commits": []map[string]any{{"commit": map[string]string{"message": "feat: Kalender\n\nlong body"}}, {"commit": map[string]string{"message": "fix: Login"}}}})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { ghUntil = time.Time{} })
	gh, ctx := GitHub{API: srv.URL}, context.Background()
	if got := gh.Changelog(ctx, "AdguardTeam/AdGuardHome", "v0.107.55", "v0.107.57", "", ""); got != "## v0.107.57\nSecurity: CVE-2026-2\n## v0.107.56\nFix DNS" || auth[0] != "" {
		t.Fatalf("releases: %q %q", got, auth)
	}
	if got := gh.Changelog(ctx, "mdopp/homes", "", "", "aaaaaaa", "bbbbbbb"); got != "- feat: Kalender\n- fix: Login" {
		t.Fatalf("commits: %q", got)
	}
	n := len(paths)
	if got := gh.Changelog(ctx, "../evil", "1", "2", "", ""); got != "" || len(paths) != n {
		t.Fatal("a repository that is no owner/name was asked")
	}
	limited = true
	if got := gh.Changelog(ctx, "mdopp/homes", "", "", "aaaaaaa", "bbbbbbb"); got != "" {
		t.Fatalf("a rate limit is an empty answer: %q", got)
	}
	n = len(paths)
	gh.Changelog(ctx, "mdopp/homes", "", "", "aaaaaaa", "bbbbbbb")
	if len(paths) != n {
		t.Fatal("asked GitHub again before its reset")
	}
}

// ADR 0037: during a third party's soak the updater counts the upstream's
// issues opened since the release that name the version, with up to three
// links; no release (or no version) asks no search and is 0.
func TestIssuesCountsTheReportsSinceTheRelease(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/immich-app/immich/releases/tags/v2.5.0":
			json.NewEncoder(w).Encode(map[string]string{"published_at": "2026-11-25T18:00:00Z"})
		case r.URL.Path == "/search/issues":
			queries = append(queries, r.URL.Query().Get("q"))
			json.NewEncoder(w).Encode(map[string]any{"total_count": 5, "items": []map[string]string{{"html_url": "https://github.com/immich-app/immich/issues/1"},
				{"html_url": "https://github.com/immich-app/immich/issues/2"}, {"html_url": "https://github.com/immich-app/immich/issues/3"}, {"html_url": "https://github.com/immich-app/immich/issues/4"}}})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	gh, ctx := GitHub{API: srv.URL}, context.Background()
	n, links := gh.Issues(ctx, "immich-app/immich", "2.5.0")
	if n != 5 || len(links) != 3 || links[0] != "https://github.com/immich-app/immich/issues/1" {
		t.Fatalf("issues: %d %v", n, links)
	}
	if len(queries) != 1 || queries[0] != `repo:immich-app/immich is:issue "2.5.0" created:>=2026-11-25` {
		t.Fatalf("the search: %q", queries)
	}
	if n, _ := gh.Issues(ctx, "immich-app/immich", ""); n != 0 || len(queries) != 1 {
		t.Fatal("no version asked the search")
	}
	if n, _ := gh.Issues(ctx, "immich-app/immich", "9.9.9"); n != 0 || len(queries) != 1 {
		t.Fatal("no release asked the search")
	}
}
