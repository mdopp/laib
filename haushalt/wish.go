package haushalt

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitHub is the intake's way out (#56, #797): the API and this program's
// token, GITHUB_INTAKE_TOKEN in its unit, a fine-grained PAT with Issues
// write on box.yml intake.repo, nothing else. The kernel reads no such token.
type GitHub struct {
	API, Token string
}

// Intake is box.yml intake as the door hands it over for laib wish. Title,
// Body and Label set are the kernel's own ticket (an escalation, a wish the
// admin approved): filed as they are, journalled by the kernel.
type Intake struct {
	Repo   string `json:"repo"`
	Label  string `json:"label"`
	Domain string `json:"domain"`
	Title  string `json:"title,omitempty"`
	Body   string `json:"body,omitempty"`
}

// Wish: `laib wish "<was das Haus braucht>"` (#56, #797). "Ich brauche einen
// Dienst, der …" becomes a GitHub issue with the intake label, never
// pi:ready: a person triages it first. Without an intake the wish is kept
// in the journal, which says that nobody reads it yet. The Bote's wish is
// a Box-Aufgabe for the admins instead (#883). The Operator's wish waits
// for the admin as a proposal, in the kernel (ADR 0023 §6).
func Wish(ctx context.Context, env *Env, args []string) int {
	in := cmp.Or(env.Intake, &Intake{})
	if in.Title != "" {
		url, err := env.GitHub.issue(ctx, in.Repo, in.Title, in.Body, in.Label)
		if err != nil {
			return env.fail(err)
		}
		return env.emit(map[string]string{"issue": url})
	}
	if hand, _, _ := strings.Cut(env.Actor, ":"); hand == "bote" {
		return boteWish(ctx, env, args)
	}
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		return env.fail(errors.New(`usage: laib wish "<was das Haus braucht>"`))
	}
	first := headline(text)
	title := "Wunsch: " + first
	body := text + "\n\n— aufgenommen über `laib wish` von " + env.Actor + " auf " + in.Domain + ", " + time.Now().Format("02.01.2006 15:04") + ".\n\nNächster Schritt: ein Mensch schneidet daraus ein Ticket mit Akzeptanzkriterien und setzt dann `pi:ready` oder baut es selbst (#56)."
	if in.Repo == "" {
		env.journal("", first, "kein Intake in box.yml: der Wunsch steht hier, bis jemand ihn liest: "+text)
		if env.JSON {
			return env.emit(map[string]string{"issue": ""})
		}
		fmt.Fprintln(env.Out, "aufgeschrieben im Journal (kein Intake konfiguriert — box.yml intake.repo)")
		return exitOK
	}
	url, err := env.GitHub.issue(ctx, in.Repo, title, body, cmp.Or(in.Label, "wunsch"))
	if err != nil {
		env.journalAs("red", "", first, err.Error())
		return env.fail(err)
	}
	env.journal("", first, url)
	if env.JSON {
		return env.emit(map[string]string{"issue": url})
	}
	fmt.Fprintln(env.Out, "aufgenommen:", url)
	return exitOK
}

// boteWish: `laib wish [--von <login>] "<text>"` from the Bote (#883, owner
// 2026-10-28) is a Box-Aufgabe for the admins, „Wunsch aus dem Boten: …“,
// with who asked as its von, never a ticket in intake.repo: the family's
// words stay in the house and the owner decides what goes on to the
// Werkstatt, as with the Bote's escalation (#863). laib writes the task.
func boteWish(ctx context.Context, env *Env, args []string) int {
	by, who := env.Actor, "eine Person"
	if len(args) > 1 && args[0] == "--von" {
		by, who, args = env.Actor+":"+args[1], args[1], args[2:]
	}
	text := strings.Join(strings.Fields(strings.Join(args, " ")), " ")
	if text == "" {
		return env.fail(errors.New(`usage: laib wish [--von <login>] "<was das Haus braucht>"`))
	}
	first := headline(text)
	warum := text + " — " + who + " hat sich das über den Boten gewünscht, " + time.Now().Format("02.01.2006 15:04") + "; ob daraus Arbeit für die Werkstatt wird, entscheidet der Admin."
	id, err := env.Laib.Aufgabe(ctx, env.Ceiling, "add", "Wunsch aus dem Boten: "+first, "--warum", warum, "--fuer", "admins", "--von", by)
	if err != nil {
		env.journalAs("red", "", first, err.Error())
		return env.fail(err)
	}
	msg := fmt.Sprintf("Der Wunsch liegt beim Admin als Box-Aufgabe #%d; er entscheidet, was daraus wird.", id)
	env.journal("", first, fmt.Sprintf("Box-Aufgabe #%d für die Admins", id))
	if env.JSON {
		return env.emit(map[string]any{"aufgabe": id, "message": msg})
	}
	fmt.Fprintln(env.Out, msg)
	return exitOK
}

// headline is a wish's title: its first sentence, cut after 70 runes.
func headline(text string) string {
	if i := strings.IndexAny(text, ".\n"); i > 0 {
		text = text[:i]
	}
	if r := []rune(text); len(r) > 70 {
		return string(r[:70]) + "…"
	}
	return text
}

// issue files one issue in repo and returns its URL.
func (g GitHub) issue(ctx context.Context, repo, title, body, label string) (string, error) {
	if g.Token == "" {
		return "", fmt.Errorf("intake %s: laib-haushalt has no GITHUB_INTAKE_TOKEN (laib secret set GITHUB_INTAKE_TOKEN, then laib apply)", repo)
	}
	payload, _ := json.Marshal(map[string]any{"title": title, "body": body, "labels": []string{label}})
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cmp.Or(g.API, "https://api.github.com"), "/")+"/repos/"+repo+"/issues", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("intake %s: %w", repo, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("intake %s: HTTP %d: %.200s", repo, resp.StatusCode, raw)
	}
	var out struct {
		URL string `json:"html_url"`
	}
	json.Unmarshal(raw, &out)
	return out.URL, nil
}
