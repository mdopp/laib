package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// fakeRegistry answers the Docker registry v2 tag list behind the
// anonymous token dance every public registry speaks.
func fakeRegistry(t *testing.T, tags map[string][]string) {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			json.NewEncoder(w).Encode(map[string]string{"token": "t-" + r.URL.Query().Get("scope")})
			return
		}
		repo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/"), "/tags/list")
		if r.Header.Get("Authorization") != "Bearer t-repository:"+repo+":pull" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="registry",scope="repository:`+repo+`:pull"`)
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags[repo]})
	}))
	t.Cleanup(srv.Close)
	real := RegistryURL
	RegistryURL = func(string) string { return srv.URL }
	t.Cleanup(func() { RegistryURL = real })
}

// #287: which pinned images have a newer patch or minor release — read
// from the registry, the tag's own shape kept (v1.10.1 → v1.10.3, v1.11.2);
// a floating tag (latest, a bare major) is podman auto-update's business.
func TestImageUpdatesFindPatchAndMinor(t *testing.T) {
	fakeRegistry(t, map[string][]string{
		"traefik/whoami":         {"v1.9.0", "v1.10.1", "v1.10.3", "v1.10.3-rc1", "v1.11.2", "v1.11.0", "v2.0.0", "latest"},
		"library/redis":          {"6.2", "6.4", "7.0", "6.2-alpine"},
		"advplyr/audiobookshelf": {"2.1.0"},
	})
	val := func(v string) map[string]state.Value { return map[string]state.Value{"IMAGE": {Literal: v}} }
	st := &state.State{Services: map[string]*state.Service{
		"whoami": {Name: "whoami", Values: val("docker.io/traefik/whoami:v1.10.1")},
		"redis":  {Name: "redis", Values: val("redis:6.2")},
		"abs":    {Name: "abs", Values: val("ghcr.io/advplyr/audiobookshelf:latest")},
		"caddy":  {Name: "caddy", Values: val("ghcr.io/mdopp/laib-caddy:2")},
	}}
	got := map[string]ImageUpdate{}
	for _, u := range ImageUpdates(context.Background(), st) {
		got[u.Service] = u
	}
	if u := got["whoami"]; u.Current != "v1.10.1" || u.Patch != "v1.10.3" || u.Minor != "v1.11.2" || u.Key != "IMAGE" || u.Image != "docker.io/traefik/whoami" {
		t.Fatalf("whoami: %+v", u)
	}
	if u := got["redis"]; u.Patch != "" || u.Minor != "6.4" {
		t.Fatalf("redis: %+v", u)
	}
	for _, s := range []string{"abs", "caddy"} {
		if u, ok := got[s]; ok && (u.Patch != "" || u.Minor != "") {
			t.Fatalf("%s floats and got an update: %+v", s, u)
		}
	}
}
