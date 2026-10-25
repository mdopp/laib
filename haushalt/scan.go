package haushalt

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

var (
	scanActions = []string{"start", "weiter", "abschliessen", "verwerfen", "redo", "suchen", "pruefen", "probe", "einrichten"}
	// a SANE device as scanimage -L names it; nothing that could break the unit's line (#571)
	scanGeraet = regexp.MustCompile(`^[a-z0-9_]+:[^"'\\\n\r%$]{1,120}$`)
	scanStapel = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}$`)
	scanPerson = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

// Scan: `laib scan [status] | start | weiter | abschliessen | verwerfen |
// redo --stapel <zeit> | suchen | pruefen | probe` [--person p] (#568, #571).
// Status is the scan service's status file as it wrote it; every other
// action is an order file in its data folder, ${DATA_DIR}/scan/auftrag/,
// which the service picks up within seconds. `einrichten [--geraet d]` is a
// proposal: the scan template with that scanner, or SCANNER set on the
// installed one. laib runs no scan itself and decides nothing about one.
func Scan(ctx context.Context, env *Env, args []string) int {
	svc := env.Call.Scan
	act := "status"
	if len(args) > 0 {
		act, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	person := fs.String("person", "", "")
	stapel := fs.String("stapel", "", "")
	geraet := fs.String("geraet", "", "")
	usage := errors.New("usage: laib scan [status] | start | weiter | abschliessen | verwerfen | redo --stapel <zeit> | suchen | pruefen | probe [--person p] | einrichten [--geraet <sane-gerät>]")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || (act != "status" && !slices.Contains(scanActions, act)) {
		return env.fail(usage)
	}
	if act == "einrichten" {
		return scanEinrichten(ctx, env, svc, *geraet)
	}
	if svc == nil || svc.Orders == "" {
		return env.refused("Der Dienst scan ist nicht eingerichtet.")
	}
	if act == "status" {
		if svc.Status == nil {
			return env.refused("Der Dienst scan hat noch keinen Zustand geschrieben.")
		}
		fmt.Fprintln(env.Out, *svc.Status)
		return exitOK
	}
	switch {
	case (act == "redo") != (*stapel != "") || (*stapel != "" && !scanStapel.MatchString(*stapel)):
		return env.fail(fmt.Errorf("redo braucht --stapel <zeit> (JJJJ-MM-TTThh-mm-ss): %w", usage))
	case *person != "" && !scanPerson.MatchString(*person):
		return env.fail(fmt.Errorf("--person %q: ein Name aus box.yml people, klein", *person))
	}
	dir := svc.Orders
	order, _ := json.Marshal(map[string]string{"aktion": act, "stapel": *stapel, "person": *person, "von": env.Actor, "at": time.Now().UTC().Format(time.RFC3339)})
	name := fmt.Sprintf("%d", time.Now().UnixNano())
	// written whole, then renamed: the service never reads half an order
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return env.fail(err)
	}
	if err := writeAtomic(filepath.Join(dir, "."+name), filepath.Join(dir, name+".json"), order, 0o644); err != nil {
		return env.fail(err)
	}
	env.journal(svc.Name, "", act+" "+*stapel)
	return env.emit(map[string]string{"message": fmt.Sprintf("Auftrag „%s“ angenommen; was der Scanner tut, steht in laib scan status.", act)})
}

// scanEinrichten proposes the scanner (#571): the scan template with it when
// scan is not there, else its SCANNER value. The box's review decides the
// proposal like any other; Haus decides nothing.
func scanEinrichten(ctx context.Context, env *Env, svc *ScanService, geraet string) int {
	if (geraet != "" && !scanGeraet.MatchString(geraet)) || (geraet == "" && svc != nil) {
		return env.fail(fmt.Errorf("--geraet %q: ein SANE-Gerät wie epson2:net:192.168.1.5, ohne Anführungszeichen, %% und $", geraet))
	}
	var edit []string
	reason := "Den Scanner einrichten: SCANNER=" + geraet
	if svc == nil {
		reason = "Den Dienst scan einrichten: Papierstapel nach Paperless (SCANNER=" + geraet + ")"
		edit = []string{"--add", "scan"}
		if geraet != "" {
			edit = append(edit, "SCANNER="+geraet)
		}
	} else {
		edit = []string{"--set", "services/" + svc.Name + "/values.yml:SCANNER=" + geraet}
	}
	if env.Laib == nil {
		return env.fail(errors.New("laib-haushalt has no way back to laib (LAIB_MCP_URL, LAIB_TOKEN)"))
	}
	id, class, err := env.Laib.Propose(ctx, env.Ceiling, append(edit, reason)...)
	if err != nil {
		return env.fail(err)
	}
	env.journal("scan", reason, fmt.Sprintf("#%d (%s)", id, class))
	return env.emit(map[string]any{"proposal": id, "class": class, "message": fmt.Sprintf("Vorschlag #%d liegt im Eingang: %s.", id, reason)})
}
