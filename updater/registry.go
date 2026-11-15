package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

// RegistryURL is where a registry host's v2 API lives; tests swap it.
var RegistryURL = func(host string) string {
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	return "https://" + host
}

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

// tagDigest is the registry's digest of the tag, "" when it names none.
func tagDigest(ctx context.Context, host, repo, tag string) (string, error) {
	resp, err := registryGet(ctx, host, repo, "/manifests/"+tag)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	return resp.Header.Get("Docker-Content-Digest"), nil
}

// VersionTimeout bounds one ImageVersion (#959); tests shorten it.
var VersionTimeout = 5 * time.Second

// ImageVersion (#959) is the version an image's labels name, the OCI one
// before the vendors'. "" when it has none or the registry does not answer
// in time: a card says it without.
func ImageVersion(image string) string {
	return version(imageLabels(image))
}

func version(labels map[string]string) string {
	for _, l := range []string{"org.opencontainers.image.version", "io.hass.version", "org.label-schema.version"} {
		if v := labels[l]; v != "" {
			return v
		}
	}
	return ""
}

// imageLabels are an image's labels, read from its registry: the manifest
// (an index's for this platform), then the config blob's; nil when the
// registry does not answer in time.
func imageLabels(image string) map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), VersionTimeout)
	defer cancel()
	host, repo, ref := splitImage(image)
	if _, pin, ok := strings.Cut(image, "@"); ok {
		ref = pin
	}
	get := func(path string, v any) bool {
		resp, err := registryGet(ctx, host, repo, path)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v) == nil
	}
	type entry struct {
		Digest   string
		Platform struct{ OS, Architecture string }
	}
	var m struct {
		Manifests []entry
		Config    struct{ Digest string }
	}
	if host == "" || !get("/manifests/"+ref, &m) {
		return nil
	}
	if len(m.Manifests) > 0 {
		i := max(0, slices.IndexFunc(m.Manifests, func(x entry) bool {
			return x.Platform.OS == "linux" && x.Platform.Architecture == runtime.GOARCH
		}))
		if d := m.Manifests[i].Digest; !get("/manifests/"+d, &m) {
			return nil
		}
	}
	var cfg struct {
		Config struct{ Labels map[string]string }
	}
	if m.Config.Digest == "" || !get("/blobs/"+m.Config.Digest, &cfg) {
		return nil
	}
	return cfg.Config.Labels
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
	return out.Tags, json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out)
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
		json.NewDecoder(io.LimitReader(tresp.Body, 1<<20)).Decode(&tok)
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
