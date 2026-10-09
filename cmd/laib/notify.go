package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
)

// record appends a journal entry and, when box.yml names a webhook, posts
// the entry there for red results and proposal events (#15). Delivery is
// best effort with one retry; a failure is its own journal line, never a
// reason to fail the verb.
func record(env *cliEnv, st *state.State, e journal.Entry) {
	journal.Append(env.paths.Journal, e)
	if st == nil || st.Box.Notify.URL == "" {
		return
	}
	interesting := e.Result == "red" || e.Verb == "propose" || e.Verb == "approve" || e.Verb == "reject" || e.Verb == "ready"
	if !interesting {
		return
	}
	go postEvent(env, st.Box.Notify.URL, e)
}

func postEvent(env *cliEnv, url string, e journal.Entry) {
	body, _ := json.Marshal(e)
	client := &http.Client{Timeout: 5 * time.Second}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := client.Post(url, "application/json", bytes.NewReader(body))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return
			}
			last = &httpErr{resp.Status}
		} else {
			last = err
		}
		time.Sleep(2 * time.Second)
	}
	journal.Append(env.paths.Journal, journal.Entry{Actor: "laib", Verb: "notify", Result: "red", Detail: "webhook " + url + ": " + last.Error()})
}

type httpErr struct{ status string }

func (h *httpErr) Error() string { return h.status }
