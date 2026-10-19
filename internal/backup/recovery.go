package backup

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
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
	if len(args) == 2 && args[0] == "--vaultwarden" {
		return exportLogins(env, args[1])
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
	if ls := logins(st, nil); len(ls) > 0 {
		fmt.Fprintln(env.W(), "\nAnmeldungen, die Laib angelegt hat (die Passwörter: laib recovery-sheet --vaultwarden <datei>):")
		for _, l := range ls {
			fmt.Fprintf(env.W(), "  %-16s %-20s %s  %s\n", l.Name, l.Login.Username, l.Login.URIs[0].URI, l.Notes)
		}
	}
	if _, err := os.Stat(sheetConfirmed(env)); err != nil {
		fmt.Fprintln(env.Errw(), "laib: noch nicht bestätigt — nach dem Ausdrucken: laib recovery-sheet --confirm")
	}
	return cli.ExitOK
}

// bwItem is one login in Bitwarden's unencrypted JSON export, the format
// Vaultwarden imports (Werkzeuge → Daten importieren → Bitwarden (json)).
type bwItem struct {
	FolderID string `json:"folderId"`
	Type     int    `json:"type"`
	Name     string `json:"name"`
	Notes    string `json:"notes"`
	Login    struct {
		Username string `json:"username"`
		Password string `json:"password"`
		URIs     []struct {
			URI string `json:"uri"`
		} `json:"uris"`
	} `json:"login"`
}

// logins: every secret a template marks as a human's login (#441); with
// sec, each with its password.
func logins(st *state.State, sec map[string]string) []bwItem {
	var out []bwItem
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		for _, s := range svc.Contract.Secrets {
			if s.Login == nil {
				continue
			}
			it := bwItem{Type: 1, Name: name + ": " + s.Name, Notes: strings.TrimSpace(s.Login.Note + " (Laib: Secret " + s.Name + " in secrets.age)")}
			it.Login.Username = s.Login.User
			if k, ok := strings.CutPrefix(s.Login.User, "${"); ok {
				it.Login.Username = svc.Values[strings.TrimSuffix(k, "}")].Literal
			}
			it.Login.Password = sec[s.Name]
			it.Login.URIs = make([]struct {
				URI string `json:"uri"`
			}, 1)
			if s.Login.Host != "" {
				it.Login.URIs[0].URI = "https://" + s.Login.Host + "." + st.Box.Domain + s.Login.Path
			}
			out = append(out, it)
		}
	}
	return out
}

// exportLogins writes the logins with their passwords to file, 0600 and
// never over a file that is there: the owner imports it into Vaultwarden
// and deletes it. Nothing of it goes to the terminal.
func exportLogins(env *cli.Env, file string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return env.Fail(err)
	}
	id := make([]byte, 16)
	rand.Read(id)
	folder := fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	items := logins(st, sec)
	for i := range items {
		items[i].FolderID = folder
	}
	b, _ := json.MarshalIndent(map[string]any{"encrypted": false, "folders": []map[string]string{{"id": folder, "name": "Laib " + st.Box.Domain}}, "items": items}, "", "  ")
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return env.Fail(fmt.Errorf("%w (die Datei darf noch nicht da sein)", err))
	}
	if _, err := f.Write(b); err != nil || f.Close() != nil {
		return env.Fail(fmt.Errorf("%s: unvollständig geschrieben", file))
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "recovery-sheet", Result: "info", Detail: fmt.Sprintf("%d Anmeldungen nach %s exportiert", len(items), file)})
	fmt.Fprintf(env.W(), "%d Anmeldungen in %s (nur für dich lesbar).\nIn Vaultwarden: Werkzeuge → Daten importieren → Format \"Bitwarden (json)\" → diese Datei.\nDanach löschen: shred -u %s\n", len(items), file, file)
	return cli.ExitOK
}
