package diag

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// OSState is what rpm-ostree has booted and staged (#368), and the
// container image the booted deployment came from (#486).
type OSState struct{ Booted, BootedVersion, Staged, Image string }

// ForeignImage (#486): booted from a container image Zincati's update graph
// does not serve (uCore); Fedora CoreOS's own image is Zincati's.
func (o OSState) ForeignImage() bool {
	return o.Image != "" && !strings.Contains(o.Image, "quay.io/fedora/fedora-coreos")
}

// OS reads rpm-ostree; ok is false on a box without it.
func OS(ctx context.Context) (o OSState, ok bool) {
	out, err := sysd.Run(ctx, "rpm-ostree", "status", "--json")
	var s struct {
		Deployments []struct {
			Booted, Staged    bool
			Checksum, Version string
			Serial            int
			Image             string `json:"container-image-reference"`
		}
	}
	if err != nil || json.Unmarshal([]byte(out), &s) != nil {
		return o, false
	}
	for _, d := range s.Deployments {
		if d.Booted {
			o.Booted, o.BootedVersion, o.Image = fmt.Sprintf("%s.%d", d.Checksum, d.Serial), d.Version, d.Image // the boot check's id: commit and serial
		}
		if d.Staged {
			o.Staged = d.Version
		}
	}
	return o, true
}

// OSPause (#486) is the zincati/config.d drop-in that switches OS updates
// off, "" when none does. Read as Zincati reads them, in name order, the
// last one that sets updates.enabled decides; laib-host-image reads the
// same files the same way: one pause for both update paths.
func OSPause() string {
	files, _ := filepath.Glob(filepath.Join(cmp.Or(os.Getenv("LAIB_ETC"), "/etc"), "zincati", "config.d", "*.toml"))
	pause := ""
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, l := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == "enabled" {
				pause = map[bool]string{true: f}[strings.TrimSpace(v) == "false"]
			}
		}
	}
	return pause
}

// osFindings (#368, #486): what the host's update path says. window is
// WindowText's.
func osFindings(o OSState, pinned, zincati bool, pause, window string) (out []Finding) {
	f := func(sev, detail, fix string) {
		out = append(out, Finding{Severity: sev, Probe: "os-update", Detail: detail, Fix: fix})
	}
	switch {
	case filepath.Base(pause) == "95-laib-boot-check.toml":
		f("gelb", "OS-Updates pausiert ("+pause+"): ein Update ist nach dem Start zurückgerollt worden; kein neues wird bereitgestellt", "die Ursache suchen (journalctl -b -1 -u laib-boot-check), dann sudo rm "+pause)
	case pause != "":
		f("info", "OS-Updates pausiert ("+pause+"): kein Update wird bereitgestellt oder gestartet", "sudo rm "+pause+", wenn die Pause nicht mehr nötig ist")
	case o.Staged != "":
		f("info", "OS-Update "+o.Staged+" bereitgestellt: der Neustart kommt im Fenster "+window+", wenn alles grün und der laufende Stand bewiesen ist", "")
	}
	if !o.ForeignImage() {
		return out
	}
	if !pinned {
		f("gelb", "das Betriebssystem läuft aus "+o.Image+" und ist nicht gepinnt (state/host/image): diese Box bekommt keine OS-Updates", "install-box.sh pinnt das gebootete Image (docs/INSTALL.md, OS updates)")
	}
	if zincati {
		f("gelb", "Zincati läuft, aber Fedoras Update-Graph kennt dieses Image nicht: es findet nie ein Update (\"not found in the update graph\"); der Updateweg ist laib-host-image", "install-box.sh --box-config (maskiert Zincati auf dieser Box)")
	}
	return out
}

// Proven: the boot check proved this deployment (/var/lib/laib/boot-proven,
// shared by every deployment; /etc/laib from before).
func Proven(booted string) bool {
	b, err := os.ReadFile(cmp.Or(os.Getenv("LAIB_VAR"), "/var/lib/laib") + "/boot-proven")
	if err != nil {
		b, _ = os.ReadFile(cmp.Or(os.Getenv("LAIB_ETC"), "/etc") + "/laib/boot-proven")
	}
	f := strings.Fields(string(b))
	return booted != "" && len(f) > 0 && strings.HasPrefix(booted, f[0])
}

// window is the OS update window in minutes of the day: two to four hours
// after the backup (backup.schedule, default 03:00), never in its slot.
func window(st *state.State) (start, end int) {
	t, err := time.Parse("15:04", cmp.Or(st.Box.Backup.Schedule, "03:00"))
	if err != nil {
		t, _ = time.Parse("15:04", "03:00")
	}
	start = (t.Hour()*60 + t.Minute() + 120) % 1440
	return start, (start + 120) % 1440
}

// InWindow: now is inside the OS update window.
func InWindow(st *state.State, now time.Time) bool {
	s, e := window(st)
	m := now.Hour()*60 + now.Minute()
	return (s < e && m >= s && m < e) || (s > e && (m >= s || m < e))
}

// WindowText is the window as the family reads it, "05:00–07:00".
func WindowText(st *state.State) string {
	s, e := window(st)
	return fmt.Sprintf("%02d:%02d–%02d:%02d", s/60, s%60, e/60, e%60)
}
