package lease

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// #550: an Operator session evicted the Bote's prompt cache between two of
// a person's turns. The proxy, which every model request passes, keeps
// when a person last asked: a request the asking service marks as
// background work (X-Laib-Background, the Operator's) does not count, nor
// does one the proxy refuses.
func TestTheLeaseSaysWhenAPersonLastAskedTheModel(t *testing.T) {
	_, srv, _ := testServer(t)
	defer srv.Close()
	lastHuman := func() any {
		_, out := call(t, "GET", srv.URL+"/api/model-lease", "", "")
		v, ok := out["last_human_at"]
		if !ok {
			t.Fatalf("GET lacks last_human_at: %v", out)
		}
		return v
	}
	if v := lastHuman(); v != nil {
		t.Fatalf("nobody asked yet, last_human_at is %v", v)
	}
	chat := func(model, background string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(`{"model":"`+model+`","messages":[]}`))
		if background != "" {
			req.Header.Set("X-Laib-Background", background)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := chat("gemma-4-e4b", "operator"); code != 200 {
		t.Fatal(code)
	}
	if code := chat("qwen3.6-35b-a3b", ""); code != 409 {
		t.Fatal(code)
	}
	if v := lastHuman(); v != nil {
		t.Fatalf("background work or a refused request counted as a person's: %v", v)
	}
	before := time.Now().Unix()
	if code := chat("gemma-4-e4b", ""); code != 200 {
		t.Fatal(code)
	}
	if v, _ := lastHuman().(float64); int64(v) < before || int64(v) > time.Now().Unix() {
		t.Fatalf("a person's request is not the last one: %v, asked at %d", v, before)
	}
}
