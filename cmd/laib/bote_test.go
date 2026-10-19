package main

import (
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/state"
)

// #469: signal-cli runs as its own daemon (Hermes and the relay speak it),
// which has no QR endpoint, so the Box page tells the two ways to give the
// Bote its number: registered on a number only it uses (a spare landline by
// voice call, no second SIM — the Kraftwerk's way), or linked to a phone.
func TestTheBoxPageTellsHowToGiveTheBoteItsNumber(t *testing.T) {
	page := pages.BoteLinkPage("/mnt/data/stacks", "+4930123456")
	for _, w := range []string{"register --voice", "verify", "setPin", "link -n Bote", "signal-cli-native", "+4930123456", "/mnt/data/stacks/bote/signal", "systemctl --user stop bote-signal"} {
		if !strings.Contains(page, w) {
			t.Errorf("the page lacks %q:\n%s", w, page)
		}
	}
	if strings.Contains(page, "qrcodelink") || strings.Contains(page, "link.png") {
		t.Error("the page still asks the REST API for a QR")
	}
}

// #184: the Box page offers "Bote verbinden (QR)" only while the Bote has
// Signal; a Matrix- or Telegram-only Bote has no signal-cli to link.
func TestTheSignalQROnlyWithSignal(t *testing.T) {
	bote := func(account string) *state.State {
		return &state.State{Services: map[string]*state.Service{"bote": {Name: "bote",
			Contract: state.Contract{Provides: []string{"bote"}, Ports: []state.Port{{Port: 8085}}, Channels: map[string]string{"signal": "SIGNAL_ACCOUNT", "matrix": "MATRIX_HOMESERVER"}},
			Values:   map[string]state.Value{"SIGNAL_ACCOUNT": {Literal: account}, "MATRIX_HOMESERVER": {Literal: "https://hs.example.org"}}}}}
	}
	if got := pages.BoteSignalBase(bote("+4917012345678")); got != "http://127.0.0.1:8085" {
		t.Fatalf("with Signal: %q", got)
	}
	if got := pages.BoteSignalBase(bote("")); got != "" {
		t.Fatalf("without Signal the QR link points at nothing: %q", got)
	}
}
