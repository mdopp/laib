package registry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// ADR 0035 (#1233): the kernel asks no registry. laib updates hands
// laib-updater every image value, for a value without a digest what podman
// has locally (its RepoDigests, with --notes its labels), and the host's
// pin; it prints the updater's rows and adds only what it knows itself (the
// OS pause). An updater that does not answer is an error, never an empty list.
func TestUpdatesAskTheUpdaterWithWhatTheBoxKnows(t *testing.T) {
	realRun, realAsk := sysd.Run, Ask
	t.Cleanup(func() { sysd.Run, Ask = realRun, realAsk })
	var ran []string
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		if strings.Contains(strings.Join(args, " "), ".Labels") {
			return `{"org.opencontainers.image.version":"1.0"}`, nil
		}
		return `["docker.io/traefik/whoami@sha256:w1"]`, nil
	}
	var asked Call
	Ask = func(_ context.Context, _ *cli.Env, c Call) (string, error) {
		asked = c
		b, _ := json.Marshal([]ImageUpdate{{Service: "whoami", Key: "IMAGE", Image: "docker.io/traefik/whoami", Current: "latest", Digest: "sha256:w2"}, {Service: "host", Key: "stable", Current: "sha256:h1"}})
		return string(b), nil
	}
	etc, dir := t.TempDir(), t.TempDir()
	t.Setenv("LAIB_ETC", etc)
	os.MkdirAll(filepath.Join(etc, "zincati", "config.d"), 0o755)
	os.WriteFile(filepath.Join(etc, "zincati", "config.d", "95-laib-boot-check.toml"), []byte("[updates]\nenabled = false\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "host"), 0o755)
	os.WriteFile(filepath.Join(dir, "host", "image"), []byte("# tag: stable\nostree-unverified-registry:ghcr.io/ublue-os/ucore@sha256:h1\n"), 0o644)
	val := func(v string) map[string]state.Value {
		return map[string]state.Value{"IMAGE": {Literal: v}, "TOKEN": {Literal: "x"}}
	}
	st := &state.State{Dir: dir, Services: map[string]*state.Service{
		"whoami": {Name: "whoami", Values: val("docker.io/traefik/whoami:latest")},
		"redis":  {Name: "redis", Values: val("redis:7@sha256:r1")},
	}}
	ups, err := ImageUpdates(context.Background(), &cli.Env{}, st, true)
	if err != nil || len(ups) != 2 || ups[0].Digest != "sha256:w2" || !strings.HasSuffix(ups[1].Paused, "95-laib-boot-check.toml") {
		t.Fatalf("the updater's rows, the host's with its pause: %+v %v", ups, err)
	}
	if strings.Join(asked.Argv, " ") != "updates --notes" || len(asked.Images) != 2 || !strings.HasPrefix(asked.Host, "# tag: stable") {
		t.Fatalf("asked %+v", asked)
	}
	for _, p := range asked.Images {
		switch p.Service {
		case "redis":
			if p.Value != "redis:7@sha256:r1" || p.Have != "" || p.Labels != nil {
				t.Errorf("a pin needs nothing of podman: %+v", p)
			}
		case "whoami":
			if p.Have != `["docker.io/traefik/whoami@sha256:w1"]` || p.Labels["org.opencontainers.image.version"] != "1.0" {
				t.Errorf("a floating value carries what runs: %+v", p)
			}
		}
	}
	for _, r := range ran {
		if !strings.HasPrefix(r, "podman image inspect") {
			t.Errorf("the kernel ran more than a local inspect: %q", r)
		}
	}
	Ask = func(context.Context, *cli.Env, Call) (string, error) {
		return "", errors.New("laib-updater antwortet nicht")
	}
	if ups, err := ImageUpdates(context.Background(), &cli.Env{}, st, false); err == nil || ups != nil {
		t.Fatalf("an updater that does not answer is an error: %+v %v", ups, err)
	}
	Ask = func(context.Context, *cli.Env, Call) (string, error) { return "<html>", nil }
	if _, err := ImageUpdates(context.Background(), &cli.Env{}, st, false); err == nil {
		t.Fatal("an answer that is no list of rows passed")
	}
}

// Ablauf L-57. ADR 0035: what the kernel runs it checks itself, whatever
// laib-updater said: a digest of the wrong form is refused before cosign
// runs, our own image by a new digest only with our CI's signature; a
// third party's image of the right form, a digest the old value had or the
// box runs, needs no signature.
func TestCheckRefusesADigestOrSignatureTheUpdaterGotWrong(t *testing.T) {
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	var cosign []string
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		if args[0] == "image" {
			return `["ghcr.io/mdopp/homes@sha256:` + strings.Repeat("c", 64) + `"]`, nil
		}
		cosign = append(cosign, args[len(args)-1])
		if strings.HasSuffix(args[len(args)-1], strings.Repeat("b", 64)) {
			return "no matching signatures", errors.New("exit status 1")
		}
		return "", nil
	}
	d := func(c string) string { return "@sha256:" + strings.Repeat(c, 64) }
	for _, bad := range []string{"ghcr.io/mdopp/homes:latest@sha256:abc", "docker.io/x/y:1@sha256:" + strings.Repeat("A", 64), "docker.io/x/y:1@sha256:" + strings.Repeat("a", 64) + "\nExec=evil",
		"docker.io/x/y:1@sha512:" + strings.Repeat("a", 64), "docker.io/x/y:1" + d("a") + d("b")} {
		if why := Check("docker.io/x/y:1", bad); !strings.Contains(why, "kein Image mit gültigem Digest") {
			t.Errorf("%q passed: %q", bad, why)
		}
	}
	if len(cosign) != 0 {
		t.Fatalf("cosign ran for a digest of the wrong form: %q", cosign)
	}
	if why := Check("ghcr.io/mdopp/homes:latest"+d("a"), "ghcr.io/mdopp/homes:latest"+d("b")); !strings.Contains(why, "nicht von Laibs CI signiert") || !strings.Contains(why, "no matching signatures") {
		t.Fatalf("an unsigned own digest passed: %q", why)
	}
	for _, ok := range [][2]string{
		{"ghcr.io/mdopp/homes:latest" + d("a"), "ghcr.io/mdopp/homes:latest" + d("e")}, // signed
		{"ghcr.io/mdopp/homes:latest" + d("b"), "ghcr.io/mdopp/homes:2" + d("b")},      // the old value had it
		{"ghcr.io/mdopp/homes:latest", "ghcr.io/mdopp/homes:latest" + d("c")},          // the box runs it
		{"docker.io/x/y:1", "docker.io/x/y:1" + d("b")},                                // not ours: the digest is all
		{"docker.io/x/y:1", "localhost:5000/x/y" + d("b")},                             // a registry port, no tag
		{"docker.io/x/y:1", "docker.io/x/y:2"},                                         // no digest: nothing to check
	} {
		if why := Check(ok[0], ok[1]); why != "" {
			t.Errorf("%s → %s refused: %q", ok[0], ok[1], why)
		}
	}
	if len(cosign) != 2 {
		t.Fatalf("cosign ran for %q, want the two new own digests", cosign)
	}
}
