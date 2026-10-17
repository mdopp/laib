package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Trigger is why a session starts.
type Trigger struct {
	Kind    string `json:"kind"` // event | tick | report | manual
	Service string `json:"service,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Session is one run of a skill: trigger in, tool calls, a note out.
type Session struct {
	Started  time.Time `json:"started"`
	Skill    string    `json:"skill"`
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
	// Typed: a small model's window (#53) — compact results and context;
	// the menu itself is the kernel's typed schemas (#290).
	Typed bool
	// Window is the model's context in tokens (#285): from llama-server's
	// /props or OPERATOR_CONTEXT_WINDOW; 0 = unknown, the small budgets
	Window int
	Usage  *Usage // usage-metrics, for a skill with context usage (#270)
}

// Run executes one session of a skill for a trigger and records it. Every
// call of the session is made in the skill's name (X-Laib-Skill), so the
// kernel lists and allows only the skill's granted tools (ADR 0023); the
// menu here is narrowed to them as well.
func (o *Operator) Run(ctx context.Context, sk Skill, tr Trigger) (s Session, err error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	s = Session{Started: now().UTC(), Skill: sk.Name, Trigger: tr, Outcome: "error"}
	defer func() { s.Duration = time.Since(s.Started).Milliseconds(); o.record(s) }()

	mcp := *o.MCP
	mcp.Skill = sk.Name
	tools, err := mcp.Tools(ctx)
	if err != nil {
		return s, fmt.Errorf("tools: %w", err)
	}
	granted := map[string]bool{}
	for _, g := range sk.Granted {
		// a small model keeps the short menu: the skill's more tools are for a capable one (#286)
		if !o.Typed || !slices.Contains(sk.More, g) || slices.Contains(sk.Tools, g) {
			granted["laib_"+g] = true
		}
	}
	var mine []Tool
	for _, t := range tools {
		if granted[t.Name] {
			mine = append(mine, t)
		}
	}
	// the kernel's typed schemas (#290) are the menu: no second one here
	defs := Defs(mine)
	offered := map[string]bool{}
	for _, d := range defs {
		offered[d.Function.Name] = true
	}
	// the context: the box's bundle, or usage-metrics for a usage skill —
	// without it the skill says so once and asks no model
	var ctxText string
	var sentences []string
	if sk.Context == "usage" {
		err := errors.New("usage-metrics ist auf dieser Box nicht eingerichtet")
		if o.Usage != nil && o.Usage.URL != "" && o.Usage.Token != "" {
			ctxText, sentences, err = o.Usage.Read(ctx, 7)
		}
		if err != nil {
			s.Outcome, s.Note = "idle", "Nutzungsdaten nicht erreichbar: "+err.Error()+". Keine Vorschläge."
			mcp.Call(ctx, "laib_note", map[string]any{"kind": "watch", "text": s.Note})
			return s, nil
		}
	} else {
		ctxText, _, _ = o.MCP.Call(ctx, "laib_context", map[string]any{})
	}
	user := o.prompt(sk, tr, ctxText)
	system := SystemPrompt(defs)
	msgs := []Message{{Role: "system", Content: system}, {Role: "user", Content: user}}
	max := o.MaxCalls
	if max <= 0 {
		max = 12
	}
	textCalls := 0
	noted := false
	wished := 0
	for pass := 0; pass <= max; pass++ {
		reply, err := o.LLM.Chat(ctx, msgs, defs)
		if err != nil {
			return s, fmt.Errorf("model: %w", err)
		}
		msgs = append(msgs, reply)
		if len(reply.ToolCalls) == 0 && looksLikeToolCall(reply.Content, o.Typed, offered) {
			// a small model writing the call as text (#43): once asked again,
			// then red — never counted as work done
			textCalls++
			if textCalls == 1 {
				msgs = append(msgs, Message{Role: "user", Content: "Das war Text, kein Tool-Aufruf. Rufe das Werkzeug über die Tool-Schnittstelle auf (function call), nicht als Text."})
				continue
			}
			s.Outcome = "red"
			s.Note = "Sitzung rot: das Modell antwortete mit Text statt mit einem Tool-Aufruf (" + strings.TrimSpace(firstLine(reply.Content)) + "). Keine Änderung ausgeführt."
			mcp.Call(ctx, "laib_note", map[string]any{"kind": "incident", "text": s.Note})
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
				if sk.Output == "report" {
					kind = "report"
				} else if tr.Kind == "event" {
					kind = "incident"
				}
				mcp.Call(ctx, "laib_note", map[string]any{"kind": kind, "text": note})
				s.Note = note
			}
			if s.Outcome == "error" {
				s.Outcome = "green"
			}
			return s, nil
		}
		for _, tc := range reply.ToolCalls {
			mcpName := tc.Function.Name
			var args map[string]any
			if !offered[mcpName] {
				msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: mcpName, Content: "nicht ausgeführt: " + mcpName + " ist nicht im Menü der Fähigkeit " + sk.Name})
				continue
			}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil || args == nil {
				// invalid arguments never run (#43): laib_apply with an empty map
				// would act on the whole box; the kernel checks the rest (#290)
				msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: mcpName, Content: "ungültige Argumente: erwartet ein JSON-Objekt nach dem Schema des Werkzeugs; der Aufruf wurde nicht ausgeführt"})
				continue
			}
			shown := shownArgs(args)
			if mcpName == "laib_wish" {
				// a suggestion becomes a ticket: three a week at most, and never a
				// family member's sentence word for word (#270)
				why := ""
				if saw, ok := args["saw"].(string); ok && !strings.ContainsAny(saw, "0123456789") {
					why = "saw braucht Zahlen — was hast du gezählt?"
				} else if quotes(shown, sentences) {
					why = "der Vorschlag zitiert einen Satz der Familie wörtlich — nenne Thema und Anzahl, nicht den Satz"
				} else if wished >= 3 {
					why = "höchstens drei Vorschläge pro Woche — schreib jetzt die Notiz"
				}
				if why != "" {
					msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "nicht ausgeführt: " + why})
					continue
				}
				wished++
			}
			if len(s.Calls) >= max {
				msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "Limit an Tool-Aufrufen erreicht: schreibe jetzt die Notiz (note) oder eskaliere (escalate)."})
				continue
			}
			s.Calls = append(s.Calls, mcpName+" "+shown)
			out, isErr, err := mcp.Call(ctx, mcpName, args)
			if err != nil {
				out, isErr = err.Error(), true
			}
			_, limit := o.budgets()
			if len(out) > limit {
				out = out[:limit/2] + "\n…\n" + out[len(out)-limit/2:]
			}
			switch mcpName {
			case "laib_escalate":
				s.Outcome = "escalated"
			case "laib_note":
				s.Note = shown
				if t, ok := args["text"].(string); ok {
					s.Note = t
				}
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
	mcp.Call(ctx, "laib_note", map[string]any{"kind": "incident", "text": "Sitzung nach zu vielen Schritten abgebrochen; siehe Journal."})
	return s, nil
}

// SmallModel decides the interface (#286): OPERATOR_INTERFACE when set
// (typed = small, cli = capable), else the window — up to 32k tokens a
// small model with the short menus and compact budgets.
func SmallModel(iface string, window int) bool {
	switch iface {
	case "typed":
		return true
	case "cli":
		return false
	}
	return window <= 32768
}

// budgets are the context bundle's and each tool result's size in chars.
// Up to a 32k window they are what a small model was given (#53); above,
// they grow with the window — about a fifth of it for the context, a
// fifteenth per result, at roughly three chars a token (#285).
func (o *Operator) budgets() (ctx, result int) {
	ctx, result = 9000, 6000
	if o.Typed {
		ctx, result = 6000, 2000
	}
	if o.Window > 32768 {
		ctx, result = max(ctx, o.Window*3/5), max(result, o.Window/5)
	}
	return ctx, result
}

// shownArgs is a call's arguments for the session record: the command line
// when the model wrote one, else the JSON.
func shownArgs(a map[string]any) string {
	if s, ok := a["args"].(string); ok && len(a) == 1 {
		return s
	}
	b, _ := json.Marshal(a)
	return string(b)
}

// prompt is the user turn: the event (for an event skill), the skill's
// task and the box's context, budgeted for the model's window.
func (o *Operator) prompt(sk Skill, tr Trigger, ctx string) string {
	var b strings.Builder
	if tr.Kind == "event" {
		fmt.Fprintf(&b, "Auslöser: ein Ereignis. Dienst: %s. Detail: %s\n", tr.Service, tr.Detail)
	}
	label := "Kontext der Box:"
	if sk.Context == "usage" {
		label = "Nutzungsdaten:"
	}
	b.WriteString(strings.TrimSpace(sk.Prompt) + "\n\n" + label + "\n")
	max, _ := o.budgets()
	b.WriteString(budget(ctx, max))
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

// looksLikeToolCall: a reply that carries a JSON call, or starts a line
// with a tool's name — laib_restart or the bare restart — followed by an
// argument, instead of using the tool interface.
func looksLikeToolCall(text string, small bool, offered map[string]bool) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	if strings.Contains(t, "\"name\"") && strings.Contains(t, "laib_") {
		return true
	}
	for _, line := range strings.Split(t, "\n") {
		line = strings.TrimLeft(strings.TrimSpace(line), "`")
		if strings.HasPrefix(line, "laib_") {
			return true
		}
		if !small {
			continue // a capable model's answer that starts with a tool's name is an answer (#286)
		}
		for name := range offered {
			bare := strings.TrimPrefix(name, "laib_")
			if rest, ok := strings.CutPrefix(line, bare); ok && len(line) < 80 && (strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "(")) {
				return true
			}
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
	if look := pick("laib_status", "laib_diag", "laib_inspect", "laib_logs", "laib_read", "laib_look"); look != "" {
		b.WriteString("1. Erst schauen: " + look + ". Bilde eine Hypothese, bevor du etwas änderst.\n")
	}
	if fix := pick("laib_restart", "laib_exec", "laib_write", "laib_propose"); fix != "" {
		b.WriteString("2. Dann korrigieren, wenn es rückgängig geht: " + fix + ". Laib macht Snapshot, Journal und Rollback; du musst nichts davon selbst tun.\n   Miss jede Änderung an dem, was vorher funktioniert hat (Journal, Notizen, die bisherige Lösung): Sie muss leichter zu warten oder für die Familie angenehmer sein, und keins von beiden spürbar schlechter. Ein Vorschlag nennt, was vorher war und was besser wird.\n")
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
