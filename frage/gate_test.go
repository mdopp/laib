package frage

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const schlaffler = "Auch ein netter Schlaffler. Ja, macht gar nicht gerne, ne?" // Whisper, 2026-11-14 18:10, table talk

// gateModel answers the gate „nein“ for the sentences in no, „Ja.“ for any other, and a
// summary for a summary call; it counts the gate's calls and checks their shape.
func gateModel(t *testing.T, no ...string) (string, *atomic.Int32) {
	var calls atomic.Int32
	m := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MaxTokens int                 `json:"max_tokens"`
			Messages  []map[string]string `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		answer := "Morgen wird es sonnig."
		if req.Messages[0]["content"] == gatePrompt {
			calls.Add(1)
			if req.MaxTokens > 16 {
				t.Errorf("the gate asks for %d tokens: one word is enough", req.MaxTokens)
			}
			answer = "Ja."
			for _, n := range no {
				if req.Messages[1]["content"] == n {
					answer = "nein"
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": answer}}}})
	}))
	t.Cleanup(m.Close)
	return m.URL + "/v1", &calls
}

// Ablauf L-47
func TestTheGateTellsTalkFromRequests(t *testing.T) {
	model, calls := gateModel(t, schlaffler)
	c := Config{ModelURL: model}
	for _, tc := range []struct {
		q         string
		want, ask bool // addressed; whether the model was asked
	}{
		{schlaffler, false, true},
		{"Ska, Ska, Ska, Ska, Ska, Ska.", false, false}, // Whisper's hallucination on noise
		{"Ja, ja, ja.", false, false},
		{"", false, false},
		{"Hm.", false, false},
		{"Spiele Einslive", true, true},
		{"Wie wird das Wetter morgen?", true, true},
		{"Mach das Licht im Büro an", true, true},
		{"Licht an, Licht aus, Licht an", true, true}, // repeated words, but not one word over and over
	} {
		before := calls.Load()
		if got := c.addressed(context.Background(), tc.q); got != tc.want {
			t.Errorf("addressed(%q) = %v, want %v", tc.q, got, tc.want)
		}
		if asked := calls.Load() > before; asked != tc.ask {
			t.Errorf("%q: model asked %v, want %v", tc.q, asked, tc.ask)
		}
	}
	// a model that fails is a yes: a real request is never dropped
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "busy", http.StatusConflict) }))
	defer down.Close()
	if !(Config{ModelURL: down.URL + "/v1"}).addressed(context.Background(), schlaffler) {
		t.Error("the gate dropped a sentence when the model failed")
	}
}

// fakeHA records assist_satellite.announce calls.
func fakeHA(t *testing.T) (string, chan map[string]any) {
	got := make(chan map[string]any, 4)
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/services/assist_satellite/announce" || r.Header.Get("Authorization") != "Bearer ha-tok" {
			t.Errorf("HA called at %s with %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		got <- b
		io.WriteString(w, "[]")
	}))
	t.Cleanup(ha.Close)
	return ha.URL, got
}

// Ablauf L-47
func TestAWakeNobodyMeantGetsNoWordsButATone(t *testing.T) {
	model, _ := gateModel(t, schlaffler)
	var searched atomic.Int32 // the overheard sentence's searches
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == schlaffler {
			searched.Add(1)
		}
		json.NewEncoder(w).Encode(map[string]any{"results": []Hit{{Title: "Wetter", URL: "https://wetter.example/", Content: "Sonne."}}})
	}))
	defer searx.Close()
	ha, announced := fakeHA(t)
	wakes, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer wakes.Close()
	signal := make(chan string, 4)
	go func() {
		for {
			conn, err := wakes.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadString('\n')
			signal <- line
			conn.Close()
		}
	}()
	c := Config{SearxURL: searx.URL, ModelURL: model, HAURL: ha, HAToken: "ha-tok", Tone: "/local/laib/falsch.wav", WakesAddr: wakes.Addr().String()}
	srv := httptest.NewServer(Handler(c))
	defer srv.Close()
	ask := func(q, system string) string {
		body, _ := json.Marshal(map[string]any{"model": "frage", "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": q}}})
		resp, err := http.Post(srv.URL+"/api/chat", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var text string
		for sc := bufio.NewScanner(resp.Body); sc.Scan(); {
			var chunk struct {
				Message struct{ Content string } `json:"message"`
			}
			json.Unmarshal(sc.Bytes(), &chunk)
			text += chunk.Message.Content
		}
		return text
	}
	prompt := "Antworte kurz, auf Deutsch, zum Vorlesen.\nGeräte: Sofalicht\nSatellit: 0907c9dev"

	out := stdout(t, func() {
		if said := ask(schlaffler, prompt); said != "" {
			t.Errorf("a wake nobody meant was answered aloud: %q", said)
		}
		select {
		case b := <-announced:
			if b["device_id"] != "0907c9dev" || b["media_id"] != "/local/laib/falsch.wav" || b["preannounce"] != false || b["message"] != nil {
				t.Errorf("the tone's announce: %v", b)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no tone on the satellite")
		}
		select {
		case l := <-signal:
			if l != "ohne\n" {
				t.Errorf("stimme-wakes got %q, want only „ohne“", l)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("stimme-wakes was not told")
		}
	})
	if strings.Contains(out, "Schlaffler") || searched.Load() != 0 {
		t.Errorf("the overheard text reached the log (%q) or the web search (%d×)", out, searched.Load())
	}

	// a real request from the satellite is answered as before, without a tone
	if said := ask("Wie wird das Wetter morgen?", prompt); said != "Morgen wird es sonnig." {
		t.Errorf("a real question: %q", said)
	}
	// typed in Assist or asked by the Bote (no satellite): no gate, as before
	if said := ask(schlaffler, "Antworte kurz, auf Deutsch, zum Vorlesen.\nSatellit: "); said == "" {
		t.Error("a typed sentence got no answer")
	}
	select {
	case b := <-announced:
		t.Errorf("a tone after a real or a typed request: %v", b)
	case <-time.After(200 * time.Millisecond):
	}
}

// stdout is what f printed.
func stdout(t *testing.T, f func()) string {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	keep := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	f()
	os.Stdout = keep
	w.Close()
	return <-done
}
