package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
)

// The recovery sheet (#40): one printed page is what stands between a dead
// data disk and the family's data. The age key and the restic password
// both live on that disk and inside the encrypted backup, so without the
// page an off-box repo is useless. Humans only, never over MCP: a hand
// that can print the sheet can take the box.

func sheetConfirmed(env *cli.Env) string {
	return filepath.Join(env.Paths.Home, "recovery-sheet.confirmed")
}

func RecoverySheet(ctx context.Context, env *cli.Env, args []string) int {
	if proposal.Hand(env.Actor) != "human" || env.MCP {
		return cli.Refused(env, "the recovery sheet is for the human at the console, never for a hand or over MCP")
	}
	if len(args) > 0 && args[0] == "--confirm" {
		if err := os.WriteFile(sheetConfirmed(env), []byte(time.Now().UTC().Format(time.RFC3339)+" "+env.Actor+"\n"), 0o644); err != nil {
			return env.Fail(err)
		}
		if st, err := cli.LoadState(env); err == nil {
			cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "recovery-sheet", Result: "info", Detail: "als gesichert bestätigt (ausgedruckt oder im Passwortmanager)"})
		}
		fmt.Fprintln(env.W(), "bestätigt: das Wiederherstellungsblatt ist gesichert; diag hört auf zu mahnen")
		return cli.ExitOK
	}
	st, pw, err := Prep(env)
	if err != nil {
		return env.Fail(err)
	}
	keyFile, err := os.ReadFile(env.Paths.Key)
	if err != nil {
		return env.Fail(errors.New("no age key at " + env.Paths.Key + ": laib init first"))
	}
	key := ""
	for _, line := range strings.Split(string(keyFile), "\n") {
		if strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			key = line
		}
	}
	if key == "" {
		return env.Fail(errors.New("no AGE-SECRET-KEY line in " + env.Paths.Key))
	}
	repo := st.Box.Backup.Repo
	if repo == "" {
		repo = "(kein backup.repo in box.yml — ohne Repo gibt es nichts wiederherzustellen)"
	}
	fmt.Fprintf(env.W(), `══════════════════════════════════════════════════════════════════════
  LAIB — WIEDERHERSTELLUNGSBLATT · %s · Stand %s
  Ausdrucken oder im Passwortmanager ablegen. Ohne dieses Blatt ist das
  Backup nach einem Plattenverlust nicht lesbar.
══════════════════════════════════════════════════════════════════════

age-Schlüssel (Datei /mnt/data/laib/age.key, eine Zeile):
  %s

restic-Repo:        %s
restic-Passwort:    %s

Wiederherstellen auf einer neuen oder neu aufgesetzten Box — vier Schritte:
  1. Box vom Laib-Stick installieren (laib-stick, --reinstall wenn die Box
     schon einmal installiert war). Warten bis "laib status" antwortet.
  2. Den age-Schlüssel von oben als /mnt/data/laib/age.key ablegen
     (eine Zeile, chmod 600). Backup-Ziel erreichbar machen (NAS mounten,
     USB-Platte anstecken) und in /mnt/data/laib/state/box.yml unter
     backup: repo: das Repo von oben eintragen, falls es abweicht.
  3. RESTIC_PASSWORD='%s' laib restore --box latest
     — stellt State und Journal wieder her, dann jeden Dienst in Reihenfolge,
     dann ein apply. Dauer: Minuten bis Stunden, je nach Daten.
  4. "laib status" muss grün sein, "laib diag" nichts Rotes zeigen. Danach
     "laib recovery-sheet" neu ausdrucken (Schlüssel und Passwort sind gleich,
     das Blatt bestätigt sich mit --confirm).

Nach dem Lesen: laib recovery-sheet --confirm  (sonst mahnt laib diag)
══════════════════════════════════════════════════════════════════════
`, st.Box.Domain, time.Now().Format("02.01.2006 15:04"), key, repo, pw, pw)
	if _, err := os.Stat(sheetConfirmed(env)); err != nil {
		fmt.Fprintln(env.Errw(), "laib: noch nicht bestätigt — nach dem Ausdrucken: laib recovery-sheet --confirm")
	}
	return cli.ExitOK
}
