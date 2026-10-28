package diag

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
)

// The probes below are the ones the pages run live (#856): diag and the
// Status page, /ready and laib status reach their verdict through the same
// functions, so what the mail calls red is red there too.

// SelfFindings are what the service's status file reports, with its age.
func SelfFindings(rep Report, now time.Time) []Finding {
	var out []Finding
	for _, f := range rep.Findings {
		if a := Age(rep.At, now); a != "" {
			f.Detail += " (" + a + ")"
		}
		out = append(out, f)
	}
	return out
}

// Health is the service's health probe; nil while it answers or has none.
func Health(ctx context.Context, svc *state.Service) *Finding {
	h := svc.Contract.Health.HTTP
	if h == "" {
		return nil
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h, nil)
	resp, err := client.Do(req)
	detail := ""
	if err != nil {
		detail = err.Error()
	} else {
		resp.Body.Close()
		if !svc.Contract.Health.Answers(resp.StatusCode) {
			detail = resp.Status
		}
	}
	if detail == "" {
		return nil
	}
	return &Finding{Severity: "rot", Probe: "health", Service: svc.Name, Detail: detail, Fix: "laib logs " + svc.Name, Repair: []string{"restart", svc.Name}}
}

// DiskPressure on the data dir and the system's writable root (/var: on
// CoreOS / is the immutable, "full" composefs and would always read red).
func DiskPressure(dataDir string) []Finding {
	var out []Finding
	for _, path := range uniq([]string{dataDir, "/var"}) {
		var fs syscall.Statfs_t
		if err := syscall.Statfs(path, &fs); err != nil || fs.Blocks == 0 {
			continue
		}
		free := float64(fs.Bavail) / float64(fs.Blocks) * 100
		switch {
		case free < 5:
			out = append(out, Finding{Severity: "rot", Probe: "disk", Detail: fmt.Sprintf("%s hat nur %.1f%% frei", path, free)})
		case free < 15:
			out = append(out, Finding{Severity: "gelb", Probe: "disk", Detail: fmt.Sprintf("%s hat %.1f%% frei", path, free)})
		}
	}
	return out
}

// NFTables judges apply.FirewallState: a rendered ruleset the kernel does
// not hold is red; it used to lock readiness instead (#856, DESIGN.md).
func NFTables(firewall string) *Finding {
	if firewall != "fehlt" {
		return nil
	}
	return &Finding{Severity: "rot", Probe: "nftables", Detail: "die Firewall (table inet laib) fehlt im Kernel: die Box filtert nicht", Fix: "laib converge --force lädt sie"}
}

// OnlyOnDataDisk: the backup repo lies on the data disk it protects.
func OnlyOnDataDisk(st *state.State) bool {
	repo := st.Box.Backup.Repo
	return strings.HasPrefix(repo, "/") && strings.HasPrefix(repo, filepath.Dir(st.Box.DataDir))
}

