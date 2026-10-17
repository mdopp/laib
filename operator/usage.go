package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Usage reads usage-metrics (mdopp/usage-metrics) for the nutzung skill
// (#270): anonymous counts per app and event (GET /summary?days=) and the
// sentences the Bote and the voice could not answer, kept 30 days without
// names, for admins only (GET /misses?days=, usage-metrics#13).
type Usage struct {
	URL    string
	Token  string
	Client *http.Client
}

func (u *Usage) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(u.URL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+u.Token)
	client := u.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", path, resp.Status)
	}
	return b, nil
}

// Read is the window as text for the model, plus the unanswered sentences
// themselves, which a suggestion may not quote. Without /misses (an older
// usage-metrics) the counts still come.
func (u *Usage) Read(ctx context.Context, days int) (string, []string, error) {
	var sum struct {
		From  string
		Total int
		Apps  []struct {
			App    string
			Total  int
			Events []struct {
				Event string
				Count int
			}
		}
		KnownApps []string
	}
	b, err := u.get(ctx, fmt.Sprintf("/summary?days=%d", days))
	if err == nil {
		err = json.Unmarshal(b, &sum)
	}
	if err != nil {
		return "", nil, err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Nutzung der letzten %d Tage (anonyme Zählungen, seit %s): %d insgesamt.\n", days, sum.From, sum.Total)
	used := map[string]bool{}
	for _, a := range sum.Apps {
		used[a.App] = true
		var ev []string
		for _, e := range a.Events {
			ev = append(ev, fmt.Sprintf("%s %d", e.Event, e.Count))
		}
		fmt.Fprintf(&out, "- %s: %d (%s)\n", a.App, a.Total, strings.Join(ev, ", "))
	}
	var idle []string
	for _, k := range sum.KnownApps {
		if !used[k] {
			idle = append(idle, k)
		}
	}
	if len(idle) > 0 {
		fmt.Fprintf(&out, "Ohne Nutzung in diesen %d Tagen: %s\n", days, strings.Join(idle, ", "))
	}
	type miss struct {
		Source, Text string
		Count        int
		LastDay      string
	}
	var misses []miss
	b, err = u.get(ctx, fmt.Sprintf("/misses?days=%d", days))
	if err == nil {
		if bytes.HasPrefix(bytes.TrimSpace(b), []byte("[")) {
			err = json.Unmarshal(b, &misses)
		} else {
			var wrap struct{ Misses []miss }
			err = json.Unmarshal(b, &wrap)
			misses = wrap.Misses
		}
	}
	if err != nil {
		fmt.Fprintf(&out, "\nUnbeantwortete Sätze: nicht abrufbar (%v).\n", err)
		return out.String(), nil, nil
	}
	out.WriteString("\nUnbeantwortete Sätze (ohne Namen, nur für dich — nicht wörtlich zitieren):\n")
	var texts []string
	for _, m := range misses {
		fmt.Fprintf(&out, "- %d× %s: „%s“ (zuletzt %s)\n", m.Count, m.Source, m.Text, m.LastDay)
		texts = append(texts, m.Text)
	}
	if len(misses) == 0 {
		out.WriteString("- keine\n")
	}
	return out.String(), texts, nil
}

// quotes: a text carries one of the family's unanswered sentences word for
// word. A suggestion becomes a ticket, and an intake repo may be public;
// the sentences are for admins only (owner, 2026-10-16).
func quotes(text string, sentences []string) bool {
	t := strings.ToLower(text)
	for _, s := range sentences {
		s = strings.ToLower(strings.TrimRight(strings.TrimSpace(s), "?!. "))
		if len([]rune(s)) >= 8 && strings.Contains(t, s) {
			return true
		}
	}
	return false
}
