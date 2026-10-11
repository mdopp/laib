package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Trigger is why a session starts.
type Trigger struct {
	Kind    string `json:"kind"` // event | tick | report | manual
	Service string `json:"service,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Session is one run of the agent: trigger in, tool calls, a note out.
type Session struct {
	Started  time.Time `json:"started"`
	Trigger  Trigger   `json:"trigger"`
	Calls    []string  `json:"calls"`
	Note     string    `json:"note"`
	Outcome  string    `json:"outcome"` // green | red | escalated | idle | error
	Duration int64     `json:"ms"`
}

// Operator holds the clients and the limits.
type Operator struct {
	MCP      *MCP
	LLM      *LLM
	Home     string // OPERATOR_HOME: incidents.jsonl lives here
	MaxCalls int    // tool calls per session
	Now      func() time.Time
}

// System is the persona and the rules. Short on purpose: the tools carry
// their own descriptions, and the envelope in Laib is the real guardrail.
const System = `Du bist der Operator einer Home-Box, die von Laib verwaltet wird. Du bist der Familien-Admin: du schaust zuerst, dann korrigierst du, dann schreibst du auf.

Regeln:
1. Erst schauen: laib_status, laib_diag, laib_inspect, laib_logs, laib_read, laib_look (cat, ls, grep, ps … im Container, kostet kein Budget). Bilde eine Hypothese, bevor du etwas änderst.
2. Dann korrigieren, wenn es rückgängig geht: laib_restart, laib_exec, laib_write, laib_propose (Werte, Patch). Laib macht Snapshot, Journal und Rollback; du musst nichts davon selbst tun.
3. Nie: Dienste löschen, Reboot, öffentliche Routen, Secrets, Tokens. Das sind Vorschläge für den Menschen, nicht deine Handlungen.
4. Wenn ein Tool "Budget erschöpft" sagt oder du nach zwei Versuchen nicht weiterkommst: laib_escalate mit allem, was du weißt.
5. Zum Schluss immer laib_note: --kind incident wenn du etwas repariert oder eskaliert hast, --kind watch wenn alles in Ordnung war, --kind report für den Morgenbericht. Deutsch, drei bis fünf Sätze, Fakten mit Uhrzeit, rot zuerst.

Antworte knapp. Keine Erklärungen an den Nutzer, nur Tool-Aufrufe und am Ende die Notiz.`

// Run executes one session for a trigger and records it.
func (o *Operator) Run(ctx context.Context, tr Trigger) (s Session, err error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	s = Session{Started: now().UTC(), Trigger: tr, Outcome: "error"}
	defer func() { s.Duration = time.Since(s.Started).Milliseconds(); o.record(s) }()

	tools, err := o.MCP.Tools(ctx)
	if err != nil {
		return s, fmt.Errorf("tools: %w", err)
	}
	defs := Defs(tools)
	ctxText, _, _ := o.MCP.Call(ctx, "laib_context", map[string]string{"args": ""})
	user := o.prompt(tr, ctxText)
	msgs := []Message{{Role: "system", Content: System}, {Role: "user", Content: user}}
	max := o.MaxCalls
	if max <= 0 {
		max = 12
	}
	for pass := 0; pass <= max; pass++ {
		reply, err := o.LLM.Chat(ctx, msgs, defs)
		if err != nil {
			return s, fmt.Errorf("model: %w", err)
		}
		msgs = append(msgs, reply)
		if len(reply.ToolCalls) == 0 {
			// The model stopped without a note: write one for it so the day
			// is never silent.
			if !strings.Contains(strings.Join(s.Calls, " "), "laib_note") {
				note := strings.TrimSpace(reply.Content)
				if note == "" {
					note = "Sitzung ohne Notiz beendet."
				}
				kind := "watch"
				if tr.Kind == "report" {
					kind = "report"
				} else if tr.Kind == "event" {
					kind = "incident"
				}
				o.MCP.Call(ctx, "laib_note", map[string]string{"args": "--kind " + kind + " \"" + strings.ReplaceAll(note, "\"", "'") + "\""})
				s.Note = note
			}
			if s.Outcome == "error" {
				s.Outcome = "green"
			}
			return s, nil
		}
		for _, tc := range reply.ToolCalls {
			var args map[string]string
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil || args == nil {
				args = map[string]string{}
			}
			if len(s.Calls) >= max {
				msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "Limit an Tool-Aufrufen erreicht: schreibe jetzt laib_note oder laib_escalate."})
				continue
			}
			s.Calls = append(s.Calls, tc.Function.Name+" "+args["args"])
			out, isErr, err := o.MCP.Call(ctx, tc.Function.Name, args)
			if err != nil {
				out, isErr = err.Error(), true
			}
			if len(out) > 6000 {
				out = out[:3000] + "\n…\n" + out[len(out)-3000:]
			}
			switch tc.Function.Name {
			case "laib_escalate":
				s.Outcome = "escalated"
			case "laib_note":
				s.Note = args["args"]
				if s.Outcome == "error" {
					s.Outcome = "green"
				}
			}
			if isErr && strings.Contains(out, "Budget") {
				s.Outcome = "red"
			}
			msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: out})
		}
	}
	s.Outcome = "red"
	o.MCP.Call(ctx, "laib_note", map[string]string{"args": "--kind incident \"Sitzung nach zu vielen Schritten abgebrochen; siehe Journal.\""})
	return s, nil
}

func (o *Operator) prompt(tr Trigger, ctx string) string {
	var b strings.Builder
	switch tr.Kind {
	case "event":
		fmt.Fprintf(&b, "Auslöser: ein Ereignis. Dienst: %s. Detail: %s\n", tr.Service, tr.Detail)
		b.WriteString("Schau dir den Dienst an, finde die Ursache, korrigiere wenn möglich, notiere.\n\n")
	case "report":
		b.WriteString("Auslöser: Morgenbericht. Lies laib_journal --since 24h und laib_status, fasse zusammen, was nachts passiert ist und was ansteht. Rot zuerst. Dann laib_note --kind report.\n\n")
	case "tick":
		b.WriteString("Auslöser: Routineblick. Prüfe laib_status und laib_diag. Wenn etwas rot oder gelb ist, kümmere dich darum; wenn alles grün ist, laib_note --kind watch in einem Satz.\n\n")
	default:
		fmt.Fprintf(&b, "Auslöser: %s. %s\n\n", tr.Kind, tr.Detail)
	}
	b.WriteString("Kontext der Box:\n")
	b.WriteString(ctx)
	return b.String()
}

func (o *Operator) record(s Session) {
	if o.Home == "" {
		return
	}
	os.MkdirAll(o.Home, 0o755)
	f, err := os.OpenFile(filepath.Join(o.Home, "incidents.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(s)
	f.Write(append(b, '\n'))
}
