// The predecessor's data dir (#665, ADR 0010): the values ServiceBay held
// stay readable there until it goes, in plaintext in its unit history and
// sealed in config.json with secret.key beside it. The probe names every
// secret whose value is still one of them; once none is, it names the dir
// with `laib servicebay remove` as a repair, a destroy behind a snapshot.
// This file goes with the dir.

package diag

import (
	"bytes"
	"cmp"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// ServiceBayDir is where ServiceBay kept its data, beside the data dir.
func ServiceBayDir(st *state.State) string {
	return cmp.Or(os.Getenv("LAIB_SERVICEBAY_DIR"), filepath.Join(filepath.Dir(st.Box.DataDir), "servicebay"))
}

// sealed is a value ServiceBay sealed (secrets.ts): enc:v1:iv:tag:ct in hex.
var sealed = regexp.MustCompile(`enc:v1:([0-9a-f]{32}):([0-9a-f]{32}):([0-9a-f]+)`)

var snapID = regexp.MustCompile(`snapshot (\w+) saved`)

// Carried names the secrets whose value is in a file under dir, plain or
// sealed with the dir's secret.key; unread counts the files Laib cannot
// read or did not, past budget bytes (0: no budget). A value shorter than 8
// bytes is a word, not a password.
func Carried(dir string, sec map[string]string, budget int64) (names []string, unread int, err error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, 0, err
	}
	key, _ := os.ReadFile(filepath.Join(dir, "secret.key"))
	found := map[string]bool{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			unread++
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil || budget < 0 {
			unread++
			return nil
		}
		b, _ := io.ReadAll(io.LimitReader(f, 64<<20)) // a log or an archive past that is not searched, nor a compressed one
		f.Close()
		if budget > 0 && budget <= int64(len(b)) {
			budget = -1
		} else if budget > 0 {
			budget -= int64(len(b))
		}
		texts := [][]byte{b}
		for _, m := range sealed.FindAllSubmatch(b, -1) {
			if v := unseal(key, m); v != nil {
				texts = append(texts, v)
			}
		}
		for n, v := range sec {
			for _, t := range texts {
				if len(v) >= 8 && !strings.HasSuffix(n, ".next") && bytes.Contains(t, []byte(v)) {
					found[n] = true
				}
			}
		}
		return nil
	})
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, unread, err
}

func unseal(key []byte, m [][]byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil
	}
	gcm, _ := cipher.NewGCMWithNonceSize(block, 16)
	iv, _ := hex.DecodeString(string(m[1]))
	tag, _ := hex.DecodeString(string(m[2]))
	ct, _ := hex.DecodeString(string(m[3]))
	plain, err := gcm.Open(nil, iv, append(ct, tag...), nil)
	if err != nil {
		return nil
	}
	return plain
}

func servicebayFindings(st *state.State, sec map[string]string) []Finding {
	dir := ServiceBayDir(st)
	names, unread, err := Carried(dir, sec, 256<<20) // diag runs often: a look, not a scan
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []Finding{{Severity: "gelb", Probe: "servicebay", Detail: "der alte ServiceBay-Ordner " + dir + " ist nicht lesbar (" + err.Error() + "): ob dort noch ein Passwort von heute liegt, ist offen", Unmeasured: true}}
	}
	var out []Finding
	for _, n := range names {
		svc, fix, repair := carriedFix(st, n)
		out = append(out, Finding{Severity: "gelb", Probe: "servicebay", Service: svc, Detail: n + " hat noch den Wert aus ServiceBay, lesbar in " + dir, Fix: fix, Repair: repair})
	}
	if unread > 0 {
		out = append(out, Finding{Severity: "gelb", Probe: "servicebay", Detail: fmt.Sprintf("%d Dateien in %s hat Laib nicht gelesen (nicht lesbar, oder über 256 MiB): ob dort noch ein Passwort von heute liegt, ist offen; laib servicebay remove liest alles", unread, dir), Unmeasured: true})
	}
	if len(out) == 0 {
		out = append(out, Finding{Severity: "gelb", Probe: "servicebay", Detail: "der alte ServiceBay-Ordner " + dir + " liegt noch; kein Passwort hat mehr seinen Wert", Fix: "laib repair servicebay: löschen, vorher ein Snapshot, ein Mensch gibt frei", Repair: []string{"servicebay", "remove"}})
	}
	return out
}

