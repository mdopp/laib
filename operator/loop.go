package operator

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
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
	Outcome  string    `json:"outcome"` // green | red | escalated | idle | error | unchanged | skipped
	Duration int64     `json:"ms"`
	Proposed []int     `json:"proposed,omitempty"` // the proposals this session filed (#544)
	Red      []string  `json:"red,omitempty"`      // what was red: the event's detail, a look's red findings (#550)
	Why      string    `json:"why,omitempty"`      // why it ended red, or was skipped (#550)
}

// Line is the session's log line. A red or an incident session's names
// what was red, why it ended red and what came of it (#550): seven red
// looks in 40 h said only "red".
func (s Session) Line() string {
	l := fmt.Sprintf("session %s/%s: %s, %d calls, %dms", s.Skill, s.Trigger.Service, s.Outcome, len(s.Calls), s.Duration)
	if s.Outcome == "skipped" {
		return l + " — " + s.Why
	}
	if s.Outcome != "red" && s.Outcome != "escalated" && s.Trigger.Kind != "event" {
		return l
	}
	what := cmp.Or(strings.Join(s.Red, "; "), "kein roter Befund genannt")
	if s.Why != "" {
		what += " (" + s.Why + ")"
	}
	var came []string
	for _, n := range s.Proposed {
		came = append(came, fmt.Sprintf("#%d", n))
	}
	result := "nichts geändert"
	switch {
	case len(came) > 0:
		result = "vorgeschlagen " + strings.Join(came, ", ")
	case countPrefix(s.Calls, "laib_restart") > 0:
		result = "neu gestartet"
	}
	if s.Outcome == "escalated" {
		result += ", eskaliert"
	}
	return l + " — " + what + " → " + result
}

