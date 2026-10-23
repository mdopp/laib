package registry

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// laib updates (#287): for every pinned image in the services' values, the
// newest patch release (same major.minor) and the newest minor release
// (same major) the registry has, the tag's shape kept. For a floating tag
// (latest, stable, a bare major) and a digest pin, the registry's digest of
// the tag when the box runs another one (#528); Auto says the nightly run
// (laib-images.timer, --run) takes it itself, else the Operator's
// aktualisierung proposes it. Reading only, except --run.

type ImageUpdate struct {
	Service string `json:"service"`
	Key     string `json:"key"`
	Image   string `json:"image"`
	Current string `json:"current"`
	Patch   string `json:"patch,omitempty"`
	Minor   string `json:"minor,omitempty"`
	Error   string `json:"error,omitempty"`
	Paused  string `json:"paused,omitempty"` // the host row: the drop-in that pauses OS updates (#486)
	Digest  string `json:"digest,omitempty"` // a floating tag or a digest pin: the registry's digest, when the box has another (#528)
	Auto    bool   `json:"auto,omitempty"`   // the nightly run takes it (apply.AutoImage); else it is a proposal
	Latest  string `json:"latest,omitempty"` // the laib row (#547): the newest release; never patch or digest, it is no proposal
	At      string `json:"at,omitempty"`     // the laib row: when self-update.sh last looked
}

// RegistryURL is where a registry host's v2 API lives; tests swap it.
var RegistryURL = func(host string) string {
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	return "https://" + host
}

var semverTag = regexp.MustCompile(`^(v?)(\d+)\.(\d+)(?:\.(\d+))?$`)

// splitImage: "redis:6.2" → docker.io, library/redis, 6.2.
func splitImage(ref string) (host, repo, tag string) {
	ref, _, _ = strings.Cut(ref, "@")
	i := strings.LastIndex(ref, ":")
	if i < 0 || strings.Contains(ref[i:], "/") {
		return "", "", ""
	}
	name, tag := ref[:i], ref[i+1:]
	host, repo, ok := strings.Cut(name, "/")
	if !ok || !strings.ContainsAny(host, ".:") {
		host, repo = "docker.io", name
	}
	if host == "docker.io" && !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	return host, repo, tag
}

func ImageUpdates(ctx context.Context, st *state.State) []ImageUpdate {
	var out []ImageUpdate
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		for key, v := range svc.Values {
			host, repo, tag := splitImage(v.Literal)
			if !strings.HasSuffix(key, "IMAGE") || host == "" {
				continue
			}
			cur := semverTag.FindStringSubmatch(tag)
			ref, pin, pinned := strings.Cut(v.Literal, "@")
			u := ImageUpdate{Service: name, Key: key, Image: ref[:strings.LastIndex(ref, ":")], Current: tag}
			if cur == nil || pinned {
				u.Auto = apply.AutoImage(svc, key)
				u.Digest, u.Error = newDigest(ctx, host, repo, tag, ref, pin)
			}
			if cur == nil {
				out = append(out, u)
				continue
			}
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
			if bestP[0] != tag {
				u.Patch = bestP[0]
			}
			if bestM[0] != tag {
				u.Minor = bestM[0]
			}
			out = append(out, u)
		}
	}
	if b, err := os.ReadFile(filepath.Join(st.Dir, "host", "image")); err == nil {
		if u, ok := hostUpdate(ctx, string(b)); ok {
			u.Paused = diag.OSPause()
			out = append(out, u)
		}
	}
	return out
}

// newDigest is the registry's digest of the tag when the box runs another:
// the pin's, else the local image's (podman keeps the index digest among
// its RepoDigests), and the error in words.
func newDigest(ctx context.Context, host, repo, tag, ref, pin string) (string, string) {
	have := pin
	if pin == "" {
		have, _ = sysd.Run(ctx, "podman", "image", "inspect", "--format", "{{json .RepoDigests}}", ref)
	}
	resp, err := registryGet(ctx, host, repo, "/manifests/"+tag)
	if err != nil {
		return "", err.Error()
	}
	resp.Body.Close()
	if d := resp.Header.Get("Docker-Content-Digest"); d != "" && !strings.Contains(have, d) {
		return d, ""
	}
	return "", ""
}

// registryTags lists a repository's tags through the registry v2 API, with
// the anonymous bearer token public registries hand out.
func registryTags(ctx context.Context, host, repo string) ([]string, error) {
	resp, err := registryGet(ctx, host, repo, "/tags/list?n=1000")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct{ Tags []string }
	return out.Tags, json.NewDecoder(resp.Body).Decode(&out)
}

