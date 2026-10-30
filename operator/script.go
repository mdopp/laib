package operator

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A skill's script (#839): a built-in skill's skills/<name>/run.py, the
// deterministic steps before the model — gathering, counting, comparing,
// rendering a note. The runner starts it in the Operator's container
// through skills/laibskill.py, with an empty environment, and is its only
// door to the box: each call it asks for goes through gate, in the skill's
// name, so the kernel checks it again (ADR 0023 §4). It gets no token, no
// address and none of the Operator's files; what it keeps between runs
// the runner keeps for it (OPERATOR_HOME/skills/<name>.json).

// scriptResult is what a script hands back when its part is done.
type scriptResult struct {
	Outcome string          `json:"outcome"` // set: the session ends here, no model (unchanged, idle, green)
	Why     string          `json:"why"`
	Note    string          `json:"note"`
	Context string          `json:"context"` // what the model is shown
	Human   string          `json:"human"`   // added to the session's note
	State   json.RawMessage `json:"state"`   // kept for the next run when the session does not fail
	Checks  []string        `json:"checks"`  // the tools whose model calls the script vets before they run

	// the note the runner writes when the model fails (#858): a report
	// from status and journal, so the slot is never silent
	Fallback string `json:"fallback"`
	// the session's trigger from here on: vorfall's first aid marks it
	// aided, with what it saw and did, for the model, the queue and an escalation (#792)
	Trigger *Trigger `json:"trigger"`
}

const (
	scriptTime  = 20 * time.Minute // a script's whole run: one MCP call may take 15 (sicherung's restic check, #858)
	scriptCalls = 200              // its calls; a loop that never ends is an error
)

// gate is one session's door for the calls its code makes: only the
// skill's granted tools, each within its limit, its note once — the model
// has its own, as vorfall's first aid notes before the model looks (#792).
// Every call and every refusal is recorded in the session.
type gate struct {
	sk    Skill
	mcp   *MCP
	s     *Session
	used  map[string]int // calls per tool, the model's and, apart, the script's (#376)
	noted bool           // the model's note, or the runner's for it
	// vet asks the script about a model's call of one of its checks: why
	// it may not run, or ""; end closes the script after the session
	vet func(tool string, args map[string]any) string
	end func()
}

func (g *gate) check(tool string, args map[string]any) string {
	if g.vet == nil {
		return ""
	}
	return g.vet(tool, args)
}

func (g *gate) call(ctx context.Context, tool string, args map[string]any) (string, bool, string) {
	why := ""
	verb := strings.TrimPrefix(tool, "laib_")
	switch n := g.sk.Limits[verb]; {
	case !strings.HasPrefix(tool, "laib_") || !slices.Contains(g.sk.Granted, verb):
		why = tool + " ist der Fähigkeit " + g.sk.Name + " nicht gewährt"
	case n > 0 && g.used["skript "+tool] >= n: // its own count, the model has its own
		why = fmt.Sprintf("höchstens %d Mal %s in dieser Sitzung", n, verb)
	case tool == "laib_note" && g.used["skript laib_note"] > 0:
		why = "die Notiz dieser Sitzung steht schon"
	}
	if why != "" {
		g.s.Refused = append(g.s.Refused, tool+": "+why)
		return "nicht ausgeführt: " + why, true, why
	}
	if args == nil {
		args = map[string]any{}
	}
	g.used["skript "+tool]++
	g.s.Script = append(g.s.Script, tool+" "+shownArgs(args))
	out, isErr, err := g.mcp.Call(ctx, tool, args)
	if err != nil {
		out, isErr = err.Error(), true
	}
	if m := proposalID.FindStringSubmatch(out); m != nil && !isErr && (tool == "laib_propose" || tool == "laib_repair") {
		n, _ := strconv.Atoi(m[1])
		g.s.Proposed = append(g.s.Proposed, n)
	}
	switch {
	case tool == "laib_note" && !isErr:
		g.s.Note, _ = args["text"].(string)
	case tool == "laib_escalate" && !isErr:
		g.s.Outcome = "escalated"
	}
	return out, isErr, ""
}

func (o *Operator) statePath(skill string) string {
	return filepath.Join(o.Home, "skills", skill+".json")
}

