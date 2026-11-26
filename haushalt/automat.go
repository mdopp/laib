package haushalt

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// automatTools are laib automat's actions, each one of the Automat's tools
// (templates/bote §5, homes ADR 0004 §3).
var automatTools = map[string]string{"entitaeten": "entitaeten", "tags": "tags", "automationen": "automationen",
	"lesen": "automation_lesen", "schreiben": "automation_schreiben", "loeschen": "automation_loeschen",
	"wiederherstellen": "automation_wiederherstellen", "einschalten": "automation_schalten",
	"ausschalten": "automation_schalten", "bestaetigen": "automation_bestaetigen"}

// cmdAutomat: `laib automat <aktion> [--id i] [--yaml y | stdin] [--domain d]
// [--suche s] [--code c]` (homes ADR 0004 §3): Home Assistant's automations
// for the Werkstatt, through the Bote's Automat, which keeps its HA token;
// the Werkstatt holds none. Reading answers at once; every change is drafted
// with a four-digit code that goes to box.yml notify.person through
// bote-notify and never into the answer, so the agent cannot know it
// (laib#1284); it happens only on bestaetigen with the code the person
// says. The journal names each confirmed change and who confirmed it.
func cmdAutomat(ctx context.Context, env *Env, args []string) int {
	usage := errors.New("usage: laib automat entitaeten [--domain d] [--suche s] | tags | automationen | lesen|loeschen|wiederherstellen|einschalten|ausschalten --id <id> | schreiben [--id <id>] --yaml <yaml> (or on stdin) | bestaetigen --code <vier Ziffern>")
	if len(args) == 0 || automatTools[args[0]] == "" {
		return env.fail(usage)
	}
	act := args[0]
	fs := flag.NewFlagSet("automat", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	a := map[string]any{}
	for _, k := range []string{"id", "yaml", "domain", "suche", "code"} {
		fs.Func(k, "", func(v string) error { a[k] = v; return nil })
	}
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return env.fail(usage)
	}
	if hand, _, _ := strings.Cut(env.Actor, ":"); !slices.Contains([]string{"claude", "pi", "human"}, hand) {
		return env.refused("laib automat ist für die Werkstatt (claude, pi) und Menschen, nicht für " + env.Actor + "; der Bote hat den Automaten selbst.")
	}
	if env.Bote.Automat == "" || env.Bote.AutomatToken == "" {
		return env.refused("Auf dieser Box ist kein Automat des Boten, der die Automationen von Home Assistant schreibt.")
	}
	if y, _ := a["yaml"].(string); act == "schreiben" {
		a["yaml"] = cmp.Or(y, env.Stdin)
	}
	if act == "einschalten" || act == "ausschalten" {
		a["an"] = act == "einschalten"
	}
	change := !slices.Contains([]string{"entitaeten", "tags", "automationen", "lesen", "bestaetigen"}, act)
	if change && (env.Person == "" || env.Bote.URL == "" || env.Bote.Token == "") {
		return env.refused("Nichts vorgemerkt: ich kann dir den Code nicht schicken, in box.yml notify.person steht niemand mit einem Kanal des Boten.")
	}
	text, isErr, err := env.Bote.automat(ctx, automatTools[act], a)
	switch {
	case err != nil:
		return env.fail(fmt.Errorf("der Automat antwortet nicht: %w", err))
	case isErr:
		return env.refused(text)
	case act == "bestaetigen":
		env.journal("bote", "", "automat: "+strings.SplitN(text, "\n", 2)[0])
	case change:
		return env.sendCode(ctx, text)
	}
	fmt.Fprintln(env.Out, text)
	return exitOK
}

// sendCode: a change's draft is {text, code} (automat.Config.Laib). The
// code goes to the person on their channel, never into the answer; if it
// cannot go, the draft is dropped (laib#1284).
func (e *Env) sendCode(ctx context.Context, draft string) int {
	var d struct{ Text, Code string }
	if json.Unmarshal([]byte(draft), &d) != nil || d.Code == "" {
		return e.refused("Nichts vorgemerkt: der Automat antwortet nicht in der Form, in der der Code bei dir statt hier ankommt.")
	}
	msg := fmt.Sprintf("Code %s für die Werkstatt: %s Nenn ihn dort, wenn es so sein soll; er gilt zehn Minuten.", d.Code, strings.SplitN(d.Text, "\n", 2)[0])
	kanal, status, err := e.Bote.send(ctx, e.Person, msg)
	if err != nil || status != http.StatusOK {
		e.Bote.automat(ctx, "automation_verwerfen", map[string]any{"code": d.Code})
		return e.refused(fmt.Sprintf("Nichts vorgemerkt: der Code ließ sich %s nicht schicken (Bote: %s).", e.Person, cmp.Or(http.StatusText(status), "antwortet nicht")))
	}
	e.journal("bote", "", "automat: Code an "+e.Person+" über "+kanal)
	fmt.Fprintf(e.Out, "%s\nIch habe dir den Code auf %s geschickt. Sag ihn mir, dann speichere ich.\n", d.Text, kanalName(kanal))
	return exitOK
}

// kanalName is bote-notify's channel as a person says it.
func kanalName(k string) string {
	return cmp.Or(map[string]string{"signal": "Signal", "telegram": "Telegram", "matrix": "Matrix", "discord": "Discord"}[k], k, "deinem Kanal")
}

// automat calls one of the Automat's MCP tools with its bearer and
// answers its text and whether it is an error.
func (b Bote) automat(ctx context.Context, tool string, args map[string]any) (string, bool, error) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	ctx, cancel := context.WithTimeout(ctx, time.Minute) // the Automat waits 20 s for HA
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(b.Automat, "/")+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.AutomatToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	var r struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || resp.StatusCode != http.StatusOK || len(r.Result.Content) == 0 {
		return "", false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return r.Result.Content[0].Text, r.Result.IsError, nil
}
