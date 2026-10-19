package disk

import (
	"testing"
)

const mdstatOne = `Personalities : [raid1]
md127 : active raid1 vdb1[0]
      16760832 blocks super 1.2 [2/1] [U_]

unused devices: <none>
`

const mdstatTwo = `Personalities : [raid1]
md127 : active raid1 vdc1[2] vdb1[0]
      16760832 blocks super 1.2 [2/2] [UU]
`

const mdstatSync = `Personalities : [raid1]
md127 : active raid1 vdc1[2] vdb1[0]
      16760832 blocks super 1.2 [2/1] [U_]
      [===>.................]  recovery = 17.4% (2920000/16760832) finish=1.1min speed=200000K/sec
`

func TestParseMdstat(t *testing.T) {
	a := ParseMdstat(mdstatTwo)
	if len(a) != 1 || a[0].Name != "md127" || a[0].Slots != 2 || a[0].Active != 2 || a[0].Status != "UU" || len(a[0].Members) != 2 {
		t.Fatalf("parsed %+v", a)
	}
	if s := ParseMdstat(mdstatSync); s[0].Resync != "recovery 17.4%" {
		t.Fatalf("resync %q", s[0].Resync)
	}
}

func TestRaidFindings(t *testing.T) {
	cases := []struct {
		name     string
		mdstat   string
		expected int
		severity string
	}{
		{"no data disk", "", 0, ""},
		{"one member by design", mdstatOne, 1, "gelb"},
		{"mirror healthy", mdstatTwo, 2, ""},
		{"a member failed", mdstatOne, 2, "rot"},
		{"syncing", mdstatSync, 2, "info"},
		{"array gone", "Personalities : [raid1]\nunused devices: <none>\n", 1, "rot"},
	}
	for _, c := range cases {
		f := raidFindings(c.mdstat, c.expected)
		got := ""
		if len(f) > 0 {
			got = f[0].Severity
		}
		if got != c.severity {
			t.Errorf("%s: got %q (%v), want %q", c.name, got, f, c.severity)
		}
	}
}
