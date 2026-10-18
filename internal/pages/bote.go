package pages

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/mdopp/laib/internal/state"
)

// SignalLinkQR fetches the QR that links the Bote's number from signal-cli's
// REST API (GET /v1/qrcodelink?device_name=…), so the Box page can show it
// (#54): no terminal, no sgnl:// URI to copy.
func SignalLinkQR(ctx context.Context, base, device string) ([]byte, error) {
	u := base + "/v1/qrcodelink?device_name=" + url.QueryEscape(device)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("signal-cli antwortet nicht (%s): %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return nil, fmt.Errorf("signal-cli: HTTP %d: %s", resp.StatusCode, body)
	}
	png, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if len(png) < 8 || string(png[:4]) != "\x89PNG" {
		return nil, fmt.Errorf("signal-cli: kein PNG (%d Bytes)", len(png))
	}
	return png, nil
}

// BoteSignalBase is signal-cli's REST address on this box: the first port
// the service providing `bote` declares (its laib.yml says so), on loopback;
// empty while the Bote has no Signal (#184).
func BoteSignalBase(st *state.State) string {
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		for _, p := range svc.Contract.Provides {
			if p == "bote" && len(svc.Contract.Ports) > 0 && svc.Set(svc.Contract.Channels["signal"]) {
				return fmt.Sprintf("http://127.0.0.1:%d", svc.Contract.Ports[0].Port)
			}
		}
	}
	return ""
}