// countPrefix is how many calls start with prefix.
func countPrefix(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
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
	Web    *Web   // Frage's search, for a session that only proposes (#544)
	// Looked is the unix time of the last session or look that ended
	// without an error; /ready says it, the kernel's heartbeat reads it,
	// since a look that found nothing new writes no note (#543)
	Looked  atomic.Int64
	failing bool // the last session ended in an error: no look is skipped
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
	if tr.Kind == "event" {
		s.Red = []string{tr.Detail}
	}
	mem, saw := o.memory(), []string(nil) // saw: the findings this session read (#543)
	defer func() {
		s.Duration = time.Since(s.Started).Milliseconds()
		if o.failing = s.Outcome == "error"; !o.failing {
			o.Looked.Store(now().Unix())
		}
		if saw != nil && !o.failing && s.Outcome != "unchanged" && s.Outcome != "skipped" {
			mem.Seen[sk.Name] = saw
			o.remember(mem)
		}
		if s.Outcome != "unchanged" {
			o.record(s)
		}
	}()

	mcp := *o.MCP
	mcp.Skill = sk.Name
	// a look skill (#543) — an interval skill that reads diag — asks no
	// model and writes no note while diag says what its last session saw;
	// a finding diag offers a repair for becomes a proposal first
	human, look, found := "", sk.Trigger.Every != "" && slices.Contains(sk.Granted, "diag"), ""
	if look {
		text, isErr, err := mcp.Call(ctx, "laib_diag", map[string]any{})
		if fs, raw, ok := parseDiag(text); ok && err == nil && !isErr {
			last, had := mem.Seen[sk.Name]
			if tr.Kind == "tick" && had && !o.failing && slices.Equal(ids(fs), last) {
				s.Outcome = "unchanged"
				return s, nil
			}
			found, saw = news(fs, raw, mem.Seen[sk.Name]), ids(fs)
			for _, f := range fs {
				if f.Severity == "rot" {
					s.Red = append(s.Red, strings.TrimSpace(f.Probe+" "+f.Service+" "+f.Detail))
				}
			}
			if slices.Contains(sk.Granted, "repair") {
				human = o.repairs(ctx, &mcp, fs, mem)
			}
		}
	}
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
	// the web (#544, ADR 0025): only in a session that proposes and cannot act
	var web *webSession
	if o.offersWeb(sk) {
		defs, web = append(defs, webDef()), &webSession{web: o.Web}
	}
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
		if web != nil {
			web.upstream = upstreamOf(ctxText)
		}
		if look {
			ctxText = lookBundle(found, ctxText)
		}
	}
	// the record (#544): what came of its last proposals, for every session;
	// the morning report also carries the month's rate
	all := o.history(ctx)
	ctxText = Record(all) + ctxText
	if sk.Output == "report" {
		human = strings.TrimSpace(human + " " + Rate(all, now()) + " " + Stale(all))
	}
	// a lessons skill (#544) asks no model: the runner files the lessons
	if sk.Lessons == "vorfall" {
		s.Outcome, s.Note = "idle", cmp.Or(o.lessons(ctx, &mcp, all), "Keine neue Lehre: kein Vorfall, dessen Befund seitdem weg ist.")
		mcp.Call(ctx, "laib_note", map[string]any{"kind": "watch", "text": s.Note})
		return s, nil
	}
	// an updates skill (#376): the runner reads the updates, the model sees
	// and may propose only the patch values, the rest goes into the note
	offers := map[string]string{}
	if sk.Updates == "patch" {
		var list, h string
		list, h, offers = o.patches(ctx, &mcp)
		human = strings.TrimSpace(human + " " + h)
		if len(offers) == 0 {
			s.Outcome, s.Note = "idle", strings.TrimSpace("Keine Patch-Versionen zum Vorschlagen. "+human)
			mcp.Call(ctx, "laib_note", map[string]any{"kind": "watch", "text": s.Note})
			return s, nil
		}
		ctxText = list + "\n" + ctxText
	}
	// GPU courtesy (#550): no session into another's window or a cold start;
	// skipped is no error, the next step tries again
	if why := o.LLM.Busy(ctx); why != "" {
		s.Outcome, s.Why = "skipped", why
		return s, nil
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
	used := map[string]int{} // calls per tool, for the skill's limits (#376)
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
			s.Outcome, s.Why = "red", "Text statt Tool-Aufruf"
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
				note = strings.TrimSpace(note + " " + human + " " + web.tell())
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
				// a suggestion becomes a ticket: with numbers, and never a family
				// member's sentence word for word (#270); the cap is the skill's limits
				why := ""
				if saw, ok := args["saw"].(string); ok && !strings.ContainsAny(saw, "0123456789") {
					why = "saw braucht Zahlen — was hast du gezählt?"
				} else if quotes(shown, sentences) {
					why = "der Vorschlag zitiert einen Satz der Familie wörtlich — nenne Thema und Anzahl, nicht den Satz"
				}
				if why != "" {
					msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "nicht ausgeführt: " + why})
					continue
				}
			}
			if why := unanchored(sk, mcpName, args); why != "" {
				msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "nicht ausgeführt: " + why})
				continue
			}
			if mcpName == "laib_propose" && sk.Updates == "patch" {
				service, _ := args["service"].(string)
				key, _ := args["key"].(string)
				if v, _ := args["value"].(string); v == "" || offers[service+" "+key] != v {
					msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "nicht ausgeführt: vorschlagen kannst du nur die Patch-Versionen oben, service, key und value genau so"})
					continue
				}
			}
			if mcpName == "laib_note" && noted {
				// a session's note is written once (#550): a model repeating it
				// put the same note five times into the journal within 14 s
				msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "nicht ausgeführt: die Notiz dieser Sitzung steht schon — beende die Sitzung"})
				continue
			}
			if n := sk.Limits[strings.TrimPrefix(mcpName, "laib_")]; n > 0 && used[mcpName] >= n {
				msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: fmt.Sprintf("nicht ausgeführt: höchstens %d Mal %s in dieser Sitzung — schreib jetzt die Notiz", n, strings.TrimPrefix(mcpName, "laib_"))})
				continue
			}
			if t, ok := args["text"].(string); mcpName == "laib_note" && ok {
				if extra := strings.TrimSpace(human + " " + web.tell()); extra != "" {
					args["text"], human = strings.TrimSpace(t+" "+extra), ""
					shown = shownArgs(args)
				}
			}
			if l, ok := args["sources"].([]any); ok && len(l) == 0 {
				delete(args, "sources") // the kernel takes a list with something in it
			}
			if src := web.sources(args["sources"]); mcpName == "laib_propose" && src != nil {
				args["sources"] = src // what it rests on is named, and a human decides (#544)
				shown = shownArgs(args)
			}
			if len(s.Calls) >= max {
				msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: "Limit an Tool-Aufrufen erreicht: schreibe jetzt die Notiz (note) oder eskaliere (escalate)."})
				continue
			}
			used[mcpName]++
			s.Calls = append(s.Calls, mcpName+" "+shown)
			var out string
			var isErr bool
			if mcpName == "websuche" {
				q, _ := args["frage"].(string)
				svc, _ := args["dienst"].(string)
				out = web.search(ctx, q, svc)
			} else if out, isErr, err = mcp.Call(ctx, mcpName, args); err != nil {
				out, isErr = err.Error(), true
			}
			if m := proposalID.FindStringSubmatch(out); m != nil && !isErr && (mcpName == "laib_propose" || mcpName == "laib_repair") {
				n, _ := strconv.Atoi(m[1])
				s.Proposed = append(s.Proposed, n)
			}
			if fs, raw, ok := parseDiag(out); mcpName == "laib_diag" && !isErr && ok {
				out = news(fs, raw, mem.Seen[sk.Name])
				saw = ids(fs, saw...)
			}
			_, limit := o.budgets()
			if look {
				limit = min(limit, lookResult)
			}
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
				s.Outcome, s.Why = "red", mcpName+": "+firstLine(out)
			}
			msgs = append(msgs, Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: out})
		}
	}
	s.Outcome, s.Why = "red", fmt.Sprintf("abgebrochen nach %d Aufrufen", len(s.Calls))
	if !noted { // the session's one note (#550): a note written stands, the log line says the rest
		mcp.Call(ctx, "laib_note", map[string]any{"kind": "incident", "text": "Sitzung nach zu vielen Schritten abgebrochen; siehe Journal."})
	}
	return s, nil
}

