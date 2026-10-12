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
	// Typed: the task-shaped surface for small models (#53) — a menu per
	// trigger, typed parameters, CLI arguments built in code, compact
	// results. Services is the enum the menu offers (installed services).
	Typed    bool
	Services []string
}

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
	has := map[string]bool{}
	for _, d := range defs {
		has[d.Function.Name] = true
	}
	if o.Typed {
		defs = TypedDefs(tr, o.Services, has)
	}
	ctxText, _, _ := o.MCP.Call(ctx, "laib_context", map[string]string{"args": ""})
	user := o.prompt(tr, ctxText)
	system := SystemPrompt(defs)
	if o.Typed {
		system = TypedSystemPrompt(defs)
		user = o.typedPrompt(tr, ctxText)
	}
	msgs := []Message{{Role: "system", Content: system}, {Role: "user", Content: user}}
	max := o.MaxCalls
	if max <= 0 {
		max = 12
	}
	textCalls := 0
	noted := false
	for pass := 0; pass <= max; pass++ {
		reply, err := o.LLM.Chat(ctx, msgs, defs)
		if err != nil {
			return s, fmt.Errorf("model: %w", err)
		}
		msgs = append(msgs, reply)
		if len(reply.ToolCalls) == 0 && (looksLikeToolCall(reply.Content) || (o.Typed && looksLikeTypedCall(reply.Content))) {
			// a small model writing the call as text (#43): once asked again,
			// then red — never counted as work done
			textCalls++
			if textCalls == 1 {
				msgs = append(msgs, Message{Role: "user", Content: "Das war Text, kein Tool-Aufruf. Rufe das Werkzeug über die Tool-Schnittstelle auf (function call), nicht als Text."})
				continue
			}
			s.Outcome = "red"
			s.Note = "Sitzung rot: das Modell antwortete mit Text statt mit einem Tool-Aufruf (" + strings.TrimSpace(firstLine(reply.Content)) + "). Keine Änderung ausgeführt."
			o.MCP.Call(ctx, "laib_note", map[string]string{"args": "--kind incident \"" + strings.ReplaceAll(s.Note, "\"", "'") + "\""})
			return s, nil
		}
		if len(reply.ToolCalls) == 0 {
			// The model stopped without a note: write one for it so the day
			// is never silent.
			if !noted {
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
			mcpName := tc.Function.Name
			var args map[string]string
			if o.Typed {
				// typed calls are validated and turned into CLI arguments here;
				// anything missing, out of range or outside the menu never runs
				name, a, err := Translate(tc.Function.Name, json.RawMessage(tc.Function.Arguments), o.Services)
				if err != nil {
					msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "nicht ausgeführt: " + err.Error()})
					continue
				}
				mcpName, args = name, a
			} else if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil || args == nil {
				// invalid arguments never run (#43, #53): laib_apply with an empty
				// map would act on the whole box
				msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "ungültige Argumente: erwartet ein JSON-Objekt {\"args\": \"…\"}; der Aufruf wurde nicht ausgeführt"})
				continue
			}
			if len(s.Calls) >= max {
				msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "Limit an Tool-Aufrufen erreicht: schreibe jetzt die Notiz (note) oder eskaliere (escalate)."})
				continue
			}
			s.Calls = append(s.Calls, tc.Function.Name+" "+args["args"])
			out, isErr, err := o.MCP.Call(ctx, mcpName, args)
			if err != nil {
				out, isErr = err.Error(), true
			}
			limit := 6000
			if o.Typed {
				limit = 2000 // compact results for a 32k window (#53)
			}
			if len(out) > limit {
				out = out[:limit/2] + "\n…\n" + out[len(out)-limit/2:]
			}
			switch mcpName {
			case "laib_escalate":
				s.Outcome = "escalated"
			case "laib_note":
				s.Note = args["args"]
				noted = true
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

// typedPrompt is the user turn for the typed surface: the same triggers,
// the menu's names instead of CLI verbs.
func (o *Operator) typedPrompt(tr Trigger, ctx string) string {
	var b strings.Builder
	switch tr.Kind {
	case "event":
		fmt.Fprintf(&b, "Auslöser: ein Ereignis. Dienst: %s. Detail: %s\n", tr.Service, tr.Detail)
		b.WriteString("Schau dir den Dienst an (diagnose, logs, inspect), finde die Ursache, korrigiere wenn es rückgängig geht (restart, set_value), sonst escalate. Zum Schluss note.\n\n")
	case "report":
		b.WriteString("Auslöser: Morgenbericht. Lies journal (24 Stunden) und status, fasse zusammen, was nachts passiert ist und was ansteht. Rot zuerst. Dann note mit kind report.\n\n")
	case "tick":
		b.WriteString("Auslöser: Routineblick. Prüfe status und diagnose. Wenn alles grün ist, note mit kind watch in einem Satz; sonst beschreibe in der Notiz, was gelb oder rot ist.\n\n")
	default:
		fmt.Fprintf(&b, "Auslöser: %s. %s\n\n", tr.Kind, tr.Detail)
	}
	b.WriteString("Kontext der Box:\n")
	b.WriteString(budget(ctx, 6000))
	return b.String()
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
	b.WriteString(budget(ctx, 9000))
	return b.String()
}

// budget keeps a context bundle inside a small model's window (#43): the
// head (state, services) and the tail (the newest journal lines) survive,
// the middle is cut.
func budget(ctx string, max int) string {
	if len(ctx) <= max {
		return ctx
	}
	head, tail := max/4, max-max/4
	cut := ctx[:head]
	if i := strings.LastIndex(cut, "\n"); i > 0 {
		cut = cut[:i]
	}
	rest := ctx[len(ctx)-tail:]
	if i := strings.Index(rest, "\n"); i >= 0 {
		rest = rest[i+1:]
	}
	return cut + "\n…(gekürzt)…\n" + rest
}

// looksLikeToolCall: a reply that names a laib_ tool or carries a JSON
// call instead of using the tool interface.
func looksLikeToolCall(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	if strings.Contains(t, "\"name\"") && strings.Contains(t, "laib_") {
		return true
	}
	for _, line := range strings.Split(t, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "laib_") || strings.HasPrefix(line, "`laib_") {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// SystemPrompt is the persona and the rules, with the tools the hand
// really has (#43: the fixed text named laib_diag, which MCP lacked).
func SystemPrompt(defs []ToolDef) string {
	has := map[string]bool{}
	var names []string
	for _, d := range defs {
		has[d.Function.Name] = true
		names = append(names, d.Function.Name)
	}
	pick := func(cands ...string) string {
		var out []string
		for _, c := range cands {
			if has[c] {
				out = append(out, c)
			}
		}
		return strings.Join(out, ", ")
	}
	var b strings.Builder
	b.WriteString("Du bist der Operator einer Home-Box, die von Laib verwaltet wird. Du bist der Familien-Admin: du schaust zuerst, dann korrigierst du, dann schreibst du auf.\n\nDeine Werkzeuge: " + strings.Join(names, ", ") + ". Nur diese; rufe sie über die Tool-Schnittstelle auf, nie als Text.\n\nRegeln:\n")
	if look := pick("laib_status", "laib_diag", "laib_inspect", "laib_logs", "laib_read", "laib_exec"); look != "" {
		b.WriteString("1. Erst schauen: " + look + ". Bilde eine Hypothese, bevor du etwas änderst.\n")
	}
	if fix := pick("laib_restart", "laib_exec", "laib_write", "laib_propose"); fix != "" {
		b.WriteString("2. Dann korrigieren, wenn es rückgängig geht: " + fix + ". Laib macht Snapshot, Journal und Rollback; du musst nichts davon selbst tun.\n")
	}
	b.WriteString("3. Nie: Dienste löschen, Reboot, öffentliche Routen, Secrets, Tokens. Das sind Vorschläge für den Menschen, nicht deine Handlungen.\n")
	if has["laib_escalate"] {
		b.WriteString("4. Wenn ein Tool \"Budget erschöpft\" sagt oder du nach zwei Versuchen nicht weiterkommst: laib_escalate mit allem, was du weißt.\n")
	}
	if has["laib_note"] {
		b.WriteString("5. Zum Schluss immer laib_note: --kind incident wenn du etwas repariert oder eskaliert hast, --kind watch wenn alles in Ordnung war, --kind report für den Morgenbericht. Deutsch, drei bis fünf Sätze, Fakten mit Uhrzeit, rot zuerst.\n")
	}
	b.WriteString("\nAntworte knapp. Keine Erklärungen an den Nutzer, nur Tool-Aufrufe und am Ende die Notiz.")
	return b.String()
}

// TypedSystemPrompt names the menu and the rules in the menu's words.
func TypedSystemPrompt(defs []ToolDef) string {
	var names []string
	has := map[string]bool{}
	for _, d := range defs {
		names = append(names, d.Function.Name)
		has[d.Function.Name] = true
	}
	var b strings.Builder
	b.WriteString("Du bist der Operator einer Home-Box, die von Laib verwaltet wird: der Familien-Admin. Du schaust zuerst, dann korrigierst du, dann schreibst du auf.\n\nDeine Werkzeuge: " + strings.Join(names, ", ") + ". Nur diese, nur über die Tool-Schnittstelle, mit genau den Parametern, die sie nennen.\n\nRegeln:\n1. Erst schauen, dann eine Hypothese, dann handeln.\n")
	if has["restart"] || has["set_value"] {
		b.WriteString("2. Korrigieren nur, wenn es rückgängig geht; Laib macht Snapshot, Journal und Rollback.\n")
	}
	b.WriteString("3. Löschen, Reboot, öffentliche Routen, Secrets, Tokens: nie — das entscheidet ein Mensch.\n")
	if has["escalate"] {
		b.WriteString("4. Wenn ein Werkzeug \"Budget erschöpft\" sagt oder du nach zwei Versuchen nicht weiterkommst: escalate mit allem, was du weißt.\n")
	}
	b.WriteString("5. Zum Schluss immer note. Deutsch, drei bis fünf Sätze, Fakten mit Uhrzeit, rot zuerst.\n\nAntworte knapp: Tool-Aufrufe, am Ende die Notiz.")
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
