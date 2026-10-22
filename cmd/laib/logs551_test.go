package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/sysd"
)

// #551 point 7: the log review reads a day's warnings and errors, not the
// whole journal: laib logs --priority hands journalctl -p a level, and
// nothing but a level.
func TestLogsReadsOnlyTheLevelAsked(t *testing.T) {
	c := newCbox(t)
	var asked []string
	fake := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "journalctl" {
			asked = append(asked, strings.Join(args, " "))
		}
		return fake(ctx, name, args...)
	}
	if rc := cmdLogs(context.Background(), c.env, []string{"whoami", "--since", "24h", "--lines", "5000", "--priority", "warning"}); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	if len(asked) == 0 || !strings.Contains(asked[0], "-p warning") || !strings.Contains(asked[0], "-n 5000") {
		t.Fatalf("journalctl was not asked for the level: %v", asked)
	}
	asked = nil
	if rc := cmdLogs(context.Background(), c.env, []string{"whoami", "--priority", "--output=export"}); rc == cli.ExitOK || len(asked) != 0 {
		t.Fatalf("a priority that is not a level ran: rc %d, %v", rc, asked)
	}
}
