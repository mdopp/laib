// Package relay is the deterministic approval path from Signal (#48): code
// that sees the sender's number decides, never the model. Not written yet;
// relay_test.go describes it.
package relay

import "context"

// Config: the admin's number, Laib's MCP and the approver token.
type Config struct {
	Admin   string
	LaibURL string
	Token   string
}

// Message is one incoming Signal message.
type Message struct {
	From, Text string
}

// Sender sends a Signal message.
type Sender interface {
	Send(ctx context.Context, to, text string) error
}

// Relay handles messages.
type Relay struct{}

// New builds a relay.
func New(cfg Config, out Sender) *Relay { return &Relay{} }

// Handle looks at one message.
func (r *Relay) Handle(ctx context.Context, m Message) {}

// ParseEvent reads a signal-cli daemon event (JSON-RPC notification).
func ParseEvent(data []byte) (Message, bool) { return Message{}, false }