// carriedFix: the service that declares name, the way to rotate it, and the
// repair a human approves in the Inbox when laib can rotate it itself (#665).
func carriedFix(st *state.State, name string) (string, string, []string) {
	for _, svc := range st.ServiceNames() {
		for _, s := range st.Services[svc].Contract.Secrets {
			switch {
			case s.Name != name || s.External:
			case s.DataBound && !s.Rotate:
				return svc, "in der App ändern, dann laib secret set " + name + " und laib apply " + svc + " (scripts/rotate-carried.sh --wie " + name + ")", nil
			default:
				return svc, "laib repair servicebay " + svc + ": nach der Freigabe läuft laib secret rotate " + name + ", dann grün geprüft", []string{"secret", "rotate", name}
			}
		}
	}
	return "", "beim Anbieter ein neues setzen, dann laib secret set " + name + " (Wert auf stdin)", nil
}

// ServiceBay is `laib servicebay remove` (destroy, humans only): the old
// data dir goes once no secret has a value from it, after a snapshot of it.
func ServiceBay(ctx context.Context, env *cli.Env, args []string) int {
	if len(args) != 1 || args[0] != "remove" {
		return env.Fail(errors.New("usage: laib servicebay remove"))
	}
	if proposal.Hand(env.Actor) != "human" {
		return cli.Refused(env, "nur ein Mensch löscht den alten ServiceBay-Ordner")
	}
	st, pw, err := backup.Prep(env)
	if err != nil {
		return env.Fail(fmt.Errorf("kein Löschen ohne Snapshot: %w", err))
	}
	dir := ServiceBayDir(st)
	sec, err := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	if err != nil {
		return env.Fail(err)
	}
	names, unread, err := Carried(dir, sec, 0)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintln(env.W(), dir+" ist schon weg")
		return cli.ExitOK
	case err != nil || unread > 0:
		return env.Fail(fmt.Errorf("%s nicht ganz lesbar (%d Dateien, %v): ob dort noch ein Passwort von heute liegt, ist offen", dir, unread, err))
	case len(names) > 0: // the snapshot would keep live values
		return env.Fail(fmt.Errorf("noch nicht: %s haben den Wert aus %s; erst rotieren (laib diag, scripts/rotate-carried.sh)", strings.Join(names, ", "), dir))
	}
	out, err := backup.ResticRun(ctx, st, pw, "backup", "--tag", "pre-destroy", "--tag", "servicebay", dir)
	if err != nil {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "servicebay", Result: "red", Detail: "Snapshot vor dem Löschen: " + err.Error()})
		return env.Fail(fmt.Errorf("kein Löschen, der Snapshot vorher ist fehlgeschlagen: %w", err))
	}
	what := "Snapshot ohne Kennung"
	if m := snapID.FindStringSubmatch(out); m != nil {
		what = "Snapshot " + m[1]
	}
	if o, err := sysd.Run(ctx, "podman", "unshare", "rm", "-rf", "--", dir); err != nil {
		cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "servicebay", Result: "red", Detail: "löschen: " + strings.TrimSpace(o) + "; " + what})
		return env.Fail(fmt.Errorf("%s nicht gelöscht (%s): %v", dir, what, err))
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "servicebay", Result: "green", Detail: dir + " gelöscht; " + what + " (restic, tag pre-destroy)"})
	fmt.Fprintf(env.W(), "gelöscht: %s · %s (restic, Tag pre-destroy)\n", dir, what)
	return cli.ExitOK
}
