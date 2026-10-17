package apply

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/state"
)

// #288: a health probe is green on 2xx/3xx only, unless the contract names
// the codes it expects; 404 (a typo'd path, a broken route) and 401/403 are red.
func TestHealthProbeIsGreenOnlyOnWhatItShouldAnswer(t *testing.T) {
	httpDeadline, httpRetry = 300*time.Millisecond, 50*time.Millisecond
	defer func() { httpDeadline, httpRetry = 90*time.Second, 3*time.Second }()
	for _, c := range []struct {
		code   int
		expect []int
		ok     bool
	}{
		{200, nil, true},
		{204, nil, true},
		{302, nil, true},
		{404, nil, false},
		{401, nil, false},
		{403, nil, false},
		{500, nil, false},
		{401, []int{200, 401}, true},
		{200, []int{200, 401}, true},
		{302, []int{200, 401}, false},
		{404, []int{401}, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c.code == 302 {
				w.Header().Set("Location", "/login")
			}
			w.WriteHeader(c.code)
		}))
		got := waitHTTP(context.Background(), "x", state.Health{HTTP: srv.URL + "/health", Expect: c.expect})
		srv.Close()
		if got.OK != c.ok {
			t.Errorf("%d expect %v: OK=%v, want %v (%s)", c.code, c.expect, got.OK, c.ok, got.Detail)
		}
		if !got.OK && got.Detail == "" {
			t.Errorf("%d: a red probe says nothing about why", c.code)
		}
	}
}
