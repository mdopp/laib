package relay

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Matrix is the relay's view of the admin's one room on the Bote's own
// homeserver (#184, ADR 0016 §3). It reads the room with /messages, never
// /sync, so Hermes' device keeps its to-device messages, and it answers as
// the Bote. It decides only on the homeserver-asserted sender of a plain
// message in an unencrypted room that holds the admin and the Bote alone.
type Matrix struct {
	URL, Token, Room string
	Every            time.Duration // poll interval, default 3 s
	Retry            time.Duration // a failed Check is tried again after this, default 1 min
	H                *http.Client
	self, admin      string          // set by Check
	seen             map[string]bool // handled event ids, bounded by order
	order            []string
}

const cs = "/_matrix/client/v3"

func (x *Matrix) room() string { return cs + "/rooms/" + url.PathEscape(x.Room) }

// handled remembers an event id; true when it was handled already. A page
// without `end` repeats its events; the last 1000 ids are kept.
func (x *Matrix) handled(id string) bool {
	if id == "" || x.seen[id] {
		return id != ""
	}
	if x.seen == nil {
		x.seen = map[string]bool{}
	}
	x.seen[id], x.order = true, append(x.order, id)
	if len(x.order) > 1000 {
		delete(x.seen, x.order[0])
		x.order = x.order[1:]
	}
	return false
}

