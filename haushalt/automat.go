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
// the Werkstatt holds none. Reading answers at once; every change answers
// with the Automat's four-digit code and happens only on bestaetigen with
// it, which the person says in the conversation — the agent never confirms
// on its own. The journal names each confirmed change and who confirmed it.
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
	text, isErr, err := env.Bote.automat(ctx, automatTools[act], a)
	switch {
	case err != nil:
		return env.fail(fmt.Errorf("der Automat antwortet nicht: %w", err))
	case isErr:
		return env.refused(text)
	case act == "bestaetigen":
		env.journal("bote", "", "automat: "+strings.SplitN(text, "\n", 2)[0])
	case strings.HasPrefix(text, "Vorgemerkt"):
		text += "\nDen Code nennt die Person selbst im Gespräch; bestaetigen nie von dir aus."
	}
	fmt.Fprintln(env.Out, text)
	return exitOK
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
