// Package relay is the deterministic approval path (#48, #184): code that
// sees the sender decides, never the model. It listens to signal-cli's
// daemon (the event stream Hermes listens to as well; the daemon serves
// every subscriber) and reads the admin's one Matrix room, reacts only to
// the admin's number or Matrix ID and only to "gib #N frei" / "lehne #N
// ab", shows the proposal's consequence back, and calls Laib with a token
// that can do nothing but decide. On Discord it reads the owner's DM with
// the bot itself (#463). Telegram never decides (ADR 0016 §3). On Signal a
// vote on bote-notify's poll or a 👍/👎 on it decides the same way (#509).
package relay

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config: the admin's identities, Laib's MCP and the approver token.
type Config struct {
	Admin     string // E.164; on Signal only this number decides (empty: no Signal approvals)
	AdminMXID string // on Matrix only this ID decides, in Room (empty: no Matrix approvals)
	Room      string // the admin's one Matrix room, !id:server
	// AdminDiscord decides on Discord only in DiscordChannel, the DM with the bot (#463)
	AdminDiscord, DiscordChannel string
	LaibURL                      string // http://host.containers.internal:5900/mcp
	Token                        string // the approver hand's token
	SignalURL                    string // http://127.0.0.1:8080 — signal-cli daemon --http
	Account                      string // the Bote's own number
	Polls                        string // bote-notify's umfragen.json: poll timestamp → proposal (#509)
}

// Message is one incoming message. Platform is "signal", "matrix" or
// "discord" (#463); any other, or none, never decides.
type Message struct {
	Platform, From, Room, Text string
	// a vote on a poll or a reaction (#509): the message it targets, whose
	// it is, and "frei", "ab" or "später"; Target 0 is a plain message
	Target               int64
	TargetAuthor, Choice string
}

// replyTo: a Matrix answer goes to the room, a Signal one to the number.
func (m Message) replyTo() string {
	switch m.Platform {
	case "matrix":
		return m.Room
	case "discord":
		return "discord:" + m.Room
	}
	return m.From
}

// Sender sends a message to a number or a room.
type Sender interface {
	Send(ctx context.Context, to, text string) error
}

// Senders routes an answer by its address: a room (!…) to Matrix,
// discord:<channel> to Discord, a number to Signal.
type Senders struct{ Signal, Matrix, Discord Sender }

// Send one message on the address's platform.
func (s Senders) Send(ctx context.Context, to, text string) error {
	out := s.Signal
	if strings.HasPrefix(to, "!") {
		out = s.Matrix
	} else if strings.HasPrefix(to, "discord:") {
		out = s.Discord
	}
	if out == nil {
		return fmt.Errorf("no sender for %s", to)
	}
	return out.Send(ctx, to, text)
}

// admin: the sender is the admin on a platform that decides (ADR 0016 §3).
func (r *Relay) admin(m Message) bool {
	switch m.Platform {
	case "signal":
		return r.cfg.Admin != "" && m.From == r.cfg.Admin
	case "matrix":
		return r.cfg.AdminMXID != "" && m.From == r.cfg.AdminMXID && m.Room == r.cfg.Room
	case "discord": // the author Discord names, in the owner's DM the relay itself reads (#463)
		return r.cfg.AdminDiscord != "" && m.From == r.cfg.AdminDiscord && m.Room == r.cfg.DiscordChannel
	}
	return false // Telegram and anything else never decide
}

// PollCloser ends a Signal poll after its decision (#509).
type PollCloser interface {
	ClosePoll(ctx context.Context, to string, ts int64) error
}

// ClosePoll ends a poll on Signal.
func (s Senders) ClosePoll(ctx context.Context, to string, ts int64) error {
	if pc, ok := s.Signal.(PollCloser); ok {
		return pc.ClosePoll(ctx, to, ts)
	}
	return errors.New("no Signal sender")
}

// Relay handles messages.
type Relay struct {
	cfg     Config
	out     Sender
	h       *http.Client
	decided map[int64]bool // polls already decided; only the Signal loop touches it (#509)
}

// New builds a relay.
func New(cfg Config, out Sender) *Relay {
	return &Relay{cfg: cfg, out: out, h: &http.Client{Timeout: 60 * time.Second}, decided: map[int64]bool{}}
}

var verdict = regexp.MustCompile(`(?i)^\s*(gib|lehne)\s+#?(\d+)\s+(frei|ab)\s*[.!]?\s*$`)

// show: "zeig #17" — the decision card, before the verdict (#58)
var show = regexp.MustCompile(`(?i)^\s*zeig\s+#?(\d+)\s*[.!?]?\s*$`)