// unanchored says why a model's proposal may not be filed (#544): in a
// session that reads diag, a proposal names the finding it answers, what
// was seen and how it is undone, so laib can measure it afterwards. The
// updates skill's patch versions answer an update, not a finding.
func unanchored(sk Skill, tool string, args map[string]any) string {
	if tool != "laib_propose" || sk.Updates == "patch" || !slices.Contains(sk.Granted, "diag") {
		return ""
	}
	for _, k := range []string{"finding", "evidence", "undo"} {
		if v, _ := args[k].(string); strings.TrimSpace(v) == "" {
			return "ein Vorschlag nennt finding (probe und Dienst eines Befunds aus laib diag), evidence (was du gesehen hast) und undo (wie man ihn zurücknimmt); laib misst danach, ob der Befund weg ist. Gibt diag keinen Befund dafür her: escalate"
		}
	}
	return ""
}

// patches reads laib updates for an updates skill (#376): the list the
// model is shown, the note's lines for a human (minor versions, registry
// errors) and the offers, "service key" → the one value it may propose.
func (o *Operator) patches(ctx context.Context, mcp *MCP) (list, human string, offers map[string]string) {
	offers = map[string]string{}
	out, isErr, err := mcp.Call(ctx, "laib_updates", map[string]any{})
	var ups []struct {
		Service, Key, Image, Current, Patch, Minor, Error, Digest string
		Auto                                                      bool
	}
	if err != nil || isErr || json.Unmarshal([]byte(out), &ups) != nil {
		return "", "Aktualisierungen nicht lesbar: " + firstLine(cmp.Or(out, fmt.Sprint(err))) + ".", offers
	}
	var b, h strings.Builder
	b.WriteString("Patch-Versionen, die du vorschlagen kannst:\n")
	// the runner's own proposals (#528), each once while it is open: a new
	// image of a service with data (pinned by its digest; the review asks a
	// person and laib snapshots first), and what a service's status offers
	// as one value (Home Assistant's extensions); firmware is only reported
	var open []struct{ Reason string }
	if text, _, err := mcp.Call(ctx, "laib_inbox", map[string]any{}); err == nil {
		json.Unmarshal([]byte(text), &open)
	}
	propose := func(what string, args map[string]any) {
		if slices.ContainsFunc(open, func(p struct{ Reason string }) bool { return p.Reason == args["reason"] }) {
			fmt.Fprintf(&h, " %s: der Vorschlag ist noch offen.", what)
		} else if out, isErr, err := mcp.Call(ctx, "laib_propose", args); err != nil || isErr {
			fmt.Fprintf(&h, " %s ließ sich nicht vorschlagen: %s.", what, firstLine(cmp.Or(out, fmt.Sprint(err))))
		} else {
			fmt.Fprintf(&h, " %s vorgeschlagen, für einen Menschen.", what)
		}
	}
	for _, u := range ups {
		if u.Digest != "" && !u.Auto {
			what := fmt.Sprintf("%s: neues Image %s:%s", u.Service, u.Image, u.Current)
			propose(what, map[string]any{"reason": what + " (" + u.Digest + ")", "service": u.Service, "key": u.Key, "value": u.Image + ":" + u.Current + "@" + u.Digest})
		}
	}
	diagText, _, _ := mcp.Call(ctx, "laib_diag", map[string]any{})
	var findings []struct{ Probe, Service, Detail, Fix string }
	json.Unmarshal([]byte(diagText), &findings)
	for _, f := range findings {
		if m := offerFix.FindStringSubmatch(f.Fix); f.Probe == "status" && m != nil && m[1] == f.Service {
			propose(f.Service+": "+f.Detail, map[string]any{"reason": f.Service + ": " + f.Detail, "service": m[1], "key": m[2], "value": m[3]})
		} else if f.Probe == "status" && strings.HasPrefix(f.Detail, "Firmware: ") {
			fmt.Fprintf(&h, " %s: %s.", f.Service, f.Detail)
		}
	}
	for _, u := range ups {
		if u.Service == "laib" {
			continue // Laib's own update is self-update.sh's (#547), no proposal
		}
		if u.Service == "host" && u.Patch != "" {
			// the host image (#368): the runner proposes it itself, a human approves
			patch := fmt.Sprintf("--- a/host/image\n+++ b/host/image\n@@ -1,2 +1,2 @@\n # tag: %s\n-%s@%s\n+%s@%s\n", u.Key, u.Image, u.Current, u.Image, u.Patch)
			out, isErr, err := mcp.Call(ctx, "laib_propose", map[string]any{"reason": "Host-Image " + u.Key + ": " + u.Current + " → " + u.Patch, "patch": patch})
			if err != nil || isErr {
				fmt.Fprintf(&h, " Host-Image %s → %s ließ sich nicht vorschlagen: %s.", u.Key, u.Patch, firstLine(cmp.Or(out, fmt.Sprint(err))))
			} else {
				fmt.Fprintf(&h, " Host-Image %s → %s vorgeschlagen, für einen Menschen.", u.Key, u.Patch)
			}
			continue
		}
		if u.Patch != "" {
			offers[u.Service+" "+u.Key] = u.Image + ":" + u.Patch
			fmt.Fprintf(&b, "- service %s, key %s, value %s:%s (jetzt %s)\n", u.Service, u.Key, u.Image, u.Patch, u.Current)
		}
		if u.Minor != "" {
			fmt.Fprintf(&h, " %s: Minor-Version %s (jetzt %s) für einen Menschen.", u.Service, u.Minor, u.Current)
		}
		if u.Error != "" {
			fmt.Fprintf(&h, " %s: Registry nicht lesbar (%s).", u.Service, u.Error)
		}
	}
	return b.String(), strings.TrimSpace(h.String()), offers
}

