package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/state"
)

// #54 (6): linking the Bote's number is a QR from signal-cli's REST API,
// fetched by laib so the Box page can show it — no terminal, no sgnl:// URI.
func TestSignalLinkQRComesFromTheRESTAPI(t *testing.T) {
	var seen string
	signal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.String()
		if r.URL.Path != "/v1/qrcodelink" || r.URL.Query().Get("device_name") != "Bote" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG fake"))
	}))
	defer signal.Close()
	png, err := pages.SignalLinkQR(context.Background(), signal.URL, "Bote")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(png), "\x89PNG") || !strings.Contains(seen, "device_name=Bote") {
		t.Fatalf("%q %q", png, seen)
	}
	if _, err := pages.SignalLinkQR(context.Background(), "http://127.0.0.1:1", "Bote"); err == nil {
		t.Fatal("a dead signal-cli must be an error, not an empty image")
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