// Backup is what the journal says the backups did and what diag finds about
// them: the target, the nightly run, the recovery sheet and the restore test.
func Backup(env *cli.Env, st *state.State) (BackupFacts, []Finding) {
	var out []Finding
	// The backup target: none is yellow, one on the data disk itself is a nudge
	// for a week and yellow after it (#40) — a risk, not damage (owner 2026-10-25:
	// „Rot bleibt rot“, so a deferred Box-Aufgabe can cover it; a failed off-box copy stays red).
	bf := backupFacts(env)
	installed, lastCheck, lastGreen, lastOffsite, lastRun := bf.installed, bf.RestoreTest, bf.LastGreen, bf.Offsite, bf.Nightly.Result
	switch repo := st.Box.Backup.Repo; {
	case st.Box.Backup.Offsite != nil && repo != "":
		if time.Since(lastOffsite) > 48*time.Hour { // #391: the copy off the data disk is the one a dead disk leaves
			out = append(out, Finding{Severity: "rot", Probe: "backup", Detail: "keine frische Kopie außer Haus (" + st.Box.Backup.Offsite.Repo + ") seit mehr als 48 Stunden: ein Plattenausfall nähme alles mit", Fix: "laib journal --verb backup (reason offsite); NAS erreichbar? laib backup", Topic: "offsite"})
		}
	case repo == "":
		out = append(out, Finding{Severity: "gelb", Probe: "backup", Detail: "kein Backup-Ziel (box.yml backup.repo): keine Snapshots, der Operator sichert nicht vor Reparaturen", Fix: "backup.repo setzen, z.B. /mnt/data/backup/restic oder sftp:nas:/backup/laib"})
	case OnlyOnDataDisk(st):
		out = append(out, Finding{Severity: localOnlySeverity(installed), Probe: "backup", Detail: "Backup liegt nur auf der Datenplatte (" + repo + "): schützt vor Fehlern, nicht vor Plattenverlust", Fix: "Repo auf NAS oder USB (box.yml backup.repo), oder ein Spiegel (laib disk add)", Topic: "offsite"})
	}
	// a failed or stale nightly backup (#393: it is what the owner gets a mail for)
	if st.Box.Backup.Repo != "" && (lastRun == "red" || !installed.IsZero() && time.Since(installed) > 36*time.Hour && time.Since(lastGreen) > 36*time.Hour) {
		what := map[bool]string{true: "ist noch nie gelaufen"}[lastGreen.IsZero()]
		out = append(out, Finding{Severity: "rot", Probe: "backup", Detail: "die letzte Sicherung " + cmp.Or(what, "ist fehlgeschlagen oder älter als 36 Stunden"), Fix: "laib journal (verb backup); laib backup", Repair: []string{"backup"}})
	}
	if st.Box.Backup.Repo != "" {
		if _, err := os.Stat(filepath.Join(env.Paths.Home, "recovery-sheet.confirmed")); err != nil {
			out = append(out, Finding{Severity: "gelb", Probe: "recovery", Detail: "das Wiederherstellungsblatt (age-Schlüssel, restic-Passwort) ist nicht als gesichert bestätigt: ohne es ist das Backup nach Plattenverlust unlesbar", Fix: "laib recovery-sheet  → ausdrucken, dann laib recovery-sheet --confirm"})
		}
		if lastCheck.IsZero() || time.Since(lastCheck) > 35*24*time.Hour {
			out = append(out, Finding{Severity: "gelb", Probe: "restore-test", Detail: "kein grüner Wiederherstellungstest in den letzten 35 Tagen (laib backup check: restic check + Probe-Restore)", Fix: "laib backup check; der Timer laib-backup-check läuft monatlich", Repair: []string{"backup", "check"}})
		}
	}
	return bf, out
}

// Live are the probes the pages run themselves; Kept leaves them out.
var Live = []string{"unit", "health", "status", "disk", "backup", "operator", "nftables"}

type kept struct {
	At       time.Time `json:"at"`
	Findings []Finding `json:"findings"`
}

func keptPath(home string) string { return filepath.Join(home, "diag.json") }

// keep writes a whole diag run's findings, the ones the mail reads (#856).
func keep(home string, fs []Finding, now time.Time) {
	if b, err := json.Marshal(kept{At: now.UTC(), Findings: fs}); err == nil {
		os.WriteFile(keptPath(home), b, 0o644)
	}
}

// Kept are the red findings of the last whole diag run that the pages do
// not probe themselves (RAID, SMART, the certificate, the box's config),
// each with the time of that run: a slow probe is not run on every look.
func Kept(home string) []Finding {
	var k kept
	b, err := os.ReadFile(keptPath(home))
	if err != nil || json.Unmarshal(b, &k) != nil {
		return nil
	}
	var out []Finding
	for _, f := range k.Findings {
		if f.Severity != "rot" || slices.Contains(Live, f.Probe) {
			continue
		}
		f.Detail += " (laib diag " + k.At.Local().Format("02.01. 15:04") + ")"
		out = append(out, f)
	}
	return out
}
