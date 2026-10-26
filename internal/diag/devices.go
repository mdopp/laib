package diag

import (
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
)

// rotated: the secrets whose last journal entry is a rotation the app took;
// a set, a refused rotation or a zurück (rotated back on red, or by hand)
// ends it.
func rotated(env *cli.Env) map[string]bool {
	out := map[string]bool{}
	all, _ := journal.Read(env.Paths.Journal, time.Time{})
	for _, e := range all {
		f := strings.Fields(e.Detail)
		switch {
		case e.Verb != "secret" || len(f) < 2:
		case f[0] == "set":
			out[f[1]] = false
		case len(f) < 3 || f[0] != "secret":
		case f[1] == "zurück", strings.HasSuffix(e.Detail, ": zurück"):
			out[strings.TrimSuffix(f[2], ":")] = false
		case f[1] == "rotate":
			out[strings.TrimSuffix(f[2], ":")] = !strings.HasSuffix(e.Detail, "nicht übernommen")
		}
	}
	return out
}

// deviceFindings (#665): a secret devices hold that was rotated locks them
// out (the door lock lost MQTT); the repair puts the value from before back.
func deviceFindings(env *cli.Env, st *state.State) []Finding {
	var out []Finding
	rot := rotated(env)
	for _, svc := range st.ServiceNames() {
		for _, s := range st.Services[svc].Contract.Secrets {
			if s.Devices && !s.External && rot[s.Name] {
				out = append(out, Finding{Severity: "gelb", Probe: "secret", Service: svc, Detail: s.Name + " wurde erneuert, aber Geräte kennen das alte",
					Fix: "laib repair secret " + svc + ": nach der Freigabe läuft laib secret zurueck " + s.Name + " und gibt ihm den Wert vor der Erneuerung zurück", Repair: []string{"secret", "zurueck", s.Name}})
			}
		}
	}
	return out
}