// laib calls one Laib tool with the approver token.
func (r *Relay) laib(ctx context.Context, verb, args string) (string, error) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "laib_" + verb, "arguments": map[string]string{"args": args}}})
	req, err := http.NewRequestWithContext(ctx, "POST", r.cfg.LaibURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+r.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.h.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("laib: %s", strings.TrimSpace(string(raw)))
	}
	if env.Error != nil {
		return "", errors.New("laib: " + env.Error.Message)
	}
	text := ""
	if len(env.Result.Content) > 0 {
		text = strings.TrimSpace(env.Result.Content[0].Text)
	}
	if env.Result.IsError {
		return "", errors.New(text)
	}
	return text, nil
}

type proposal struct {
	ID      int      `json:"id"`
	Actor   string   `json:"actor"`
	Reason  string   `json:"reason"`
	Tier    string   `json:"tier"`
	Class   string   `json:"class"`
	Reasons []string `json:"reasons"`
	Files   []string `json:"files"`
	Card    struct {
		Summary    string   `json:"summary"`
		Impact     []string `json:"impact"`
		Reversible string   `json:"reversible"`
		Why        string   `json:"why"`
		Primary    string   `json:"primary"`
	} `json:"card"`
}

// inboxOnly: a destroy, a removal or what only a human may allow is never
// decided in the chat (#441), neither by words nor by a poll (#509).
func (p *proposal) inboxOnly() bool {
	return p.Tier == "destroy" || p.Class == "human" || p.Class == "remove"
}

// cardText renders the decision card for a chat (#58).
func cardText(p *proposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Vorschlag #%d: %s\n", p.ID, p.Card.Summary)
	for _, i := range p.Card.Impact {
		fmt.Fprintf(&b, "· %s\n", i)
	}
	fmt.Fprintf(&b, "%s\n%s\n", p.Card.Reversible, p.Card.Why)
	if p.inboxOnly() {
		b.WriteString("Das entscheidest du nur in der Inbox, mit dem zweiten Faktor.")
	} else if p.Card.Primary == "Später" {
		fmt.Fprintf(&b, "Das kann warten — nichts tun ist sicher. Freigeben: „gib #%d frei“, ablehnen: „lehne #%d ab“.", p.ID, p.ID)
	} else {
		fmt.Fprintf(&b, "Freigeben: „gib #%d frei“, ablehnen: „lehne #%d ab“.", p.ID, p.ID)
	}
	return b.String()
}

// Handle looks at one message: not the admin → a journal line and nothing
// else; not a verdict → nothing; a verdict → the proposal's consequence is
// looked up, Laib decides, the admin gets the outcome.
func (r *Relay) Handle(ctx context.Context, m Message) {
	if m.Target != 0 {
		r.vote(ctx, m)
		return
	}
	to := m.replyTo()
	if sm := show.FindStringSubmatch(m.Text); sm != nil && r.admin(m) {
		if p := r.open(ctx, sm[1]); p != nil {
			r.out.Send(ctx, to, cardText(p))
		} else {
			r.out.Send(ctx, to, fmt.Sprintf("Vorschlag #%s ist nicht offen.", sm[1]))
		}
		return
	}
	match := verdict.FindStringSubmatch(m.Text)
	if match == nil {
		return // plain chat is Hermes' business
	}
	if !r.admin(m) {
		r.ignored(ctx, m, "schrieb '"+match[0]+"'")
		return
	}
	id := match[2]
	approve := strings.EqualFold(match[1], "gib")
	p := r.open(ctx, id)
	if p == nil {
		r.out.Send(ctx, to, fmt.Sprintf("Vorschlag #%s ist nicht offen — nichts getan. `laib inbox` zeigt die offenen.", id))
		return
	}
	what, out, err := r.decide(ctx, p, approve)
	switch {
	case err != nil:
		r.out.Send(ctx, to, fmt.Sprintf("Vorschlag #%s (%s): nicht %s — %v", id, what, map[bool]string{true: "freigegeben", false: "abgelehnt"}[approve], err))
	case approve:
		r.out.Send(ctx, to, fmt.Sprintf("Vorschlag #%s freigegeben: %s\n%s", id, what, tail(out, 300)))
	default:
		r.out.Send(ctx, to, fmt.Sprintf("Vorschlag #%s abgelehnt: %s", id, what))
	}
}

