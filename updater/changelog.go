package updater

import (
	"cmp"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHub is where the changelogs are read: GitHub's API, without a token —
// the program that reads the net holds no credential (ADR 0035); 60 asks an
// hour, and after a rate limit none before its reset.
type GitHub struct{ API string }

// Changelog (ADR 0034): what changed between the image the box runs and a
// newer digest — the releases from the new version down to the old, else the
// commits between the revisions — as untrusted text, capped; nothing found is
// empty.
func (g GitHub) Changelog(ctx context.Context, repo, oldV, newV, oldRev, newRev string) string {
	if !ghRepo.MatchString(repo) {
		return ""
	}
	oldV, newV = strings.TrimPrefix(oldV, "v"), strings.TrimPrefix(newV, "v")
	var text []string
	if oldV != "" && newV != "" && oldV != newV {
		var rs []struct {
			Tag  string `json:"tag_name"`
			Body string `json:"body"`
		}
		g.get(ctx, "/repos/"+repo+"/releases?per_page=30", &rs)
		in := false
		for _, r := range rs { // newest first
			t := strings.TrimPrefix(r.Tag, "v")
			if t == oldV {
				break
			}
			if in = in || t == newV; in {
				text = append(text, "## "+r.Tag+"\n"+clip(r.Body, 2000))
			}
		}
	}
	if len(text) == 0 && ghRev.MatchString(oldRev) && ghRev.MatchString(newRev) && oldRev != newRev {
		var c struct {
			Commits []struct {
				Commit struct{ Message string } `json:"commit"`
			} `json:"commits"`
		}
		g.get(ctx, "/repos/"+repo+"/compare/"+oldRev+"..."+newRev, &c)
		for _, x := range c.Commits {
			text = append(text, "- "+strings.SplitN(x.Commit.Message, "\n", 2)[0])
		}
	}
	return clip(strings.Join(text, "\n"), 6000)
}

var (
	ghRepo  = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)
	ghRev   = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	ghMu    sync.Mutex
	ghUntil time.Time // GitHub's rate-limit reset after a 403 or 429
)

// get is one GET of GitHub's API into v; a failure leaves v as it was.
func (g GitHub) get(ctx context.Context, path string, v any) {
	ghMu.Lock()
	defer ghMu.Unlock()
	if time.Now().Before(ghUntil) {
		return
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cmp.Or(g.API, "https://api.github.com"), "/")+path, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
	case http.StatusForbidden, http.StatusTooManyRequests:
		reset, _ := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64)
		ghUntil = time.Unix(max(reset, time.Now().Add(time.Minute).Unix()), 0)
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + " …"
}
