package frage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
)

// A wake nobody meant (owner 2026-11-14): the Voice PE woke on table talk, Whisper
// heard „Auch ein netter Schlaffler. Ja, macht gar nicht gerne, ne?“ and Frage
// answered at length. A request from a satellite (the prompt's line „Satellit:“,
// post-deploy) is judged first; one nobody meant gets no words (Piper stays
// silent), a short tone on that satellite, and stimme-wakes counts it „ohne
// Befehl“. The text is neither logged nor sent anywhere new.
const gatePrompt = "Ein Sprachassistent im Haushalt hat nach seinem Weckwort den Satz des Nutzers gehört. " +
	"Oft war er gar nicht gemeint: Gespräch am Tisch, Fernsehen, ein Satzfetzen, Unsinn. " +
	"Ist der Satz eine Frage, eine Bitte, ein Befehl oder ein Dank an den Assistenten oder an das Haus? " +
	"Antworte nur mit einem Wort: ja oder nein."

// addressed: too short, or the same words over and over („Ska, Ska, Ska…“), is
// not; else the model decides in a few tokens, and a model that fails or says
// anything but „nein“ means yes, so a real request is never dropped.
func (c Config) addressed(ctx context.Context, q string) bool {
	ws := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) })
	distinct := len(slices.Compact(slices.Sorted(slices.Values(ws))))
	if len([]rune(strings.Join(ws, ""))) < 3 || len(ws) >= 3 && 3*distinct <= len(ws) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	said, err := c.chat(ctx, gatePrompt, q, 0, 8)
	if err != nil {
		fmt.Println("frage: angesprochen? das Modell antwortet nicht, also ja:", err)
	}
	return err != nil || !strings.HasPrefix(strings.ToLower(said), "nein")
}

// overheard, once HA ended the run without speech: „ohne“ to stimme-wakes (nothing of
// the text), then the tone through HA's assist_satellite.announce on that device.
func (c Config) overheard(device string) {
	time.Sleep(c.ToneAfter)
	if conn, err := net.DialTimeout("tcp", c.WakesAddr, 2*time.Second); err == nil {
		conn.Write([]byte("ohne\n"))
		conn.Close()
	}
	body, _ := json.Marshal(map[string]any{"device_id": device, "media_id": c.Tone, "preannounce": false})
	req, _ := http.NewRequest(http.MethodPost, strings.TrimRight(c.HAURL, "/")+"/api/services/assist_satellite/announce", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.HAToken)
	resp, err := c.client().Do(req)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("%s", resp.Status)
		}
	}
	if err != nil {
		fmt.Println("frage: kein Ton auf dem Satelliten (FRAGE_HA_TOKEN: laib apply home-assistant):", err)
	}
}