// registryGet is one v2 API GET with the anonymous token dance; a status
// other than 200 is an error.
func registryGet(ctx context.Context, host, repo, path string) (*http.Response, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	url := RegistryURL(host) + "/v2/" + repo + path
	get := func(token string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return client.Do(req)
	}
	resp, err := get("")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		ch := map[string]string{}
		for _, m := range regexp.MustCompile(`(\w+)="([^"]*)"`).FindAllStringSubmatch(resp.Header.Get("WWW-Authenticate"), -1) {
			ch[m[1]] = m[2]
		}
		treq, err := http.NewRequestWithContext(ctx, http.MethodGet, ch["realm"]+"?service="+ch["service"]+"&scope="+ch["scope"], nil)
		if err != nil {
			return nil, err
		}
		tresp, err := client.Do(treq)
		if err != nil {
			return nil, err
		}
		var tok struct{ Token, AccessToken string }
		json.NewDecoder(tresp.Body).Decode(&tok)
		tresp.Body.Close()
		if resp, err = get(tok.Token + tok.AccessToken); err != nil {
			return nil, err
		}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s/%s: %s", host, repo, resp.Status)
	}
	return resp, nil
}

// hostUpdate (#368): state/host/image is "# tag: <tag>" and the rebase
// reference by digest; Patch is the tag's digest when it moved.
func hostUpdate(ctx context.Context, file string) (u ImageUpdate, ok bool) {
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
	u.Patch, u.Error = newDigest(ctx, host, repo, u.Key, "", digest)
	return u, true
}

// hostLine is the host row in words (#486): the pin by digest, whether its
// tag moved, and a pause that holds every OS update.
func hostLine(u ImageUpdate) string {
	what := "aktuell (" + u.Key + ")"
	if u.Patch != "" {
		what = "neuer Digest von " + u.Key + ": " + u.Patch + " (der wöchentliche Lauf schlägt ihn vor)"
	}
	if u.Paused != "" {
		what += "; OS-Updates pausiert durch " + u.Paused
	}
	return strings.TrimSpace(fmt.Sprintf("%-14s %s@%s  %s %s", u.Service, u.Image, u.Current, what, u.Error))
}

// laibRow (#547): Laib itself, from self-update.sh's last run; none before it.
func laibRow(home, running string) (ImageUpdate, bool) {
	r, ok := diag.ReadSelfUpdate(home)
	if !ok {
		return ImageUpdate{}, false
	}
	u := ImageUpdate{Service: "laib", Image: "laib", Current: running, Auto: r.Result != "off", At: r.At.Local().Format("02.01. 15:04")}
	if r.Latest != running {
		u.Latest = r.Latest
	}
	if r.Result == "failed" || r.Result == "skipped" {
		u.Error = r.Detail
	}
	if r.Result == "failed" {
		u.Error = r.Latest + " zurückgenommen: " + r.Detail
	}
	return u, true
}

// laibLine is the laib row in words.
func laibLine(u ImageUpdate) string {
	what := "aktuell"
	switch {
	case u.Latest != "" && u.Auto:
		what = "neu: " + u.Latest + " (nachts automatisch)"
	case u.Latest != "":
		what = "neu: " + u.Latest + " (aus: box.yml updates.laib)"
	}
	return strings.TrimSpace(fmt.Sprintf("%-14s %s  %s, geprüft %s  %s", u.Service, u.Current, what, u.At, u.Error))
}

func Updates(ctx context.Context, env *cli.Env, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	code := cli.ExitOK
	if slices.Contains(args, "--run") && !env.MCP {
		// after Laib's own update (the unit's ExecStartPre): the new release's default verbs as one proposal (#575)
		if _, err := inbox.ProposeDefaults(ctx, env, st); err != nil {
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "updates", Result: "red", Detail: "Standard-Verben: " + err.Error()})
		}
		code = apply.ImageRun(ctx, env, st) // laib-images.timer's nightly run (#528)
	}
	ups := ImageUpdates(ctx, st)
	if u, ok := laibRow(env.Paths.Home, cli.Version); ok {
		ups = append(ups, u)
	}
	behind := map[string]string{} // the 30-day yellow in diag (#528): images only
	for _, u := range ups {
		if w := cmp.Or(u.Digest, u.Patch); u.Service != "host" && u.Service != "laib" && w != "" {
			behind[u.Service+" "+u.Key] = w
		}
	}
	diag.SeeBehind(env.Paths.Home, time.Now(), behind)
	if env.JSON {
		if ups == nil {
			ups = []ImageUpdate{}
		}
		cli.Emit(env, ups)
		return code
	}
	for _, u := range ups {
		if u.Service == "host" && u.Key != "" {
			fmt.Fprintln(env.W(), hostLine(u))
			continue
		}
		if u.Service == "laib" {
			fmt.Fprintln(env.W(), laibLine(u))
			continue
		}
		if u.Digest != "" || !semverTag.MatchString(u.Current) {
			how := map[bool]string{true: "nachts automatisch", false: "als Vorschlag"}[u.Auto]
			fmt.Fprintf(env.W(), "%-14s %s:%s  %s (%s) %s\n", u.Service, u.Image, u.Current, cmp.Or(u.Digest, "aktuell"), how, u.Error)
			continue
		}
		fmt.Fprintf(env.W(), "%-14s %s:%s  patch %s  minor %s %s\n", u.Service, u.Image, u.Current, cmp.Or(u.Patch, "—"), cmp.Or(u.Minor, "—"), u.Error)
	}
	return code
}