// ignored journals what someone not the admin tried, and does nothing else.
func (r *Relay) ignored(ctx context.Context, m Message, did string) {
	who := "unbekannter Nummer"
	if m.Platform != "signal" {
		who = "fremdem Absender (" + strings.TrimSpace(m.Platform+" "+m.Room) + ")"
	}
	clean := func(s string) string { return strings.ReplaceAll(s, "\"", "'") }
	r.laib(ctx, "note", fmt.Sprintf("--kind watch \"Freigabe von %s ignoriert: %s %s\"", clean(who), clean(m.From), clean(did)))
}

// decide is the one verdict both the words and the poll reach: Laib
// approves or rejects with the approver token, and its own gate refuses
// what only the Inbox may allow (#48). It returns what the proposal does.
func (r *Relay) decide(ctx context.Context, p *proposal, approve bool) (what, out string, err error) {
	what = p.Card.Summary
	if what == "" {
		what = p.Reason
	}
	if len(p.Reasons) > 0 {
		what += " — " + strings.Join(p.Reasons, "; ")
	}
	verb := "approve"
	if !approve {
		verb = "reject"
	}
	out, err = r.laib(ctx, verb, fmt.Sprint(p.ID))
	return what, out, err
}

// vote is a vote on a poll or a 👍/👎 (#509). It counts only from the admin,
// only on a poll bote-notify sent (the Bote's own message, its timestamp in
// umfragen.json), once per poll, and only for what the chat may decide;
// anything else from the admin is Hermes' business, and anyone else's is
// journaled.
func (r *Relay) vote(ctx context.Context, m Message) {
	if !r.admin(m) {
		r.ignored(ctx, m, fmt.Sprintf("stimmte '%s' auf Nachricht %d", m.Choice, m.Target))
		return
	}
	id := r.pollFor(m)
	if id == 0 {
		return // a reaction on any other message
	}
	to := m.replyTo()
	if r.decided[m.Target] {
		r.laib(ctx, "note", fmt.Sprintf("--kind watch \"Doppelte Stimme '%s' zu Vorschlag #%d ignoriert: schon entschieden\"", m.Choice, id))
		return
	}
	if m.Choice == "später" {
		r.out.Send(ctx, to, fmt.Sprintf("Gut, Vorschlag #%d bleibt offen.", id))
		return
	}
	p := r.open(ctx, fmt.Sprint(id))
	switch {
	case p == nil:
		r.out.Send(ctx, to, fmt.Sprintf("Vorschlag #%d ist nicht mehr offen — nichts getan.", id))
	case p.inboxOnly():
		r.out.Send(ctx, to, fmt.Sprintf("Vorschlag #%d entscheidest du nur in der Inbox, mit dem zweiten Faktor — nichts getan.", id))
		return
	default:
		what, _, err := r.decide(ctx, p, m.Choice == "frei")
		switch {
		case err != nil: // the poll stays open: the admin may vote again
			r.out.Send(ctx, to, fmt.Sprintf("Vorschlag #%d: Fehler — %v", id, err))
			return
		case m.Choice == "frei":
			r.out.Send(ctx, to, fmt.Sprintf("Vorschlag #%d angewendet: %s", id, what))
		default:
			r.out.Send(ctx, to, fmt.Sprintf("Vorschlag #%d abgelehnt.", id))
		}
	}
	r.decided[m.Target] = true
	if pc, ok := r.out.(PollCloser); ok {
		if err := pc.ClosePoll(ctx, to, m.Target); err != nil {
			log.Printf("relay: poll %d not closed: %v", m.Target, err)
		}
	}
}

// pollFor: the proposal a poll of the Bote's stands for, 0 for any other message.
func (r *Relay) pollFor(m Message) int {
	if r.cfg.Polls == "" || m.TargetAuthor != r.cfg.Account {
		return 0
	}
	b, err := os.ReadFile(r.cfg.Polls)
	if err != nil {
		return 0
	}
	var polls map[string]struct {
		Vorschlag int `json:"vorschlag"`
	}
	json.Unmarshal(b, &polls)
	return polls[strconv.FormatInt(m.Target, 10)].Vorschlag
}

