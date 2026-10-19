package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Hardware health (#59): for a box in a cupboard the top risks are a
// dying disk and heat. diag asks smartctl about every member of the data
// array and the OS disk, and reads the kernel's thermal zones.

var (
	mdstatPath  = "/proc/mdstat"
	thermalRoot = "/sys/class/thermal"
)

// hardwareDevices: the whole disks behind the array's members and the root
// filesystem's source (/dev/vda4 → /dev/vda), sorted, each once.
func hardwareDevices(mdstat, rootSource string) []string {
	seen := map[string]bool{}
	add := func(part string) {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "/dev/") {
			part = "/dev/" + part
		}
		disk := regexp.MustCompile(`p?\d+$`).ReplaceAllString(part, "")
		if strings.HasPrefix(disk, "/dev/nvme") && !strings.Contains(disk, "n") {
			disk = part
		}
		if disk != "/dev/" {
			seen[disk] = true
		}
	}
	if rootSource != "" {
		add(rootSource)
	}
	if b, err := os.ReadFile(mdstat); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, " : active") {
				continue
			}
			for _, f := range strings.Fields(line)[3:] {
				if i := strings.Index(f, "["); i > 0 {
					add(f[:i])
				}
			}
		}
	}
	var out []string
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

type smartReport struct {
	Status struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature struct {
		Current int `json:"current"`
	} `json:"temperature"`
	ATA struct {
		Table []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Raw  struct {
				Value int64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	NVMe struct {
		PercentageUsed int   `json:"percentage_used"`
		MediaErrors    int64 `json:"media_errors"`
	} `json:"nvme_smart_health_information_log"`
}

// smartFindings runs smartctl -H -A --json for every device.
func smartFindings(ctx context.Context, devices []string) []Finding {
	var out []Finding
	for _, dev := range devices {
		raw, err := Run(ctx, "sudo", "smartctl", "-H", "-A", "--json", dev)
		if err != nil && (strings.Contains(raw, "command not found") || strings.Contains(raw, "No such file")) {
			return []Finding{{Severity: "info", Probe: "smart", Detail: "smartctl fehlt: der Zustand der Platten ist unbekannt", Fix: "sudo rpm-ostree install smartmontools (ein Reboot später ist es da; install-box.sh ändert das OS nicht, #367)"}}
		}
		var r smartReport
		if i := strings.Index(raw, "{"); i >= 0 {
			raw = raw[i:]
		}
		if jerr := json.Unmarshal([]byte(raw), &r); jerr != nil {
			if err != nil {
				out = append(out, Finding{Severity: "gelb", Probe: "smart", Detail: dev + ": smartctl antwortet nicht lesbar (" + tail(strings.TrimSpace(raw), 120) + ")", Fix: "sudo smartctl -a " + dev})
			}
			continue
		}
		if !r.Status.Passed {
			out = append(out, Finding{Severity: "rot", Probe: "smart", Detail: dev + ": SMART-Selbsttest nicht bestanden — Platte bald tauschen", Fix: "Daten sind gesichert (laib backup); Platte ersetzen, bei der Datenplatte laib disk add für den Spiegel"})
			continue
		}
		var bad []string
		for _, a := range r.ATA.Table {
			switch a.ID {
			case 5, 197, 198:
				if a.Raw.Value > 0 {
					bad = append(bad, fmt.Sprintf("%s=%d", a.Name, a.Raw.Value))
				}
			}
		}
		if len(bad) > 0 {
			out = append(out, Finding{Severity: "gelb", Probe: "smart", Detail: dev + ": defekte oder schwebende Sektoren (" + strings.Join(bad, ", ") + ") — die Platte altert", Fix: "Backup prüfen (laib backup check); Ersatzplatte besorgen"})
		}
		if r.NVMe.MediaErrors > 0 {
			out = append(out, Finding{Severity: "gelb", Probe: "smart", Detail: fmt.Sprintf("%s: %d Medienfehler", dev, r.NVMe.MediaErrors), Fix: "Backup prüfen; Platte beobachten"})
		}
		if r.NVMe.PercentageUsed >= 90 {
			out = append(out, Finding{Severity: "gelb", Probe: "smart", Detail: fmt.Sprintf("%s: zu %d %% abgenutzt", dev, r.NVMe.PercentageUsed), Fix: "Ersatz einplanen"})
		}
		if t := r.Temperature.Current; t >= 65 {
			out = append(out, Finding{Severity: "rot", Probe: "smart", Detail: fmt.Sprintf("%s: %d °C — zu heiß", dev, t), Fix: "Lüftung, Staub, Standort"})
		} else if t >= 55 {
			out = append(out, Finding{Severity: "gelb", Probe: "smart", Detail: fmt.Sprintf("%s: %d °C — warm", dev, t), Fix: "Lüftung prüfen"})
		}
	}
	return out
}

// thermalFindings reads the hottest thermal zone (millidegrees).
func thermalFindings(root string) []Finding {
	zones, _ := filepath.Glob(filepath.Join(root, "thermal_zone*", "temp"))
	max := 0
	for _, z := range zones {
		b, err := os.ReadFile(z)
		if err != nil {
			continue
		}
		if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && v/1000 > max {
			max = v / 1000
		}
	}
	switch {
	case max >= 90:
		return []Finding{{Severity: "rot", Probe: "thermal", Detail: fmt.Sprintf("die Box ist %d °C heiß", max), Fix: "sofort für Luft sorgen: Lüftungsschlitze, Staub, kein geschlossener Schrank"}}
	case max >= 80:
		return []Finding{{Severity: "gelb", Probe: "thermal", Detail: fmt.Sprintf("die Box ist %d °C warm", max), Fix: "Lüftung prüfen"}}
	}
	return nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// hardwareFindings: devices from the array and the root.
func hardwareFindings(ctx context.Context) []Finding {
	root, _ := Run(ctx, "findmnt", "-n", "-o", "SOURCE", "/")
	root = strings.TrimSpace(root)
	if !strings.HasPrefix(root, "/dev/") {
		root = ""
	}
	devs := hardwareDevices(mdstatPath, root)
	var out []Finding
	if len(devs) > 0 {
		out = append(out, smartFindings(ctx, devs)...)
	}
	return append(out, thermalFindings(thermalRoot)...)
}
