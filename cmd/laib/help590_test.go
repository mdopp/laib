package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
)

// homeSnapshot is every file under the box's home with its content hash:
// the state repo, the journal, the render and Quadlet output, the inbox.
func homeSnapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		snap[p] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	return snap
}

func diffSnapshot(before, after map[string]string) []string {
	var changed []string
	for p, h := range after {
		if before[p] != h {
			changed = append(changed, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			changed = append(changed, p+" (gone)")
		}
	}
	return changed
}

// runVerb runs one verb with a deadline: a verb that would serve or wait
// forever is a failure, not a hung test.
func runVerb(t *testing.T, env *cliEnv, v cli.Verb, args ...string) (int, string, bool) {
	t.Helper()
	var out bytes.Buffer
	callEnv := *env
	callEnv.Out, callEnv.ErrOut, callEnv.In = &out, &out, strings.NewReader("")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- v.Run(ctx, &callEnv, args) }()
	select {
	case rc := <-done:
		return rc, out.String(), true
	case <-time.After(6 * time.Second):
		return -1, "", false
	}
}

// #590: `laib backup --help` ran a backup. Every verb in the table treats
// --help and -h as "show the usage, do nothing": no systemctl, podman,
// restic or git call, no journal line, no file under the home changed.
func TestEveryVerbHelpRunsNothing(t *testing.T) {
	c := newCbox(t)
	for _, v := range mcp.Verbs {
		for _, flag := range []string{"--help", "-h"} {
			before := homeSnapshot(t, c.env.Paths.Home)
			c.calls = nil
			rc, out, finished := runVerb(t, c.env, v, flag)
			if !finished {
				t.Errorf("laib %s %s did not return: it ran instead of showing its usage", v.Name, flag)
				continue
			}
			if rc != cli.ExitOK || !strings.Contains(out, v.Name) {
				t.Errorf("laib %s %s: rc %d, want 0 and the usage, got %q", v.Name, flag, rc, out)
			}
			if len(c.calls) > 0 {
				t.Errorf("laib %s %s called the box: %v", v.Name, flag, c.calls)
			}
			if changed := diffSnapshot(before, homeSnapshot(t, c.env.Paths.Home)); len(changed) > 0 {
				t.Errorf("laib %s %s changed files: %v", v.Name, flag, changed)
			}
		}
	}
}

// #590: the help sits anywhere before "--", also behind the service.
func TestHelpBehindTheServiceRunsNothing(t *testing.T) {
	c := newCbox(t)
	for _, argv := range [][]string{{"stop", "whoami", "--help"}, {"restore", "latest", "whoami", "-h"}, {"rollback", "--help"}, {"approve", "1", "--help"}} {
		before := homeSnapshot(t, c.env.Paths.Home)
		c.calls = nil
		rc, _, _ := runVerb(t, c.env, verb(t, argv[0]), argv[1:]...)
		if rc != cli.ExitOK || len(c.calls) > 0 || len(diffSnapshot(before, homeSnapshot(t, c.env.Paths.Home))) > 0 {
			t.Errorf("laib %v: rc %d, calls %v — want the usage and nothing run", argv, rc, c.calls)
		}
	}
}

// #590: over MCP the same holds — the agent that looked up the options was
// the one that started the backup.
func TestMcpHelpRunsNothing(t *testing.T) {
	c := newCbox(t)
	st, err := cli.LoadState(c.env)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range mcp.Verbs {
		if !mcp.Allowed[v.Name] {
			continue
		}
		before := homeSnapshot(t, c.env.Paths.Home)
		c.calls = nil
		out, rc := mcp.Call(context.Background(), c.env, st, "claude", "laib_"+v.Name, map[string]any{"args": "--help"})
		if rc == cli.ExitRefused { // above the hand's ceiling: refused before the verb, nothing ran
			continue
		}
		if rc != cli.ExitOK || !strings.Contains(out, v.Name) || len(c.calls) > 0 {
			t.Errorf("laib_%s --help: rc %d, out %q, calls %v", v.Name, rc, out, c.calls)
		}
		if changed := diffSnapshot(before, homeSnapshot(t, c.env.Paths.Home)); len(changed) > 0 {
			t.Errorf("laib_%s --help changed files: %v", v.Name, changed)
		}
	}
}

// strict590 are the verbs that stop, write, restore or apply: a flag they
// do not take is refused before anything runs (#590), never read as a
// service name, a reason or a commit.
var strict590 = [][]string{
	{"backup", "--frobnicate"},
	{"restore", "latest", "whoami", "--force"},
	{"apply", "--dry-run"},
	{"approve", "1", "--yes"},
	{"reject", "1", "--yes"},
	{"rollback", "--dry-run"},
	{"stop", "whoami", "--for", "2h"},
	{"restart", "whoami", "--now"},
	{"start", "whoami", "--now"},
	{"remove", "whoami", "--keep"},
	{"exec", "whoami", "--containr", "x", "--", "true"},
	{"write", "whoami", "a.txt", "--append"},
	{"converge", "--dry-run"},
	{"add", "whoami", "--nmae", "x"},
	{"pause", "whoami", "--for", "2h"},
	{"resume", "whoami", "--all"},
	{"secret", "set", "X", "--value"},
	{"token", "mint", "pi", "--tll", "1h"},
	{"propose", "--sett", "services/whoami/values.yml:A=b", "grund"},
}

func TestStrictVerbsRefuseAnUnknownFlag(t *testing.T) {
	c := newCbox(t)
	for _, argv := range strict590 {
		before := homeSnapshot(t, c.env.Paths.Home)
		c.calls = nil
		rc, out, _ := runVerb(t, c.env, verb(t, argv[0]), argv[1:]...)
		if rc != cli.ExitUsage || !strings.Contains(out, "unknown flag") {
			t.Errorf("laib %v: rc %d, out %q — want exit 1 and the unknown flag named", argv, rc, out)
		}
		if len(c.calls) > 0 {
			t.Errorf("laib %v called the box: %v", argv, c.calls)
		}
		if changed := diffSnapshot(before, homeSnapshot(t, c.env.Paths.Home)); len(changed) > 0 {
			t.Errorf("laib %v changed files: %v", argv, changed)
		}
	}
}

func verb(t *testing.T, name string) cli.Verb {
	t.Helper()
	for _, v := range mcp.Verbs {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no verb %s", name)
	return cli.Verb{}
}

// #590: what follows "--" is the container command's — `laib look media --
// grep --help x` reaches grep, and a strict verb does not judge its flags.
func TestGuardLeavesTheCommandAfterDoubleDash(t *testing.T) {
	var got []string
	v := cli.Guard(cli.Verb{Name: "probe590", Flags: []string{"--container"}, Run: func(_ context.Context, _ *cliEnv, args []string) int {
		got = args
		return cli.ExitOK
	}})
	c := &cliEnv{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	if rc := v.Run(context.Background(), c, []string{"media", "--container", "db", "--", "grep", "--help", "-rq", "x"}); rc != cli.ExitOK || len(got) != 8 {
		t.Fatalf("rc %d, the verb got %v — want the whole argv", rc, got)
	}
	got = nil
	if rc := v.Run(context.Background(), c, []string{"media", "-1"}); rc != cli.ExitOK || got == nil {
		t.Fatalf("a negative number is a value, not a flag: rc %d", rc)
	}
}
