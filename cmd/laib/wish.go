package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

// intakeTokenSecret is the GitHub token's name in secrets.age: a fine-grained
// PAT with Issues write on box.yml intake.repo, nothing else.
const intakeTokenSecret = "GITHUB_INTAKE_TOKEN"

// githubAPI is swapped in tests.
var githubAPI = "https://api.github.com"

// githubIssue files an issue in the intake repo and returns its URL. An
// empty intake means "not configured" (ok=false, no error).
func githubIssue(ctx context.Context, env *cliEnv, st *state.State, title, body string, labels []string) (url string, ok bool, err error) {
	if st.Box.Intake.Repo == "" {
		return "", false, nil
	}
	sec, err := secrets.Load(env.paths.Secrets, env.paths.Key)
	if err != nil {
		return "", true, err
	}
	tok := sec[intakeTokenSecret]
	if tok == "" {
		return "", true, fmt.Errorf("intake %s: no %s in secrets.age (laib secret set %s)", st.Box.Intake.Repo, intakeTokenSecret, intakeTokenSecret)
	}
	payload, _ := json.Marshal(map[string]any{"title": title, "body": body, "labels": labels})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(githubAPI, "/")+"/repos/"+st.Box.Intake.Repo+"/issues", bytes.NewReader(payload))
	if err != nil {
		return "", true, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", true, fmt.Errorf("intake %s: %w", st.Box.Intake.Repo, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusCreated {
		return "", true, fmt.Errorf("intake %s: HTTP %d: %s", st.Box.Intake.Repo, resp.StatusCode, tail(string(raw), 200))
	}
	var out struct {
		URL string `json:"html_url"`
	}
	json.Unmarshal(raw, &out)
	return out.URL, true, nil
}

func wishLabel(st *state.State) string { return cmp.Or(st.Box.Intake.Label, "wunsch") }

// wishProposal: the Operator's wish waits for the admin (ADR 0023 §6). It
// is a proposal that adds wishes/<time>.md — class human in the review —
// and approving it files the intake issue (fileWishes).
func wishProposal(ctx context.Context, env *cliEnv, st *state.State, title, body string) int {
	name := "wishes/" + time.Now().UTC().Format("2006-01-02T15-04-05.000000") + ".md"
	lines := strings.Split("# "+title+"\n\n"+body, "\n")
	patch := fmt.Sprintf("--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n+%s\n", name, len(lines), strings.Join(lines, "\n+"))
	p, err := proposal.CreateFromEdit(ctx, env.paths.State, inboxDir(env), env.actor, title, proposal.Edit{Patch: patch}, judgeFor(st, env.actor))
	if err != nil {
		return fail(err)
	}
	record(env, st, journal.Entry{Actor: env.actor, Verb: "wish", Result: "info", Reason: title, Detail: fmt.Sprintf("#%d wartet auf Freigabe", p.ID)})
	if env.json {
		return emit(env, p)
	}
	fmt.Fprintf(env.w(), "Vorschlag #%d angelegt: nach der Freigabe wird daraus ein Ticket.\n", p.ID)
	return exitOK
}

// fileWishes: the wishes an approved proposal adds become intake issues.
func fileWishes(ctx context.Context, env *cliEnv, st *state.State, p *proposal.Proposal) {
	for _, f := range p.Files {
		b, err := os.ReadFile(filepath.Join(env.paths.State, f))
		if !strings.HasPrefix(f, "wishes/") || err != nil {
			continue
		}
		title, body, _ := strings.Cut(string(b), "\n")
		title = strings.TrimPrefix(title, "# ")
		body = strings.TrimSpace(body) + fmt.Sprintf("\n\nFreigegeben von %s (#%d).", env.actor, p.ID)
		entry := journal.Entry{Actor: env.actor, Verb: "wish", Result: "info", Reason: title}
		url, ok, err := githubIssue(ctx, env, st, title, body, []string{wishLabel(st)})
		switch {
		case err != nil:
			entry.Result, entry.Detail = "red", err.Error()
		case !ok:
			entry.Detail = "kein Intake in box.yml: der Wunsch bleibt in " + f
		default:
			entry.Detail = url
		}
		record(env, st, entry)
	}
}

// cmdWish is the intake (#56): "Ich brauche einen Dienst, der …" becomes a
// GitHub issue with the intake label — never pi:ready, a person triages it
// first. Without an intake the wish is kept under <home>/wishes and the
// journal says that nobody reads it yet.
func cmdWish(ctx context.Context, env *cliEnv, args []string) int {
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		return fail(errors.New("usage: laib wish \"<was das Haus braucht>\""))
	}
	st, err := loadState(env)
	if err != nil {
		return fail(err)
	}
	first := text
	if i := strings.IndexAny(first, ".\n"); i > 0 {
		first = first[:i]
	}
	if len([]rune(first)) > 70 {
		first = string([]rune(first)[:70]) + "…"
	}
	title := "Wunsch: " + first
	body := text + "\n\n— aufgenommen über `laib wish` von " + env.actor + " auf " + st.Box.Domain + ", " + time.Now().Format("02.01.2006 15:04") + ".\n\nNächster Schritt: ein Mensch schneidet daraus ein Ticket mit Akzeptanzkriterien und setzt dann `pi:ready` oder baut es selbst (#56)."
	if proposal.Hand(env.actor) == "ops" {
		return wishProposal(ctx, env, st, title, body)
	}
	url, ok, err := githubIssue(ctx, env, st, title, body, []string{wishLabel(st)})
	if err != nil {
		record(env, st, journal.Entry{Actor: env.actor, Verb: "wish", Result: "red", Reason: first, Detail: err.Error()})
		return fail(err)
	}
	if !ok {
		dir := filepath.Join(env.paths.Home, "wishes")
		os.MkdirAll(dir, 0o755)
		p := filepath.Join(dir, time.Now().UTC().Format("2006-01-02T15-04-05")+".md")
		if err := os.WriteFile(p, []byte("# "+title+"\n\n"+body+"\n"), 0o644); err != nil {
			return fail(err)
		}
		record(env, st, journal.Entry{Actor: env.actor, Verb: "wish", Result: "info", Reason: first, Detail: "kein Intake in box.yml: der Wunsch liegt unter " + p + ", bis jemand ihn liest"})
		if env.json {
			return emit(env, map[string]string{"file": p, "issue": ""})
		}
		fmt.Fprintf(env.w(), "aufgeschrieben: %s (kein Intake konfiguriert — box.yml intake.repo)\n", p)
		return exitOK
	}
	record(env, st, journal.Entry{Actor: env.actor, Verb: "wish", Result: "info", Reason: first, Detail: url})
	if env.json {
		return emit(env, map[string]string{"issue": url})
	}
	fmt.Fprintln(env.w(), "aufgenommen:", url)
	return exitOK
}
