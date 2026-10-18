package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// #349: Home Assistant's post-deploy mints Haus's and the Bote's tokens and
// hands them back (laib-secret:). Hand-backs were stored after the run, so a
// service that declares one took it only on the next apply: `laib apply
// home-assistant` ended green and Haus still had no token. Now each service
// that declares a handed-back secret is planned again in the same apply, and
// applied when its rendered output changed (the new value reaches its
// container as a podman secret, and the service restarts).
func TestAHandedBackSecretReachesTheServiceThatDeclaresItInTheSameApply(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	svc := filepath.Join(env.Paths.State, "services", "reader")
	os.MkdirAll(svc, 0o755)
	for name, content := range map[string]string{
		"laib.yml":         "name: reader\nsecrets:\n  - name: MINTED_TOKEN\n    external: true\n    optional: true\n",
		"values.yml":       "IMAGE: docker.io/traefik/whoami:v1.10\nTOKEN: !secret MINTED_TOKEN\n",
		"reader.container": "[Container]\nImage=${IMAGE}\nEnvironment=TOKEN=${TOKEN}\n\n[Install]\nWantedBy=default.target\n",
	} {
		if err := os.WriteFile(filepath.Join(svc, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(env.Paths.State, "services", "whoami", "post-deploy"), []byte("#!/bin/sh\n"), 0o755)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	if code := apply.ApplyLocked(ctx, env, st, "", "both"); code != cli.ExitOK {
		t.Fatalf("first apply: exit %d", code)
	}

	// the hook that mints: whoami's post-deploy, once
	realHook := apply.RunHook
	t.Cleanup(func() { apply.RunHook = realHook })
	minted := false
	apply.RunHook = func(_ context.Context, h apply.HookSpec) (string, error) {
		if strings.HasSuffix(h.Command[0], "whoami/post-deploy") && !minted {
			minted = true
			return "laib-secret: MINTED_TOKEN=minted-349\n", nil
		}
		return "", nil
	}
	var got []string // podman secrets created with their value, and restarts
	realRun := sysd.Run
	t.Cleanup(func() { sysd.Run = realRun })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		switch {
		case name == "podman" && slices.Contains(args, "create"):
			got = append(got, args[len(args)-2]+"="+sysd.Stdin(ctx))
		case name == "systemctl" && slices.Contains(args, "restart"):
			got = append(got, line)
		}
		return realRun(ctx, name, args...)
	}
	if code := apply.ApplyLocked(ctx, env, st, "whoami", "laib apply whoami"); code != cli.ExitOK {
		t.Fatalf("apply whoami: exit %d", code)
	}
	if !slices.Contains(got, "laib.reader.reader.TOKEN=minted-349") {
		t.Fatalf("reader did not get the handed-back token in the apply that minted it: %q", got)
	}
	if !slices.Contains(got, "systemctl --user restart reader.service") {
		t.Fatalf("reader was not restarted with its new token: %q", got)
	}
}
