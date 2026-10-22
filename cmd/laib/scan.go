package main

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

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
)

var (
	scanActions = []string{"start", "weiter", "abschliessen", "verwerfen", "redo", "suchen", "pruefen", "probe", "einrichten"}
	// a SANE device as scanimage -L names it; nothing that could break the unit's line (#571)
	scanGeraet = regexp.MustCompile(`^[a-z0-9_]+:[^"'\\\n\r%$]{1,120}$`)
	scanStapel = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}$`)
	scanPerson = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

// cmdScan: `laib scan [status] | start | weiter | abschliessen | verwerfen |
// redo --stapel <zeit> | suchen | pruefen | probe` [--person p] (#568, #571).
// Status is the scan service's status file as it wrote it; every other
// action is an order file in its data folder, ${DATA_DIR}/scan/auftrag/,
// which the service picks up within seconds. `einrichten [--geraet d]` is a
// proposal: the scan template with that scanner, or SCANNER set on the
// installed one. laib runs no scan itself and decides nothing about one.
func cmdScan(ctx context.Context, env *cliEnv, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	var svc *state.Service
	for _, s := range st.Services {
		if slices.Contains(s.Contract.Provides, "scan") {
			svc = s
		}
	}
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
		return env.Fail(usage)
	}
	if act == "einrichten" {
		return scanEinrichten(ctx, env, st, svc, *geraet)
	}
	if svc == nil || state.StatusPath(st, svc) == "" {
		return cli.Refused(env, "Der Dienst scan ist nicht eingerichtet.")
	}
	if act == "status" {
		b, err := os.ReadFile(state.StatusPath(st, svc))
		if err != nil {
			return cli.Refused(env, "Der Dienst scan hat noch keinen Zustand geschrieben.")
		}
		fmt.Fprintln(env.W(), string(b))
		return cli.ExitOK
	}
	switch {
	case (act == "redo") != (*stapel != "") || (*stapel != "" && !scanStapel.MatchString(*stapel)):
		return env.Fail(fmt.Errorf("redo braucht --stapel <zeit> (JJJJ-MM-TTThh-mm-ss): %w", usage))
	case *person != "" && !scanPerson.MatchString(*person):
		return env.Fail(fmt.Errorf("--person %q: ein Name aus box.yml people, klein", *person))
	}
	dir := filepath.Join(filepath.Dir(state.StatusPath(st, svc)), "auftrag")
	b, _ := json.Marshal(map[string]string{"aktion": act, "stapel": *stapel, "person": *person, "von": env.Actor, "at": time.Now().UTC().Format(time.RFC3339)})
	name := fmt.Sprintf("%d", time.Now().UnixNano())
	// written whole, then renamed: the service never reads half an order
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return env.Fail(err)
	}
	if err := boxfs.WriteAtomic(filepath.Join(dir, "."+name), filepath.Join(dir, name+".json"), b, 0o644); err != nil {
		return env.Fail(err)
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "scan", Service: svc.Name, Result: "info", Detail: act + " " + *stapel})
	return cli.Emit(env, map[string]string{"message": fmt.Sprintf("Auftrag „%s“ angenommen; was der Scanner tut, steht in laib scan status.", act)})
}

// scanEinrichten proposes the scanner (#571): the scan template with it when
// scan is not there, else its SCANNER value. The box's dial and review
// decide the proposal like any other; Haus decides nothing.
func scanEinrichten(ctx context.Context, env *cliEnv, st *state.State, svc *state.Service, geraet string) int {
	if (geraet != "" && !scanGeraet.MatchString(geraet)) || (geraet == "" && svc != nil) {
		return env.Fail(fmt.Errorf("--geraet %q: ein SANE-Gerät wie epson2:net:192.168.1.5, ohne Anführungszeichen, %% und $", geraet))
	}
	var edit proposal.Edit
	reason := "Den Scanner einrichten: SCANNER=" + geraet
	if svc == nil {
		tpl, err := inbox.TemplateFS(env, "scan")
		if err != nil {
			return env.Fail(err)
		}
		edit.Add = &proposal.AddService{Name: "scan", Template: tpl, Values: map[string]string{}}
		if geraet != "" {
			edit.Add.Values["SCANNER"] = geraet
		}
		reason = "Den Dienst scan einrichten: Papierstapel nach Paperless (SCANNER=" + geraet + ")"
	} else {
		edit.Set = map[string]string{"services/" + svc.Name + "/values.yml:SCANNER": geraet}
	}
	p, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), env.Actor, reason, edit, inbox.JudgeFor(st, env.Actor))
	if err != nil {
		return env.Fail(err)
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "scan", Service: "scan", Result: "info", Reason: reason, Detail: fmt.Sprintf("#%d (%s)", p.ID, p.Class)})
	return cli.Emit(env, map[string]any{"proposal": p.ID, "class": p.Class, "message": fmt.Sprintf("Vorschlag #%d liegt im Eingang: %s.", p.ID, reason)})
}
