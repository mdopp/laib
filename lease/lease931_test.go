package lease

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// #931: after the update run restarted the llama stack the household model
// stayed unloaded for hours: a look never wakes it (#550/#792) and nobody
// else asked. Now the lease warms the household preset itself when the
// router reports it unloaded in the household window — once, not per
// refresh while that one request still loads it — and never in another
// window, where the GPU belongs to the holder.
func TestTheLeaseWarmsTheHouseholdModelInTheHouseholdWindow(t *testing.T) {
	for _, c := range []struct {
		name   string
		status string // the household's status at the router
		lease  bool   // a foreign window stands at start
		want   int    // warm requests for the household preset
	}{
		{"unloaded in the household window", "unloaded", false, 1},
		{"already loaded", "loaded", false, 0},
		{"loading", "loading", false, 0},
		{"a router without status", "", false, 0},
		{"unloaded in another window", "unloaded", true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			asked := map[string]int{}
			release := make(chan struct{})
			router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					st := ""
					if c.status != "" {
						st = `,"status":{"value":"` + c.status + `"}`
					}
					w.Write([]byte(`{"object":"list","data":[{"id":"gemma-4-e4b"` + st + `},{"id":"gemma-4-12b","status":{"value":"unloaded"}}]}`))
					return
				}
				var req struct{ Model string }
				json.NewDecoder(r.Body).Decode(&req)
				mu.Lock()
				asked[req.Model]++
				mu.Unlock()
				<-release // the weights load for a while
				w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
			}))
			defer router.Close()
			defer close(release)
			cfg := config42(t, router.URL)
			if c.lease {
				os.MkdirAll(cfg.LeaseDir, 0o755)
				b, _ := json.Marshal(Lease{Holder: "foundry", Mode: "shared", Since: time.Now(), Until: time.Now().Add(time.Hour)})
				os.WriteFile(filepath.Join(cfg.LeaseDir, "lease.json"), b, 0o644)
			}
			s, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			s.Refresh(context.Background()) // a second measurement while the first warm still loads
			count := func() int { mu.Lock(); defer mu.Unlock(); return asked["gemma-4-e4b"] }
			for end := time.Now().Add(2 * time.Second); c.want > 0 && count() < c.want && time.Now().Before(end); {
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(200 * time.Millisecond) // room for a request that should not come
			mu.Lock()
			defer mu.Unlock()
			if asked["gemma-4-e4b"] != c.want || len(asked) > c.want {
				t.Fatalf("warm requests: %v, want %d for the household preset and none other", asked, c.want)
			}
		})
	}
}
