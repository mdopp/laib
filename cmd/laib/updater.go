package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/registry"
)

// updaterAsk is registry.Ask (ADR 0035): laib-updater answers what the
// registries, GitHub and cosign say, on 127.0.0.1 with its door's key; the
// kernel reads no answer from the internet itself.
func updaterAsk(ctx context.Context, env *cliEnv, c registry.Call) (string, error) {
	reply, err := doorPost(ctx, env, "updater", c)
	if err != nil {
		return "", fmt.Errorf("laib-updater antwortet nicht: %v (laib logs updater)", err)
	}
	if reply.Code != 0 {
		return "", errors.New("laib-updater: " + strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(reply.Err), "laib:")))
	}
	return reply.Out, nil
}

// imageVersion is inbox.ImageVersion: the version an image's labels name,
// as laib-updater reads them; "" when it does not answer in ten seconds.
func imageVersion(image string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := updaterAsk(ctx, &cliEnv{Paths: boxfs.Default()}, registry.Call{Argv: []string{"version", image}})
	return out
}
