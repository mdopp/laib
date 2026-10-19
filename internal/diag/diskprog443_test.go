package diag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #443: the data RAID, SMART and the box's temperature moved out of the
// kernel into the program laib-disk (disk/). diag asks it (`laib-disk probe`,
// findings as JSON) and shows what it says; the hardware part only when the
// whole box is probed. A missing program is a finding, never a silent skip.
func TestDiagAsksLaibDisk(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "laib-disk")
	os.WriteFile(bin, []byte(`#!/bin/sh
echo "$@" >> "$0.args"
echo '[{"severity":"gelb","probe":"raid","detail":"md127: 1 von 2 Platten","fix":"laib disk add /dev/sdX"}]'
`), 0o755)
	t.Setenv("LAIB_DISK", bin)
	got := diskFindings(context.Background(), true)
	if len(got) != 1 || got[0].Probe != "raid" || got[0].Severity != "gelb" || !strings.Contains(got[0].Detail, "1 von 2") {
		t.Fatalf("diag did not take laib-disk's findings: %+v", got)
	}
	diskFindings(context.Background(), false)
	args, _ := os.ReadFile(bin + ".args")
	if string(args) != "probe --hardware\nprobe\n" {
		t.Errorf("laib-disk was asked %q", args)
	}
	t.Setenv("LAIB_DISK", filepath.Join(dir, "missing"))
	got = diskFindings(context.Background(), false)
	if len(got) != 1 || got[0].Severity != "gelb" || !strings.Contains(got[0].Detail, "laib-disk") {
		t.Fatalf("a missing laib-disk is not a finding: %+v", got)
	}
}
