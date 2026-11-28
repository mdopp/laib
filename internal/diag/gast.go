package diag

import (
	"context"
	"fmt"
	"net"
	"slices"
	"time"

	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

// guestFindings (field failure 2026-11-28): a guest name (render.GuestNames)
// answers inside the house what it answers outside, or Chrome refuses a
// guest's link to it on the household's Wi-Fi. The box's AdGuard and a public
// resolver are asked and compared; with the same answer the box dials that
// public address itself, through the router's NAT loopback, as a guest's
// phone at home does.
func guestFindings(ctx context.Context, st *state.State, only string) []Finding {
	if st.Box.Roles.DNS == "" || only != "" {
		return nil
	}
	ask := func(server string) func(string) ([]string, error) {
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, server+":53")
		}}
		return func(n string) ([]string, error) { return r.LookupHost(ctx, n) }
	}
	dial := func(addr string) error {
		c, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(addr, "443"))
		if err == nil {
			c.Close()
		}
		return err
	}
	var out []Finding
	for _, n := range render.GuestNames(st) {
		home, herr := ask("127.0.0.1")(n)
		public, perr := ask(render.PublicResolvers[0])(n)
		if f := guestFinding(n, st.Box.LanIP, home, herr, public, perr, dial); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

func guestFinding(name, lanIP string, home []string, herr error, public []string, perr error, dial func(string) error) *Finding {
	if perr != nil || len(public) == 0 {
		return &Finding{Severity: "gelb", Probe: "gast-dns", Detail: name + " hat keine öffentliche Antwort: ein Gast erreicht den Link weder unterwegs noch im Heimnetz", Fix: "beim DNS-Anbieter der Domain: " + name + " auf die öffentliche Adresse der Box (A-Eintrag)", Unmeasured: perr != nil}
	}
	if herr != nil || len(home) == 0 {
		return &Finding{Severity: "gelb", Probe: "gast-dns", Detail: name + ": AdGuard auf der Box antwortet nicht (" + fmt.Sprint(herr) + "), was ein Gast im Heimnetz bekommt, ist offen", Fix: "laib diag adguard", Unmeasured: true}
	}
	if slices.Contains(home, lanIP) || !slices.ContainsFunc(home, func(a string) bool { return slices.Contains(public, a) }) {
		return &Finding{Severity: "gelb", Probe: "gast-dns", Detail: name + " ist im Heimnetz " + home[0] + ", öffentlich " + public[0] + ": Chrome blockiert den Gast-Link im WLAN zu Hause (ERR_LOCAL_NETWORK_PERMISSION_MISSING)", Fix: "laib apply adguard (die Ausnahme für Gast-Namen in den AdGuard-Rewrites)"}
	}
	for _, a := range public {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			if err := dial(a); err != nil {
				return &Finding{Severity: "gelb", Probe: "gast-dns", Detail: "die Box erreicht " + name + " über die öffentliche Adresse " + a + " nicht (" + err.Error() + "): ohne NAT-Loopback des Routers öffnet ein Gast den Link im WLAN zu Hause nicht", Fix: "am Router die Freigabe von Port 443 auf die Box prüfen (die FRITZ!Box leitet die eigene öffentliche Adresse für Freigaben zurück); sonst den Link über mobile Daten öffnen"}
			}
			break
		}
	}
	return nil
}
