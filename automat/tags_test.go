package automat

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeWS is HA's /api/websocket for one connection: auth_required, the
// token, then answers each command with result(cmd) — the shape is HA's
// tag/list as Homes' fake has it (unverified on a real HA).
func fakeWS(t *testing.T, token string, result func(cmd map[string]any) any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/websocket" {
			w.WriteHeader(404)
			return
		}
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		nc, brw, _ := w.(http.Hijacker).Hijack()
		defer nc.Close()
		brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
		send := func(v any) {
			b, _ := json.Marshal(v)
			brw.Write(append([]byte{0x81, 126, byte(len(b) >> 8), byte(len(b))}, b...)) // unmasked, as a server sends
			brw.Flush()
		}
		send(map[string]string{"type": "auth_required"})
		for {
			msg, err := readMasked(brw.Reader)
			if err != nil {
				return
			}
			var m map[string]any
			json.Unmarshal(msg, &m)
			switch {
			case m["type"] == "auth" && m["access_token"] == token:
				send(map[string]string{"type": "auth_ok"})
			case m["type"] == "auth":
				send(map[string]string{"type": "auth_invalid"})
				return
			default:
				send(map[string]any{"type": "event", "id": m["id"]}) // something else first: read on to the result
				send(map[string]any{"id": m["id"], "type": "result", "success": true, "result": result(m)})
			}
		}
	}))
}

// readMasked is a client's frame as the server reads it.
func readMasked(r *bufio.Reader) ([]byte, error) {
	var h [2]byte
	if _, err := r.Read(h[:1]); err != nil {
		return nil, err
	}
	b, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	h[1] = b
	n := int(h[1] & 0x7f)
	if n == 126 {
		hi, _ := r.ReadByte()
		lo, _ := r.ReadByte()
		n = int(hi)<<8 | int(lo)
	}
	key := make([]byte, 4)
	for i := range key {
		key[i], _ = r.ReadByte()
	}
	p := make([]byte, n)
	for i := range p {
		c, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		p[i] = c ^ key[i%4]
	}
	return p, nil
}

// homes ADR 0004 §3: the tags by name, for „wenn ich den Tag ‚Bürotür‘ berühre“.
func TestTagsListsHAsTagRegistryByName(t *testing.T) {
	var asked string
	ha := fakeWS(t, "tok", func(cmd map[string]any) any {
		asked, _ = cmd["type"].(string)
		return []map[string]any{{"id": "homes-0a1b2c3d4e5f", "name": "Bürotür"}, {"tag_id": "alt-companion", "name": "Bett"}, {"id": "ohne"}}
	})
	defer ha.Close()
	h := Server(Config{HAURL: ha.URL, Token: "tok"}, "test").Handler()
	out, isErr := text(call(t, h, "tools/call", map[string]any{"name": "tags", "arguments": map[string]any{}}))
	if isErr || asked != "tag/list" {
		t.Fatalf("tags: %q %v (asked %q)", out, isErr, asked)
	}
	for _, want := range []string{"homes-0a1b2c3d4e5f · Bürotür", "alt-companion · Bett", "ohne · (ohne Namen)"} {
		if !strings.Contains(out, want) {
			t.Errorf("tags lacks %q:\n%s", want, out)
		}
	}
	empty := fakeWS(t, "tok", func(map[string]any) any { return []any{} })
	defer empty.Close()
	if out, _ := text(call(t, Server(Config{HAURL: empty.URL, Token: "tok"}, "test").Handler(), "tools/call", map[string]any{"name": "tags", "arguments": map[string]any{}})); !strings.Contains(out, "keine Tags") {
		t.Errorf("no tags: %q", out)
	}
	if out, isErr := text(call(t, Server(Config{HAURL: ha.URL, Token: "falsch"}, "test").Handler(), "tools/call", map[string]any{"name": "tags", "arguments": map[string]any{}})); !isErr || !strings.Contains(out, "Token abgelehnt") {
		t.Errorf("a refused token must say so: %q %v", out, isErr)
	}
}

// homes ADR 0004 §1: the model that writes an automation knows the phone's
// events and their fields, from the server's instructions and the tools.
func TestThePhonesTriggersAreInTheDescriptions(t *testing.T) {
	h := Server(Config{HAURL: "http://127.0.0.1:1", Token: "tok"}, "test").Handler()
	init := call(t, h, "initialize", nil)
	instr, _ := init["result"].(map[string]any)["instructions"].(string)
	list, _ := json.Marshal(call(t, h, "tools/list", nil))
	for _, want := range []string{"homes_laden", "tag_scanned", "zustand: an", "ladeart: kabellos", "trigger: tag", "person"} {
		if !strings.Contains(instr, want) || !strings.Contains(string(list), want) {
			t.Errorf("%q missing from the instructions or the tools", want)
		}
	}
	if !strings.Contains(instr, "nie von dir aus") {
		t.Error("the instructions must say the model never confirms on its own")
	}
}

// homes ADR 0004 §3: switching waits for its code like every other change.
func TestSchaltenWaitsForItsCode(t *testing.T) {
	ha, _ := fakeHA(t)
	defer ha.Close()
	var switched []string
	inner := ha.Config.Handler
	ha.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/services/automation/") {
			switched = append(switched, r.URL.Path)
		}
		inner.ServeHTTP(w, r)
	})
	h := Server(Config{HAURL: ha.URL, Token: "tok"}, "test").Handler()
	out, isErr := text(call(t, h, "tools/call", map[string]any{"name": "automation_schalten", "arguments": map[string]any{"id": "flur_aus", "an": true}}))
	if isErr || len(switched) != 0 || codeIn(out) == "" || !strings.Contains(out, "einschalten") {
		t.Fatalf("schalten switched before its code: %q %v", out, switched)
	}
	if out, isErr = text(call(t, h, "tools/call", map[string]any{"name": "automation_bestaetigen", "arguments": map[string]any{"code": codeIn(out)}})); isErr || len(switched) != 1 || switched[0] != "/api/services/automation/turn_on" {
		t.Fatalf("confirmed switch: %q %v", out, switched)
	}
}