// offerFix is a status finding's fix that is one value proposal (#528), the
// runner files it as it stands: Home Assistant's check names the extensions
// to install this way (templates/home-assistant/home-assistant-check.py).
var offerFix = regexp.MustCompile(`^laib propose --set services/([a-z0-9-]+)/values\.yml:([A-Z][A-Z0-9_]*)=(\S+)$`)

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

// A look's own budget (#550), whatever the model's window: a look that ran
// read about 28,000 tokens, 4.0 million a day on the Kraftwerk, because the
// context grew with the 131k window, and it evicted the Bote's prompt
// cache. Its context is diag and the last journal lines; chars, at roughly
// three a token.
const lookCtx, lookResult, lookLines = 9000, 4000, 15

// lookBundle is a look's context: what diag found, as the model would see
// it, and the journal's last entries without the Operator's own notes —
// the lease, a backup, a converge are what is news there.
func lookBundle(found, bundle string) string {
	var c struct {
		Journal []struct {
			T                                            time.Time
			Actor, Verb, Service, Result, Reason, Detail string
		}
	}
	json.Unmarshal([]byte(bundle), &c)
	var lines []string
	for i := len(c.Journal) - 1; i >= 0 && len(lines) < lookLines; i-- {
		e := c.Journal[i]
		if e.Actor == "ops" && e.Verb == "note" {
			continue
		}
		l := strings.Join(strings.Fields(strings.Join([]string{e.T.Format("15:04"), e.Actor, e.Verb, e.Service, e.Result, e.Reason, e.Detail}, " ")), " ")
		if r := []rune(l); len(r) > 200 {
			l = string(r[:200]) + "…"
		}
		lines = append(lines, "- "+l)
	}
	slices.Reverse(lines)
	text := "Befunde (laib diag):\n" + budget(cmp.Or(found, "nicht lesbar"), lookCtx/2) + "\n\nJournal, die letzten Einträge ohne die Notizen des Operators:\n" + strings.Join(lines, "\n")
	return budget(text, lookCtx)
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
