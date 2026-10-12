package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	png, err := signalLinkQR(context.Background(), signal.URL, "Bote")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(png), "\x89PNG") || !strings.Contains(seen, "device_name=Bote") {
		t.Fatalf("%q %q", png, seen)
	}
	if _, err := signalLinkQR(context.Background(), "http://127.0.0.1:1", "Bote"); err == nil {
		t.Fatal("a dead signal-cli must be an error, not an empty image")
	}
}
