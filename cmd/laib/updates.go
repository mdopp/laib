package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
)

// laib updates (#287): for every pinned image in the services' values, the
// newest patch release (same major.minor) and the newest minor release
// (same major) the registry has, the tag's shape kept. A floating tag
// (latest, a bare major) is podman auto-update's business, not a proposal.
// Reading only; the Operator's aktualisierung skill proposes from it.

type imageUpdate struct {
	Service string `json:"service"`
	Key     string `json:"key"`
	Image   string `json:"image"`
	Current string `json:"current"`
	Patch   string `json:"patch,omitempty"`
	Minor   string `json:"minor,omitempty"`
	Error   string `json:"error,omitempty"`
}

// registryURL is where a registry host's v2 API lives; tests swap it.
var registryURL = func(host string) string {
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

func imageUpdates(ctx context.Context, st *state.State) []imageUpdate {
	var out []imageUpdate
	for _, name := range st.ServiceNames() {
		for key, v := range st.Services[name].Values {
			host, repo, tag := splitImage(v.Literal)
			cur := semverTag.FindStringSubmatch(tag)
			if !strings.HasSuffix(key, "IMAGE") || cur == nil {
				continue
			}
			ref, _, _ := strings.Cut(v.Literal, "@")
			u := imageUpdate{Service: name, Key: key, Image: ref[:strings.LastIndex(ref, ":")], Current: tag}
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
	return out
}

// registryTags lists a repository's tags through the registry v2 API, with
// the anonymous bearer token public registries hand out.
func registryTags(ctx context.Context, host, repo string) ([]string, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	url := registryURL(host) + "/v2/" + repo + "/tags/list?n=1000"
	get := func(token string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s/%s: %s", host, repo, resp.Status)
	}
	var out struct{ Tags []string }
	return out.Tags, json.NewDecoder(resp.Body).Decode(&out)
}

func cmdUpdates(ctx context.Context, env *cliEnv, _ []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	ups := imageUpdates(ctx, st)
	if env.JSON {
		if ups == nil {
			ups = []imageUpdate{}
		}
		return cli.Emit(env, ups)
	}
	for _, u := range ups {
		fmt.Fprintf(env.W(), "%-14s %s:%s  patch %s  minor %s %s\n", u.Service, u.Image, u.Current, cmp.Or(u.Patch, "—"), cmp.Or(u.Minor, "—"), u.Error)
	}
	return cli.ExitOK
}
