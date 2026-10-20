package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #486: on uCore the one OS update path is laib-host-image. diag says when
// it cannot work (no pin, Zincati still asking Fedora's graph) and when
// updates are paused: before, everything was green and no update ever came.

func TestOSPauseIsReadAsZincatiReadsIt(t *testing.T) {
	etc := t.TempDir()
	t.Setenv("LAIB_ETC", etc)
	d := filepath.Join(etc, "zincati", "config.d")
	os.MkdirAll(d, 0o755)
	if p := OSPause(); p != "" {
		t.Fatalf("no drop-in, no pause: %q", p)
	}
	os.WriteFile(filepath.Join(d, "60-laib-updates.toml"), []byte("[updates]\nstrategy = \"fleet_lock\"\n"), 0o644)
	os.WriteFile(filepath.Join(d, "90-paused-by-hand.toml"), []byte("[updates]\nenabled = false\n"), 0o644)
	if p := OSPause(); filepath.Base(p) != "90-paused-by-hand.toml" {
		t.Fatalf("the hand-made pause: %q", p)
	}
	os.WriteFile(filepath.Join(d, "99-on.toml"), []byte("[updates]\nenabled = true\n"), 0o644)
	if p := OSPause(); p != "" {
		t.Fatalf("a later file switches updates on again, as in Zincati: %q", p)
	}
}

func TestOSFindingsOnAUCoreBox(t *testing.T) {
	ucore := OSState{Image: "ostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal@sha256:a287", Booted: "38aa.0"}
	has := func(fs []Finding, sev, word string) bool {
		for _, f := range fs {
			if f.Probe == "os-update" && f.Severity == sev && strings.Contains(f.Detail, word) {
				return true
			}
		}
		return false
	}
	if fs := osFindings(ucore, false, false, "", "05:00–07:00"); !has(fs, "gelb", "nicht gepinnt") {
		t.Errorf("a uCore box without host/image gets no OS update, and diag must say so: %+v", fs)
	}
	if fs := osFindings(ucore, true, true, "", "05:00–07:00"); !has(fs, "gelb", "Zincati") {
		t.Errorf("Zincati running on uCore asks a graph that does not know the image: %+v", fs)
	}
	if fs := osFindings(ucore, true, false, "", "05:00–07:00"); len(fs) != 0 {
		t.Errorf("pinned, Zincati masked, no pause: nothing to say, got %+v", fs)
	}
	fcos := OSState{Image: "ostree-image-signed:docker://quay.io/fedora/fedora-coreos:stable"}
	if fs := osFindings(fcos, false, true, "", "05:00–07:00"); len(fs) != 0 {
		t.Errorf("Fedora CoreOS: Zincati is its driver, no pin needed, got %+v", fs)
	}
	if fs := osFindings(ucore, true, false, "/etc/zincati/config.d/90-paused-by-hand.toml", "05:00–07:00"); !has(fs, "info", "pausiert") || !has(fs, "info", "90-paused-by-hand.toml") {
		t.Errorf("a pause by hand is said, with its file: %+v", fs)
	}
	if fs := osFindings(ucore, true, false, "/etc/zincati/config.d/95-laib-boot-check.toml", "05:00–07:00"); !has(fs, "gelb", "zurückgerollt") {
		t.Errorf("the boot check's pause after a rollback needs the owner: %+v", fs)
	}
	staged := ucore
	staged.Staged = "44.20261101"
	if fs := osFindings(staged, true, false, "", "05:00–07:00"); !has(fs, "info", "05:00–07:00") {
		t.Errorf("a staged update names its window: %+v", fs)
	}
	if fs := osFindings(staged, true, false, "/etc/zincati/config.d/90-x.toml", "05:00–07:00"); has(fs, "info", "05:00–07:00") {
		t.Errorf("a staged update under a pause does not come in the window: %+v", fs)
	}
}
