package operator

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// #861: looks got no lessons — their context is diag and the journal — and
// the first aid ignored them. A look's context carries the lessons for the
// findings it sees; the first aid names the lesson for its finding in its
// note; both sessions record the lesson as offered.
func TestALookAndTheFirstAidReadTheLessons(t *testing.T) {
	disk := `{"severity":"gelb","probe":"disk","service":"immich","detail":"/mnt/data hat 9 % frei"}`
	box := `{"journal":[],"lessons":["# Lehre: disk immich\n\n- Lösung: Den Thumbnail-Cache leeren lassen, nicht die Fotos.","# Lehre: unit bote\n\n- Lösung: Den Pod neu starten, nicht den Check.","# Lehre: unit caddy\n\n- Lösung: y"]}`
	replies := map[string]string{"laib_diag": "[" + disk + "]", "laib_context": box, "laib_logs": boteLogs, "laib_restart": "grün: bote"}
	mcp, calls := fakeMCP(t, replies)
	llm, seen := recLLM(t, []Message{{Role: "assistant", Content: "Gelb: wenig Platz."}})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}

	look, err := op.Run(context.Background(), skillFor(t, Trigger{Kind: "tick"}, "routineblick"), Trigger{Kind: "tick"})
	prompt := (*seen)[0][1].Content
	if err != nil || !strings.Contains(prompt, "Thumbnail-Cache leeren") || strings.Contains(prompt, "Pod neu starten") || !slices.Equal(look.Lessons, []string{"disk immich"}) {
		t.Fatalf("the look was not offered its finding's lesson, or another's: %v %+v\n%s", err, look, prompt)
	}

	// the first aid: the restart clears the red, no model; its note names the lesson
	inner := mcp.Config.Handler
	mcp.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		inner.ServeHTTP(w, r)
		if bytes.Contains(b, []byte(`"laib_restart"`)) {
			replies["laib_diag"] = "[]"
		}
	})
	replies["laib_diag"] = "[" + redBote + "]"
	tr := Trigger{Kind: "event", Service: "bote", Detail: "bote: unit bote-check.service ist failed", Restart: []string{"bote"}, Opened: true, Finding: []string{"unit bote"}}
	s, err := op.Run(context.Background(), skillFor(t, tr, ""), tr)
	if err != nil || s.Outcome != "green" || len(*seen) != 1 || !strings.Contains(s.Note, "Lehre zu unit bote: Lösung: Den Pod neu starten, nicht den Check.") || !slices.Equal(s.Lessons, []string{"unit bote"}) {
		t.Fatalf("the first aid did not read the lesson: %v %+v %v", err, s, *calls)
	}
}