// script runs the skill's run.py to its result. Only a built-in skill's
// script runs: the kernel refuses a state repo's, and the image is the
// only place one comes from.
func (o *Operator) script(ctx context.Context, g *gate, tr Trigger, now time.Time, data map[string]any) (scriptResult, error) {
	var res scriptResult
	if g.sk.Source != "builtin" || g.sk.Run != "run.py" || o.Skills == "" {
		return res, errors.New("ein Skript läuft nur für eine eingebaute Fähigkeit, aus dem Image")
	}
	ctx, cancel := context.WithTimeout(ctx, scriptTime)
	dir := o.Skills
	cmd := exec.CommandContext(ctx, "python3", "-I", "-S", "-B", "-X", "utf8", filepath.Join(dir, "laibskill.py"), filepath.Join(dir, g.sk.Name, "run.py"))
	cmd.Env, cmd.Dir = []string{}, dir // nothing of the Operator's environment: no token, no address
	var errOut strings.Builder
	cmd.Stderr = &capped{b: &errOut}
	in, err := cmd.StdinPipe()
	out, err2 := cmd.StdoutPipe()
	if err = cmp.Or(err, err2, cmd.Start()); err != nil {
		cancel()
		return res, err
	}
	end := func() error {
		in.Close()
		defer cancel()
		return cmd.Wait()
	}
	failed := func(err error) (scriptResult, error) {
		end()
		return res, fmt.Errorf("Skript %s: %v %s", g.sk.Name, err, lastLine(errOut.String()))
	}
	state := []byte("null")
	if b, err := os.ReadFile(o.statePath(g.sk.Name)); err == nil && json.Valid(b) {
		state = b
	}
	enc, dec := json.NewEncoder(in), json.NewDecoder(bufio.NewReader(out))
	start := map[string]any{"skill": map[string]any{"name": g.sk.Name, "granted": g.sk.Granted, "limits": g.sk.Limits, "trigger": g.sk.Trigger, "output": g.sk.Output},
		"trigger": tr, "state": json.RawMessage(state), "now": now.Format(time.RFC3339), "data": data}
	if err := enc.Encode(start); err != nil {
		return failed(err)
	}
	for n := 0; ; n++ {
		var m struct {
			Call string
			Args map[string]any
			Ask  string // one short question to the model (#861)
			Done *scriptResult
		}
		if err := dec.Decode(&m); err != nil {
			return failed(err)
		}
		if m.Done != nil {
			res = *m.Done
			break
		}
		if n >= scriptCalls {
			return failed(fmt.Errorf("mehr als %d Aufrufe", scriptCalls))
		}
		if m.Ask != "" {
			text, isErr := o.ask(ctx, g, m.Ask)
			if err := enc.Encode(map[string]any{"text": text, "error": isErr}); err != nil {
				return failed(err)
			}
			continue
		}
		o.work.step("Skript ruft " + m.Call + " auf")
		text, isErr, refused := g.call(ctx, m.Call, m.Args)
		if err := enc.Encode(map[string]any{"text": text, "error": isErr, "refused": refused != ""}); err != nil {
			return failed(err)
		}
	}
	if len(res.Checks) > 0 { // it stays to vet the model's calls, until the session ends
		g.end = func() { end() }
		g.vet = func(tool string, args map[string]any) string {
			if !slices.Contains(res.Checks, tool) {
				return ""
			}
			var r struct{ Refuse string }
			if enc.Encode(map[string]any{"check": map[string]any{"tool": tool, "args": args}}) != nil || dec.Decode(&r) != nil {
				return "das Skript der Fähigkeit antwortet nicht"
			}
			return r.Refuse
		}
		return res, nil
	}
	if err := end(); err != nil {
		return res, fmt.Errorf("Skript %s: %v %s", g.sk.Name, err, lastLine(errOut.String()))
	}
	return res, nil
}

// ask is a script's short question to the model, without tools (#861):
// only a skill whose model judges asks, and the session counts its cost.
func (o *Operator) ask(ctx context.Context, g *gate, q string) (string, bool) {
	if g.sk.Modell != "urteil" {
		return "nur eine Fähigkeit mit modell: urteil fragt das Modell", true
	}
	o.work.step("Skript fragt das Modell")
	g.s.Script = append(g.s.Script, "frage "+clip(firstLine(q)))
	reply, err := o.chat(ctx, g.s, []Message{{Role: "user", Content: q}}, nil)
	if err != nil {
		return err.Error(), true
	}
	return reply.Content, false
}

// keep stores a script's state for its next run.
func (o *Operator) keep(skill string, state json.RawMessage) {
	if o.Home == "" || len(state) == 0 || string(state) == "null" {
		return
	}
	os.MkdirAll(filepath.Join(o.Home, "skills"), 0o700)
	p := o.statePath(skill)
	if os.WriteFile(p+".tmp", state, 0o600) == nil {
		os.Rename(p+".tmp", p)
	}
}

// capped keeps the first 8 KiB a script writes to stderr, for the error.
type capped struct{ b *strings.Builder }

func (c *capped) Write(p []byte) (int, error) {
	if room := 8<<10 - c.b.Len(); room > 0 {
		c.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
