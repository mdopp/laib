package apply

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/sysd"
)

// #370: the dns role's apply runs while the box has no resolver (the AdGuard
// it replaces is stopped), so the pull fails. An image that is present
// locally is no red: the unit starts from it and verify says whether it runs.
// An image that is not there stays red.
func TestAFailedPullOfALocalImageIsNotRed(t *testing.T) {
	for _, present := range []bool{true, false} {
		st, paths, calls := roleBox(t)
		inner := sysd.Run
		sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
			line := name + " " + strings.Join(args, " ")
			switch {
			case strings.HasPrefix(line, "podman pull"):
				*calls = append(*calls, line)
				return "Error: dial tcp: lookup registry-1.docker.io: no such host", errors.New("exit status 125")
			case strings.HasPrefix(line, "podman image exists"):
				*calls = append(*calls, line)
				if present {
					return "", nil
				}
				return "", errors.New("exit status 1")
			}
			return inner(ctx, name, args...)
		}
		key := "quadlet/whoami/whoami.container"
		p := &plan.Plan{Services: []string{"whoami"}, Output: render.Output(map[string]string{key: "[Container]\nImage=docker.io/traefik/whoami:v1.11\n\n[Install]\nWantedBy=default.target\n"}), Changes: []plan.Change{{Path: key, Target: plan.Target(paths, key), Service: "whoami", Kind: "add"}}}
		res, err := Run(context.Background(), st, p, paths, map[string]string{}, "add whoami")
		if err != nil {
			t.Fatal(err)
		}
		var pull *Check
		for i, c := range res.Checks {
			if c.What == "pull docker.io/traefik/whoami:v1.11" {
				pull = &res.Checks[i]
			}
		}
		if pull == nil {
			t.Fatalf("present=%v: the failed pull is not reported: %+v", present, res.Checks)
		}
		if pull.OK != present {
			t.Fatalf("present=%v: the failed pull's check is OK=%v: %+v", present, pull.OK, *pull)
		}
		if present && (!res.Green || !strings.Contains(pull.Detail, "nicht aktualisiert")) {
			t.Fatalf("a local image: the apply is green and says the image was not updated: %+v", res)
		}
	}
}
