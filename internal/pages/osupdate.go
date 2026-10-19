package pages

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/sysd"
)

// FleetLock is Zincati's reboot lock (#368; Zincati's fleet_lock strategy,
// install-box.sh points it here): the reboot into a staged OS update is
// granted only in the window after the backup, while the backup does not
// run, laib is green and the running deployment is proven. Loopback only.
func FleetLock(env *cli.Env, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") || r.Method != http.MethodPost || r.Header.Get("fleet-lock-protocol") != "true" {
			http.Error(w, "nur Zincati auf dieser Box", http.StatusForbidden)
			return
		}
		st, err := cli.LoadState(env)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		o, _ := diag.OS(r.Context())
		if r.URL.Path == "/v1/steady-state" {
			cli.Record(env, st, journal.Entry{Actor: "laib", Verb: "os-update", Result: "info", Detail: "OS " + o.BootedVersion + " läuft (Zincati, nach dem Start)"})
			return
		}
		rd, _ := CollectStatus(r.Context(), env, st)
		why := ""
		switch a := sysd.Active(r.Context(), "laib-backup.service"); {
		case !diag.InWindow(st, now()):
			why = "außerhalb des Fensters " + diag.WindowText(st)
		case a == "active" || a == "activating":
			why = "die Sicherung läuft"
		case rd.State != "bereit" || rd.Red > 0:
			why = "laib ist nicht grün"
		case !diag.Proven(o.Booted):
			why = "der laufende Stand ist nicht bewiesen (laib-boot-check)"
		}
		if why != "" {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"kind": "laib", "value": why})
			return
		}
		cli.Record(env, st, journal.Entry{Actor: "laib", Verb: "os-update", Result: "info", Detail: "Neustart in OS " + o.Staged + " freigegeben (läuft: " + o.BootedVersion + ")"})
	}
}
