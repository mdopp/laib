package diag

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// OSState is what rpm-ostree has booted and staged (#368).
type OSState struct{ Booted, BootedVersion, Staged string }

// OS reads rpm-ostree; ok is false on a box without it.
func OS(ctx context.Context) (o OSState, ok bool) {
	out, err := sysd.Run(ctx, "rpm-ostree", "status", "--json")
	var s struct {
		Deployments []struct {
			Booted, Staged    bool
			Checksum, Version string
			Serial            int
		}
	}
	if err != nil || json.Unmarshal([]byte(out), &s) != nil {
		return o, false
	}
	for _, d := range s.Deployments {
		if d.Booted {
			o.Booted, o.BootedVersion = fmt.Sprintf("%s.%d", d.Checksum, d.Serial), d.Version // the boot check's id: commit and serial
		}
		if d.Staged {
			o.Staged = d.Version
		}
	}
	return o, true
}

// Proven: the boot check proved this deployment (/etc/laib/boot-proven).
func Proven(booted string) bool {
	b, _ := os.ReadFile(cmp.Or(os.Getenv("LAIB_ETC"), "/etc") + "/laib/boot-proven")
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
