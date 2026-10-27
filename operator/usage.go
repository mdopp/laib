package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Usage reads usage-metrics (mdopp/usage-metrics) for the nutzung skill
// (#270) with the Operator's own token: anonymous counts per app and event
// (GET /summary?days=) and the sentences the Bote and the voice could not
// answer, kept 30 days without names, for admins only (GET /misses?days=,
// usage-metrics#13). The runner hands both to nutzung's script as they
// come (#839); the script renders them for the model and keeps a
// suggestion from quoting a sentence.
type Usage struct {
	URL    string
	Token  string
	Client *http.Client
}

func (u *Usage) get(ctx context.Context, path string) (json.RawMessage, error) {
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
	if !json.Valid(b) {
		return nil, fmt.Errorf("%s: kein JSON", path)
	}
	return b, nil
}

// Read is the window's summary and misses. Without /misses (an older
// usage-metrics) the counts still come, with the reason it is missing.
func (u *Usage) Read(ctx context.Context, days int) (map[string]any, error) {
	sum, err := u.get(ctx, fmt.Sprintf("/summary?days=%d", days))
	if err != nil {
		return nil, err
	}
	out := map[string]any{"days": days, "summary": sum}
	if misses, err := u.get(ctx, fmt.Sprintf("/misses?days=%d", days)); err != nil {
		out["missesError"] = err.Error()
	} else {
		out["misses"] = misses
	}
	return out, nil
}
