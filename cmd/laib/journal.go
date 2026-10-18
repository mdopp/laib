package main

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
)

func cmdJournal(_ context.Context, env *cliEnv, args []string) int {
	since := 24 * time.Hour
	for i := 0; i < len(args); i++ {
		if args[i] == "--since" && i+1 < len(args) {
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				return env.Fail(err)
			}
			since = d
			i++
		}
	}
	es, err := journal.Read(env.Paths.Journal, time.Now().Add(-since))
	if err != nil {
		return env.Fail(err)
	}
	if env.JSON {
		return cli.Emit(env, es)
	}
	for _, e := range es {
		fmt.Fprintf(env.W(), "%s  %-14s %-8s %-12s %-5s %s %s\n", e.T.Local().Format("02.01. 15:04:05"), e.Actor, e.Verb, e.Service, e.Result, e.Reason, e.Detail)
	}
	return cli.ExitOK
}

// cmdNote: an agent writes what it concluded. `--kind report` is the morning
// report (the Box page shows the latest), `incident` a repair story,
// `watch` a routine finding. Journaled as the hand; the webhook carries
// reports and incidents.
func cmdNote(_ context.Context, env *cliEnv, args []string) int {
	kind := "watch"
	var words []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--kind" && i+1 < len(args) {
			kind = args[i+1]
			i++
			continue
		}
		words = append(words, args[i])
	}
	text := strings.TrimSpace(strings.Join(words, " "))
	if len(text) > 4000 {
		text = text[:4000] + "…" // a note is a few sentences; the journal reader has a line limit (#35)
	}
	if text == "" {
		return env.Fail(errors.New("usage: laib note [--kind report|incident|watch] \"<text>\""))
	}
	if kind != "report" && kind != "incident" && kind != "watch" {
		return env.Fail(fmt.Errorf("unknown kind %q", kind))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	e := journal.Entry{Actor: env.Actor, Verb: "note", Reason: kind, Result: "info", Detail: text}
	journal.Append(env.Paths.Journal, e)
	// reports and incidents reach the webhook like a red does
	if (kind == "report" || kind == "incident") && st.Box.Notify.URL != "" {
		go cli.PostEvent(env, st.Box.Notify.URL, e)
		time.Sleep(50 * time.Millisecond)
	}
	if kind == "report" {
		dir := filepath.Join(env.Paths.Home, "reports")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, time.Now().Format("2006-01-02")+".md"), []byte(text+"\n"), 0o644)
	}
	if env.JSON {
		return cli.Emit(env, e)
	}
	fmt.Fprintln(env.W(), "notiert:", kind)
	return cli.ExitOK
}
