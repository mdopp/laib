package automat

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// command runs one command over HA's WebSocket API (/api/websocket) and
// decodes its result into out: the tag registry lives only there (homes ADR
// 0004). Stdlib only, the client side of RFC 6455 HA needs for one command:
// the handshake, auth, one masked text frame, and reading until its result.
func (c *client) command(ctx context.Context, cmd map[string]any, out any) error {
	u, err := url.Parse(strings.TrimRight(c.cfg.HAURL, "/") + "/api/websocket")
	if err != nil {
		return err
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = map[bool]string{true: "443", false: "80"}[u.Scheme == "https"]
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	d := &net.Dialer{}
	var nc net.Conn
	if u.Scheme == "https" {
		nc, err = (&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: host}}).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	} else {
		nc, err = d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	}
	if err != nil {
		return err
	}
	defer nc.Close()
	dl, _ := ctx.Deadline()
	nc.SetDeadline(dl)
	var k [16]byte
	rand.Read(k[:])
	key := base64.StdEncoding.EncodeToString(k[:])
	fmt.Fprintf(nc, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", u.RequestURI(), u.Host, key)
	br := bufio.NewReader(nc)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		return fmt.Errorf("home assistant: websocket: %s", resp.Status)
	}
	cmd["id"] = 1
	for _, m := range []any{map[string]string{"type": "auth", "access_token": c.cfg.Token}, cmd} {
		var in struct {
			Type    string          `json:"type"`
			ID      int             `json:"id"`
			Success bool            `json:"success"`
			Result  json.RawMessage `json:"result"`
			Error   struct{ Message string }
		}
		b, _ := json.Marshal(m)
		if err := writeFrame(nc, b); err != nil {
			return err
		}
		for in.Type != "auth_ok" && in.Type != "auth_invalid" && !(in.Type == "result" && in.ID == 1) {
			msg, err := readFrame(br)
			if err != nil {
				return fmt.Errorf("home assistant: websocket: %w", err)
			}
			json.Unmarshal(msg, &in)
		}
		switch {
		case in.Type == "auth_invalid":
			return errors.New("home assistant: websocket: Token abgelehnt")
		case in.Type == "result" && !in.Success:
			return fmt.Errorf("home assistant: %s: %s", cmd["type"], in.Error.Message)
		case in.Type == "result" && out != nil:
			return json.Unmarshal(in.Result, out)
		}
	}
	return nil
}

// writeFrame sends one masked text frame.
func writeFrame(w io.Writer, p []byte) error {
	hdr := []byte{0x81}
	switch n := len(p); {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n < 1<<16:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = binary.BigEndian.AppendUint64(append(hdr, 0x80|127), uint64(n))
	}
	var mask [4]byte
	rand.Read(mask[:])
	hdr = append(hdr, mask[:]...)
	for i := range p {
		hdr = append(hdr, p[i]^mask[i%4])
	}
	_, err := w.Write(hdr)
	return err
}

// readFrame reads one message (HA's are unmasked, maybe fragmented);
// a ping or pong is skipped, a close ends it.
func readFrame(r *bufio.Reader) ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return nil, err
		}
		n := uint64(h[1] & 0x7f)
		if n >= 126 {
			e := make([]byte, map[bool]int{true: 2, false: 8}[n == 126])
			if _, err := io.ReadFull(r, e); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(append(make([]byte, 8-len(e)), e...))
		}
		if n+uint64(len(msg)) > 16<<20 {
			return nil, errors.New("Nachricht zu groß")
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(r, p); err != nil {
			return nil, err
		}
		switch h[0] & 0x0f {
		case 0x8:
			return nil, io.EOF
		case 0x9, 0xa:
			continue
		}
		if msg = append(msg, p...); h[0]&0x80 != 0 {
			return msg, nil
		}
	}
}
