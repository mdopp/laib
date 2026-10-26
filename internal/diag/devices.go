package diag

import (
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
)

// lastSecret: the last thing the journal shows done to each secret: "move"
// (its first set, which was the move's: carry-servicebay-secrets.sh ran laib
// secret set), "set", "rotate", "back" (rotated back on red, or zurück) or
// "refused" (the app did not take the rotation, #665).
func lastSecret(env *cli.Env) map[string]string {
	out := map[string]string{}
	all, _ := journal.Read(env.Paths.Journal, time.Time{})
	for _, e := range all {
		f := strings.Fields(e.Detail)
		switch {
		case e.Verb != "secret" || len(f) < 2:
		case f[0] == "set" && out[f[1]] == "":
			out[f[1]] = "move"
		case f[0] == "set":
			out[f[1]] = "set"
		case len(f) < 3 || f[0] != "secret":
		case f[1] == "zurück", strings.HasSuffix(e.Detail, ": zurück"):
			out[strings.TrimSuffix(f[2], ":")] = "back"
		case f[1] == "rotate" && strings.HasSuffix(e.Detail, "nicht übernommen"):
			out[strings.TrimSuffix(f[2], ":")] = "refused"
		case f[1] == "rotate":
			out[f[2]] = "rotate"
		}
	}
	return out
}

// deviceFindings (#665): a secret devices hold that was rotated locks them
// out (the door lock lost MQTT); the repair puts the value from before back.
func deviceFindings(env *cli.Env, st *state.State) []Finding {
	var out []Finding
	last := lastSecret(env)
	for _, svc := range st.ServiceNames() {
		for _, s := range st.Services[svc].Contract.Secrets {
			if s.Devices && !s.External && last[s.Name] == "rotate" {
				out = append(out, Finding{Severity: "gelb", Probe: "secret", Service: svc, Detail: s.Name + " wurde erneuert, aber Geräte kennen das alte",
					Fix: "laib repair secret " + svc + ": nach der Freigabe läuft laib secret zurueck " + s.Name + " und gibt ihm den Wert vor der Erneuerung zurück", Repair: []string{"secret", "zurueck", s.Name}})
			}
		}
	}
	return out
}
