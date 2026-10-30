package main

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/sysd"
)

// #947: the Operator did not look for 43 minutes and its own log was out of
// reach (#944): `laib logs operator` answered "unknown service". Laib's own
// units are read under short names, with the same flags as a service.
func TestLogsReadsLaibsOwnUnits(t *testing.T) {
	c := newCbox(t)
	fake := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name != "journalctl" {
			return fake(ctx, name, args...)
		}
		if !slices.Contains(args, "--user") {
			t.Errorf("own units are user units: %v", args)
		}
		return "2026-10-30T10:00:00+0000 laib op[1]: tick\n2026-10-30T10:01:00+0000 laib op[1]: scan token=x", nil // newCbox's DNS_API_TOKEN is "x"
	}
	t.Cleanup(func() { sysd.Run = fake })
	for name, want := range map[string][]string{
		"laib":     {"laib.service"},
		"operator": {"laib-operator.service"},
		"notify":   {"laib-notify.service", "laib-notify.timer"},
		"updates":  {"laib-images.service", "laib-images.timer"},
	} {
		var buf bytes.Buffer
		c.env.JSON, c.env.Out = true, &buf
		if rc := cmdLogs(context.Background(), c.env, []string{name, "--since", "2h", "--grep", "scan", "--lines", "5"}); rc != cli.ExitOK {
			t.Fatalf("laib logs %s: rc %d", name, rc)
		}
		var got map[string]string
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("laib logs %s: %q: %v", name, buf.String(), err)
		}
		if units := slices.Sorted(maps.Keys(got)); !slices.Equal(units, slices.Sorted(slices.Values(want))) {
			t.Fatalf("laib logs %s: units %v, want %v", name, units, want)
		}
		for u, text := range got {
			if text != "2026-10-30T10:01:00+0000 laib op[1]: scan token=${secret:DNS_API_TOKEN}" {
				t.Fatalf("laib logs %s, %s: want the grep hit, secret redacted, got %q", name, u, text)
			}
		}
	}
	if rc := cmdLogs(context.Background(), c.env, []string{"nope"}); rc == cli.ExitOK {
		t.Fatal("an unknown name must still fail")
	}
}