// open looks one open proposal up through laib's inbox.
func (r *Relay) open(ctx context.Context, id string) *proposal {
	text, err := r.laib(ctx, "inbox", "")
	if err != nil {
		return nil
	}
	var ps []proposal
	if i := strings.IndexByte(text, '['); i >= 0 {
		json.Unmarshal([]byte(text[i:]), &ps)
	}
	for i := range ps {
		if fmt.Sprint(ps[i].ID) == id {
			return &ps[i]
		}
	}
	return nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// ParseEvent reads a signal-cli daemon event (a JSON-RPC "receive"
// notification) into a Message; receipts, typing and sync events are not
// messages. A poll vote (options 0 Freigeben, 1 Ablehnen, 2 Später) and a
// 👍/👎 reaction carry the message they target (#509); a withdrawn vote or
// reaction and any other emoji are nothing.
func ParseEvent(data []byte) (Message, bool) {
	type target struct {
		AuthorNumber        string `json:"authorNumber"`
		TargetAuthorNumber  string `json:"targetAuthorNumber"`
		TargetSentTimestamp int64  `json:"targetSentTimestamp"`
	}
	var ev struct {
		Method string `json:"method"`
		Params struct {
			Envelope struct {
				Source       string `json:"source"`
				SourceNumber string `json:"sourceNumber"`
				DataMessage  *struct {
					Message  string `json:"message"`
					PollVote *struct {
						target
						OptionIndexes []int `json:"optionIndexes"`
					} `json:"pollVote"`
					Reaction *struct {
						target
						Emoji    string `json:"emoji"`
						IsRemove bool   `json:"isRemove"`
					} `json:"reaction"`
				} `json:"dataMessage"`
			} `json:"envelope"`
		} `json:"params"`
	}
	if err := json.Unmarshal(data, &ev); err != nil || ev.Method != "receive" || ev.Params.Envelope.DataMessage == nil {
		return Message{}, false
	}
	m := Message{Platform: "signal", From: cmp.Or(ev.Params.Envelope.SourceNumber, ev.Params.Envelope.Source)}
	dm := ev.Params.Envelope.DataMessage
	switch {
	case dm.PollVote != nil && len(dm.PollVote.OptionIndexes) == 1 && dm.PollVote.OptionIndexes[0] >= 0 && dm.PollVote.OptionIndexes[0] <= 2:
		m.Target, m.TargetAuthor = dm.PollVote.TargetSentTimestamp, dm.PollVote.AuthorNumber // the poll's author
		m.Choice = []string{"frei", "ab", "später"}[dm.PollVote.OptionIndexes[0]]
	case dm.Reaction != nil && !dm.Reaction.IsRemove && (strings.HasPrefix(dm.Reaction.Emoji, "👍") || strings.HasPrefix(dm.Reaction.Emoji, "👎")):
		m.Target, m.TargetAuthor = dm.Reaction.TargetSentTimestamp, dm.Reaction.TargetAuthorNumber
		m.Choice = map[bool]string{true: "frei", false: "ab"}[strings.HasPrefix(dm.Reaction.Emoji, "👍")]
	case dm.PollVote != nil || dm.Reaction != nil:
		return Message{}, false
	default:
		m.Text = strings.TrimSpace(dm.Message)
	}
	if m.From == "" || m.Text == "" && m.Target == 0 {
		return Message{}, false
	}
	return m, true
}

// SignalSender sends through the daemon's JSON-RPC endpoint.
type SignalSender struct {
	URL     string
	Account string
	H       *http.Client
}

// Send one text message.
func (s *SignalSender) Send(ctx context.Context, to, text string) error {
	return s.rpc(ctx, "send", map[string]any{"account": s.Account, "recipient": []string{to}, "message": text})
}

// ClosePoll ends a poll the Bote sent (#509).
func (s *SignalSender) ClosePoll(ctx context.Context, to string, ts int64) error {
	return s.rpc(ctx, "sendPollTerminate", map[string]any{"account": s.Account, "recipient": []string{to}, "pollTimestamp": ts})
}

func (s *SignalSender) rpc(ctx context.Context, method string, params map[string]any) error {
	h := s.H
	if h == nil {
		h = &http.Client{Timeout: 30 * time.Second}
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(s.URL, "/")+"/api/v1/rpc", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("signal %s: %s", method, resp.Status)
	}
	if out.Error != nil {
		return fmt.Errorf("signal %s: %s", method, out.Error.Message)
	}
	return nil
}

// Run subscribes to the daemon's event stream and handles every message
// until the context ends; it reconnects after a pause.
func (r *Relay) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := r.listen(ctx); err != nil && ctx.Err() == nil {
			log.Printf("relay: events: %v (reconnect in 10 s)", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
	}
}

func (r *Relay) listen(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(r.cfg.SignalURL, "/")+"/api/v1/events?account="+url.QueryEscape(r.cfg.Account), nil) // a "+" unescaped is a space (#469)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("events: %s", resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		if m, ok := ParseEvent(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))); ok {
			r.Handle(ctx, m)
		}
	}
	return sc.Err()
}
