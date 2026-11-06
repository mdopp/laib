package diag

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

func put(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// laib auslastung (owner 2026-11-06): the values from fake /proc and /sys, a
// fake nvidia-smi and a fake lease; a missing sensor is said, not zero.
func TestAuslastungReadsTheBox(t *testing.T) {
	proc, sys, bin := t.TempDir(), t.TempDir(), t.TempDir()
	put(t, proc, "stat", "cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 1 2 3\n")
	put(t, proc, "loadavg", "0.52 0.41 0.30 1/600 4242\n")
	put(t, proc, "uptime", "86400.25 1000.0\n")
	put(t, proc, "meminfo", "MemTotal:       16000 kB\nMemFree:  1000 kB\nMemAvailable:    6000 kB\nSwapTotal:  4000 kB\nSwapFree: 3000 kB\n")
	put(t, sys, "class/hwmon/hwmon0/name", "coretemp\n")
	put(t, sys, "class/hwmon/hwmon0/temp1_label", "Package id 0\n")
	put(t, sys, "class/hwmon/hwmon0/temp1_input", "54000\n")
	put(t, sys, "class/hwmon/hwmon0/temp2_label", "Core 0\n")
	put(t, sys, "class/hwmon/hwmon0/temp2_input", "49000\n")
	put(t, sys, "devices/nvme0/x", "")
	put(t, sys, "class/hwmon/hwmon1/name", "nvme\n")
	put(t, sys, "class/hwmon/hwmon1/temp1_input", "41850\n")
	if err := os.Symlink(filepath.Join(sys, "devices/nvme0"), filepath.Join(sys, "class/hwmon/hwmon1/device")); err != nil {
		t.Fatal(err)
	}
	put(t, sys, "class/hwmon/hwmon2/name", "acpitz\n")
	put(t, bin, "nvidia-smi", "#!/bin/sh\necho 'NVIDIA GeForce RTX 3090, 87, 20480, 24576, 66, 301.25, 350.00'\n")
	os.Chmod(filepath.Join(bin, "nvidia-smi"), 0o755)
	lease := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"state":"ready","holder":"foundry","mode":"exclusive","alias":""}`))
	}))
	defer lease.Close()
	procRoot, sysRoot, smi = proc, sys, filepath.Join(bin, "nvidia-smi")
	defer func() { procRoot, sysRoot, smi = "/proc", "/sys", "nvidia-smi" }()

	st := &state.State{Box: state.Box{DataDir: t.TempDir(), GPU: state.GPU{Present: true}, Model: state.Model{Lease: lease.URL}}}
	l := measure(context.Background(), st)
	if l.CPU.Load != [3]float64{0.52, 0.41, 0.30} || l.CPU.Cores < 1 || l.Uptime != 86400 {
		t.Errorf("cpu %+v uptime %d", l.CPU, l.Uptime)
	}
	if l.Mem != (Used{16000 << 10, 10000 << 10}) || l.Swap != (Used{4000 << 10, 1000 << 10}) {
		t.Errorf("mem %+v swap %+v", l.Mem, l.Swap)
	}
	if len(l.Disks) == 0 || l.Disks[0].Role != "system" || l.Disks[0].Size == 0 {
		t.Errorf("disks %+v", l.Disks)
	}
	g := l.GPU
	if g == nil || g.Name != "NVIDIA GeForce RTX 3090" || g.Pct != 87 || g.MemUsed != 20480<<20 || g.MemTotal != 24576<<20 || g.Temp != 66 || g.Power != 301.25 || g.Holder != "foundry" || g.Window != "exclusive" {
		t.Errorf("gpu %+v", g)
	}
	if len(l.Temps) != 2 || l.Temps[0] != (Temp{"cpu", 54}) || l.Temps[1] != (Temp{"nvme0", 41.85}) {
		t.Errorf("temps %+v", l.Temps)
	}
	if len(l.Missing) != 0 {
		t.Errorf("missing %v", l.Missing)
	}

	// no GPU answering, no CPU sensor: both said, nothing invented
	smi = filepath.Join(bin, "gibt-es-nicht")
	os.RemoveAll(filepath.Join(sys, "class/hwmon/hwmon0"))
	l = measure(context.Background(), st)
	if l.GPU != nil || len(l.Missing) != 2 || !strings.HasPrefix(l.Missing[0], "Grafikkarte: nvidia-smi antwortet nicht") || !strings.HasPrefix(l.Missing[1], "CPU-Temperatur") {
		t.Errorf("gpu %+v missing %v", l.GPU, l.Missing)
	}
}
