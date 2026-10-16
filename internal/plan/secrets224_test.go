package plan

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// podmanBox swaps sysd.Run for a podman that holds box (name → value) and
// logs every argv; inspectErr makes inspect print what it found and fail,
// lsErr makes ls fail.
func podmanBox(t *testing.T, box map[string]string, inspectErr, lsErr string) *[]string {
	t.Helper()
	var calls []string
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		switch {
		case strings.HasPrefix(line, "podman secret ls") && lsErr != "":
			return lsErr, errors.New("exit status 125")
		case strings.HasPrefix(line, "podman secret ls"):
			var names []string
			for n := range box {
				names = append(names, n)
			}
			slices.Sort(names)
			return `time="2026-10-16T10:00:00Z" level=warning msg="laib.ignored by the filter"` + "\n" + strings.Join(names, "\n"), nil
		case strings.HasPrefix(line, "podman secret inspect"):
			if len(args) == 5 {
				return "Error: requires at least 1 arg(s), only received 0", errors.New("exit status 125")
			}
			out := []string{`WARN[0000] "a warning podman prints is a line too"`}
			for _, n := range args[5:] {
				out = append(out, n+" "+strconv.Quote(box[n]))
			}
			if inspectErr != "" {
				return strings.Join(append(out, inspectErr), "\n"), errors.New("exit status 125")
			}
			return strings.Join(out, "\n"), nil
		}
		return "", nil
	}
	return &calls
}

// #224 review: plan compares only laib's podman secrets, deletes only the
// laib.* ones nothing names, reads no other secret's value, and without the
// values (secrets nil) compares nothing at all, so an apply of that plan
// could never take the box's secrets away.
func TestPlanComparesOnlyLaibsPodmanSecrets(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	paths := boxfs.Paths{Quadlet: filepath.Join(t.TempDir(), "q"), Render: t.TempDir()}
	secrets := map[string]string{"WHOAMI_TOKEN": "s3cr3t-224", "DNS_API_TOKEN": "dns-224", "LLDAP_JWT_SECRET": "jwt-224"}
	box := map[string]string{
		"laib.whoami.whoami.TOKEN":        "s3cr3t-224", // the same: nothing to do
		"laib.caddy.caddy.LAIB_DNS_TOKEN": "dns-old",    // another value: a change of caddy
		"laib.gone.gone.KEY":              "orphan",     // nothing names it: goes
		"mysql-root":                      "not-laibs",  // not laib's: never read, never touched
		"laib-lookalike":                  "not-laibs",
	}
	calls := podmanBox(t, box, "", "")
	p, err := Compute(context.Background(), st, secrets, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range p.Changes {
		if n, ok := strings.CutPrefix(c.Path, "secret/"); ok {
			got[n] = c.Kind + "/" + c.Service
			if strings.Contains(c.Diff+c.Target, "224") || strings.Contains(c.Diff, "orphan") {
				t.Errorf("a secret value in the plan: %+v", c)
			}
		}
	}
	want := map[string]string{"laib.caddy.caddy.LAIB_DNS_TOKEN": "change/caddy", "laib.gone.gone.KEY": "delete/gone", "laib.auth.auth-lldap.LLDAP_JWT_SECRET": "add/auth"}
	if len(got) != len(want) {
		t.Errorf("secret changes %v, want %v", got, want)
	}
	for n, k := range want {
		if got[n] != k {
			t.Errorf("%s: %q, want %q (all: %v)", n, got[n], k, got)
		}
	}
	for _, c := range *calls {
		if strings.Contains(c, "inspect") && (strings.Contains(c, "mysql-root") || strings.Contains(c, "lookalike") || strings.Contains(c, "ignored")) {
			t.Errorf("plan read a secret that is not laib's: %s", c)
		}
	}
	only, _ := Compute(context.Background(), st, secrets, paths, "whoami")
	for _, c := range only.Changes {
		if c.Service != "" && c.Service != "whoami" {
			t.Errorf("only=whoami included %s", c.Path)
		}
	}

	*calls = nil
	lint, err := Compute(context.Background(), st, nil, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range lint.Changes {
		if strings.HasPrefix(c.Path, "secret/") {
			t.Errorf("without the values plan compared podman secrets: %+v", c)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("without the values plan read the box's secrets: %v", *calls)
	}
}

// #224 review: inspect --showsecret prints every value it found before it
// fails on one it did not; that output never reaches an error message
// (which ends in the journal and on the CLI), its error lines do.
func TestPlanNeverPrintsInspectOutput(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	podmanBox(t, map[string]string{"laib.whoami.whoami.TOKEN": "s3cr3t-224"}, `Error: no secret with name or id "laib.x": no such secret`, "")
	_, err = Compute(context.Background(), st, map[string]string{"WHOAMI_TOKEN": "s3cr3t-224"}, boxfs.Paths{Quadlet: t.TempDir(), Render: t.TempDir()}, "")
	if err == nil || strings.Contains(err.Error(), "s3cr3t-224") || !strings.Contains(err.Error(), "no such secret") {
		t.Fatalf("inspect failed: the error must say why and hold no value: %v", err)
	}
}

// #224 review: what plan cannot read from podman is red, never an empty box
// (every secret an add, and the orphans never seen); a box with none of
// laib's asks inspect nothing. An empty secrets.age (a lost or moved file)
// compares nothing either: the podman secrets are then the last copy of the
// values, and plan never removes them.
func TestPlanReadsPodmanOrFails(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	paths := boxfs.Paths{Quadlet: t.TempDir(), Render: t.TempDir()}
	podmanBox(t, nil, "", "Error: cannot open the secrets database")
	if _, err := Compute(context.Background(), st, map[string]string{"WHOAMI_TOKEN": "x"}, paths, ""); err == nil || !strings.Contains(err.Error(), "secrets database") {
		t.Fatalf("a failing podman secret ls must fail the plan with its reason: %v", err)
	}
	podmanBox(t, map[string]string{"mysql-root": "x"}, "", "")
	if _, err := Compute(context.Background(), st, map[string]string{"WHOAMI_TOKEN": "x"}, paths, ""); err != nil {
		t.Fatalf("a box without laib's secrets: %v", err)
	}
	podmanBox(t, map[string]string{"laib.whoami.whoami.TOKEN": "x", "laib.gone.gone.KEY": "y"}, "", "")
	p, err := Compute(context.Background(), st, map[string]string{}, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Changes {
		if strings.HasPrefix(c.Path, "secret/") {
			t.Errorf("an empty secrets.age took a podman secret away: %+v", c)
		}
	}
}