// call does one client-server request; out is filled on a 200.
func (x *Matrix) call(ctx context.Context, method, path string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(x.URL, "/")+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+x.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cmp.Or(x.H, &http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == 200 && out != nil {
		return 200, json.Unmarshal(raw, out)
	}
	return resp.StatusCode, nil
}

func failed(what string, code int, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return fmt.Errorf("%s: HTTP %d", what, code)
}

func server(id string) string { _, s, _ := strings.Cut(id, ":"); return s }

// Check refuses what the relay cannot verify: no token, an admin on another
// homeserver than the Bote's (one homeserver is trusted) or the Bote itself,
// a Bote account that is a server admin (Synapse's admin API answers it), a
// room that is not a room id, that is encrypted (the relay cannot read
// Megolm) or that holds anyone besides the admin and the Bote.
func (x *Matrix) Check(ctx context.Context, admin string) error {
	if x.Token == "" {
		return fmt.Errorf("no Matrix token (laib secret set BOTE_MATRIX_TOKEN)") // an unset secret renders empty (#185)
	}
	var who struct {
		UserID string `json:"user_id"`
	}
	if code, err := x.call(ctx, "GET", cs+"/account/whoami", nil, &who); err != nil || code != 200 {
		return failed("whoami", code, err)
	}
	x.self, x.admin = who.UserID, admin
	if server(admin) == "" || server(admin) != server(who.UserID) {
		return fmt.Errorf("%s is not on the Bote's homeserver %s, the one homeserver the relay trusts", admin, server(who.UserID))
	}
	if admin == who.UserID {
		return fmt.Errorf("ADMIN_MXID %s is the Bote itself", admin)
	}
	if code, err := x.call(ctx, "GET", "/_synapse/admin/v2/users/"+url.PathEscape(who.UserID), nil, nil); err == nil && code == 200 {
		return fmt.Errorf("the Bote's account %s is a server admin: it could act as anyone on the homeserver", who.UserID)
	}
	if !strings.HasPrefix(x.Room, "!") {
		return fmt.Errorf("ADMIN_ROOM %q is not a room id (!id:server)", x.Room)
	}
	switch code, err := x.call(ctx, "GET", x.room()+"/state/m.room.encryption/", nil, nil); {
	case err != nil:
		return failed("room "+x.Room, code, err)
	case code == 200:
		return fmt.Errorf("room %s is encrypted: the relay cannot read it and does not decide there", x.Room)
	case code != 404:
		return failed("room "+x.Room+" (is the Bote in it?)", code, nil)
	}
	var m struct {
		Joined map[string]json.RawMessage `json:"joined"`
	}
	if code, err := x.call(ctx, "GET", x.room()+"/joined_members", nil, &m); err != nil || code != 200 {
		return failed("members of "+x.Room, code, err)
	}
	if _, ok := m.Joined[admin]; !ok {
		return fmt.Errorf("%s is not in room %s", admin, x.Room)
	}
	for id := range m.Joined {
		if id != x.self && id != admin {
			return fmt.Errorf("room %s holds %s besides the admin and the Bote", x.Room, id)
		}
	}
	return nil
}

// Send one plain message to a room, as the Bote.
func (x *Matrix) Send(ctx context.Context, room, text string) error {
	txn := fmt.Sprint("relay", time.Now().UnixNano())
	code, err := x.call(ctx, "PUT", cs+"/rooms/"+url.PathEscape(room)+"/send/m.room.message/"+txn, map[string]string{"msgtype": "m.text", "body": text}, nil)
	if err != nil || code != 200 {
		return failed("matrix send", code, err)
	}
	return nil
}

// changed names what makes the room untrusted: encryption turned on, or
// someone besides the admin and the Bote joined.
func (x *Matrix) changed(raw []byte) string {
	var e struct {
		Type     string `json:"type"`
		StateKey string `json:"state_key"`
		Content  struct {
			Membership string `json:"membership"`
		} `json:"content"`
	}
	json.Unmarshal(raw, &e)
	switch {
	case e.Type == "m.room.encryption":
		return "der Raum ist jetzt verschlüsselt"
	case e.Type == "m.room.member" && e.Content.Membership == "join" && e.StateKey != x.self && e.StateKey != x.admin:
		return e.StateKey + " ist dem Raum beigetreten"
	}
	return ""
}

// ParseMatrix reads a room event into a Message: only a plain text message
// (m.text, not an edit) with its sender counts.
func ParseMatrix(raw []byte) (Message, bool) {
	var e struct {
		Type    string `json:"type"`
		Sender  string `json:"sender"`
		RoomID  string `json:"room_id"`
		Content struct {
			MsgType   string `json:"msgtype"`
			Body      string `json:"body"`
			RelatesTo struct {
				RelType string `json:"rel_type"`
			} `json:"m.relates_to"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &e) != nil || e.Type != "m.room.message" || e.Content.MsgType != "m.text" || e.Content.RelatesTo.RelType == "m.replace" {
		return Message{}, false
	}
	text := strings.TrimSpace(e.Content.Body)
	if e.Sender == "" || text == "" {
		return Message{}, false
	}
	return Message{Platform: "matrix", From: e.Sender, Room: e.RoomID, Text: text}, true
}

// RunMatrix reads the admin's room from now on until the context ends. A
// room that changes (encrypted, another member) ends Matrix approvals, with
// a journal line, until a restart's Check passes again.
func (r *Relay) RunMatrix(ctx context.Context, x *Matrix) {
	every, from := cmp.Or(x.Every, 3*time.Second), ""
	for ctx.Err() == nil {
		var page struct {
			Chunk      []json.RawMessage `json:"chunk"`
			Start, End string
		}
		q := "?dir=b&limit=1" // from now on: the current end of the room
		if from != "" {
			q = "?dir=f&limit=50&from=" + url.QueryEscape(from)
		}
		code, err := x.call(ctx, "GET", x.room()+"/messages"+q, nil, &page)
		switch {
		case err != nil || code != 200:
			if ctx.Err() == nil {
				log.Printf("relay: %v", failed("matrix messages", code, err))
			}
		case from == "":
			from = page.Start
		default:
			for _, raw := range page.Chunk {
				var ev struct {
					ID string `json:"event_id"`
				}
				if json.Unmarshal(raw, &ev); x.handled(ev.ID) {
					continue // a page without `end` repeats its events
				}
				if why := x.changed(raw); why != "" {
					log.Printf("relay: matrix: %s; Matrix approvals stop", why)
					r.laib(ctx, "note", fmt.Sprintf("--kind watch \"Matrix-Freigaben gestoppt: %s. Erst ein Neustart des Relais prüft den Raum wieder (ADR 0016).\"", strings.ReplaceAll(why, "\"", "'")))
					return
				}
				if m, ok := ParseMatrix(raw); ok && m.From != x.self {
					r.Handle(ctx, m)
				}
			}
			from = cmp.Or(page.End, from)
		}
		select {
		case <-ctx.Done():
		case <-time.After(every):
		}
	}
}

// WatchMatrix keeps Matrix approvals going without ever taking the relay
// down (#184): a failed Check (homeserver down, token revoked, room changed)
// is journaled once per reason and tried again; once it passes, the room is
// read until it changes, then checked again.
func (r *Relay) WatchMatrix(ctx context.Context, x *Matrix, admin string) {
	last := ""
	for ctx.Err() == nil {
		if err := x.Check(ctx, admin); err != nil {
			if err.Error() != last && ctx.Err() == nil {
				last = err.Error()
				log.Printf("relay: Matrix approvals off: %v", err)
				r.laib(ctx, "note", fmt.Sprintf("--kind watch \"Matrix-Freigaben aus: %s. Signal und die Box-Seite gehen weiter.\"", strings.ReplaceAll(last, "\"", "'")))
			}
			select {
			case <-ctx.Done():
			case <-time.After(cmp.Or(x.Retry, time.Minute)):
			}
			continue
		}
		last = ""
		log.Printf("relay: Matrix approvals on: %s in %s", admin, x.Room)
		r.RunMatrix(ctx, x)
	}
}
