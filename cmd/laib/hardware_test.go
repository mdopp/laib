package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/sysd"
)

const smartHealthy = `{"smart_status":{"passed":true},"temperature":{"current":31},"ata_smart_attributes":{"table":[{"id":5,"name":"Reallocated_Sector_Ct","raw":{"value":0}},{"id":197,"name":"Current_Pending_Sector","raw":{"value":0}}]}}`
const smartPending = `{"smart_status":{"passed":true},"temperature":{"current":34},"ata_smart_attributes":{"table":[{"id":5,"name":"Reallocated_Sector_Ct","raw":{"value":12}},{"id":197,"name":"Current_Pending_Sector","raw":{"value":3}}]}}`
const smartFailed = `{"smart_status":{"passed":false},"temperature":{"current":41},"ata_smart_attributes":{"table":[]}}`
const smartNVMeWorn = `{"smart_status":{"passed":true},"temperature":{"current":58},"nvme_smart_health_information_log":{"percentage_used":94,"media_errors":2}}`

// #59: disk health from smartctl --json for the array's members and the
// OS disk — pending or reallocated sectors, media errors and wear are
// yellow, a failed self-assessment is red with "Platte bald tauschen";
// temperatures have plain thresholds.
func TestSmartFindingsFromRecordedFixtures(t *testing.T) {
	cases := []struct {
		name  string
		json  string
		sev   string
		words string
	}{
		{"healthy", smartHealthy, "", ""},
		{"pending sectors", smartPending, "gelb", "Sektoren"},
		{"failed self-assessment", smartFailed, "rot", "bald tauschen"},
		{"nvme worn and warm", smartNVMeWorn, "gelb", "abgenutzt"},
	}
	for _, tc := range cases {
		real := sysd.Run
		sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
			if strings.Contains(name+" "+strings.Join(args, " "), "smartctl") {
				return tc.json, nil
			}
			return "", nil
		}
		fs := smartFindings(context.Background(), []string{"/dev/sda"})
		sysd.Run = real
		if tc.sev == "" {
			if len(fs) != 0 {
				t.Fatalf("%s: unexpected findings %+v", tc.name, fs)
			}
			continue
		}
		var hit bool
		for _, f := range fs {
			if f.Probe == "smart" && f.Severity == tc.sev && strings.Contains(f.Detail, tc.words) && strings.Contains(f.Detail, "/dev/sda") {
				hit = true
			}
		}
		if !hit {
			t.Fatalf("%s: want %s %q, got %+v", tc.name, tc.sev, tc.words, fs)
		}
	}
	// smartctl missing: one plain hint, not a failure
	real := sysd.Run
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		return "sudo: smartctl: command not found", errors.New("exit status 1")
	}
	fs := smartFindings(context.Background(), []string{"/dev/sda"})
	sysd.Run = real
	if len(fs) != 1 || fs[0].Severity != "info" || !strings.Contains(fs[0].Fix, "smartmontools") {
		t.Fatalf("without smartctl: %+v", fs)
	}
}

// #59: the disks to watch are the array's members and the disk the OS
// lives on; the temperature comes from the kernel's thermal zones.
func TestHardwareDevicesAndThermal(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mdstat"), []byte("Personalities : [raid1] \nmd127 : active raid1 vdb1[0] vdc1[1]\n      9426880 blocks super 1.2 [2/2] [UU]\n\nunused devices: <none>\n"), 0o644)
	devs := hardwareDevices(filepath.Join(dir, "mdstat"), "/dev/vda4")
	want := "/dev/vda /dev/vdb /dev/vdc"
	if strings.Join(devs, " ") != want {
		t.Fatalf("devices %v, want %s", devs, want)
	}
	os.MkdirAll(filepath.Join(dir, "thermal_zone0"), 0o755)
	os.MkdirAll(filepath.Join(dir, "thermal_zone1"), 0o755)
	os.WriteFile(filepath.Join(dir, "thermal_zone0", "temp"), []byte("45000\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "thermal_zone1", "temp"), []byte("91000\n"), 0o644)
	fs := thermalFindings(dir)
	if len(fs) != 1 || fs[0].Severity != "rot" || !strings.Contains(fs[0].Detail, "91") {
		t.Fatalf("91 °C must be red: %+v", fs)
	}
	os.WriteFile(filepath.Join(dir, "thermal_zone1", "temp"), []byte("82000\n"), 0o644)
	if fs := thermalFindings(dir); len(fs) != 1 || fs[0].Severity != "gelb" {
		t.Fatalf("82 °C must be yellow: %+v", fs)
	}
	os.WriteFile(filepath.Join(dir, "thermal_zone1", "temp"), []byte("60000\n"), 0o644)
	if fs := thermalFindings(dir); len(fs) != 0 {
		t.Fatalf("60 °C is fine: %+v", fs)
	}
}
