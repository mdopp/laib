package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/sysd"
)

// #916: the newest 200 lines were all start-up output after a restart, and
// the evidence from the day before was cut. --grep filters on the box before
// the cut: 1,000 lines with 10 matches spread across them give all 10.
func TestLogsGrepFiltersBeforeTheCut(t *testing.T) {
	c := newCbox(t)
	var journal []string
	for i := range 1000 {
		msg := "starting worker " + strconv.Itoa(i)
		if i%100 == 7 {
			msg = fmt.Sprintf("VipsJpeg: premature end of JPEG file asset=%d", i)
		}
		journal = append(journal, fmt.Sprintf("2026-10-29T%02d:%02d:00+0000 laib immich[1]: %s", i/60%24, i%60, msg))
	}
	fake := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name != "journalctl" {
			return fake(ctx, name, args...)
		}
		out := journal
		if i := slices.Index(args, "-n"); i >= 0 { // journalctl's own cut: the newest n
			n, _ := strconv.Atoi(args[i+1])
			out = out[max(0, len(out)-n):]
		}
		return strings.Join(out, "\n"), nil
	}
	var buf bytes.Buffer
	c.env.JSON, c.env.Out = true, &buf
	if rc := cmdLogs(context.Background(), c.env, []string{"whoami", "--since", "72h", "--grep", "vipsjpeg"}); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	var got map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil || len(got) == 0 {
		t.Fatalf("output %q: %v", buf.String(), err)
	}
	for unit, text := range got {
		lines := strings.Split(text, "\n")
		if len(lines) != 10 || strings.Count(text, "VipsJpeg") != 10 || !strings.Contains(lines[0], "asset=7") || !strings.Contains(lines[9], "asset=907") {
			t.Fatalf("%s: want the 10 matches, oldest first, got %d lines:\n%s", unit, len(lines), text)
		}
	}

	// the cut still holds: --lines bounds the matches, the newest kept
	buf.Reset()
	if rc := cmdLogs(context.Background(), c.env, []string{"whoami", "--since", "72h", "--grep", "VipsJpeg", "--lines", "3"}); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	_ = json.Unmarshal(buf.Bytes(), &got)
	for unit, text := range got {
		if lines := strings.Split(text, "\n"); len(lines) != 3 || !strings.Contains(lines[2], "asset=907") {
			t.Fatalf("%s: --lines 3 kept %d lines:\n%s", unit, len(lines), text)
		}
	}

	// a pattern that does not compile is an error, not an unfiltered read
	if rc := cmdLogs(context.Background(), c.env, []string{"whoami", "--grep", "("}); rc == cli.ExitOK {
		t.Fatal("a broken pattern read the journal")
	}
}

// #916: --lines is a number; anything else is refused, also next to --grep.
func TestLogsLinesIsANumber(t *testing.T) {
	c := newCbox(t)
	for _, args := range [][]string{{"whoami", "--lines", "--output=export"}, {"whoami", "--lines", "0", "--grep", "x"}} {
		if rc := cmdLogs(context.Background(), c.env, args); rc == cli.ExitOK {
			t.Fatalf("%v ran", args)
		}
	}
}
