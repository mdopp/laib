package diag

import (
	"slices"

	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

// keyFindings: an owner's key that is missing while what it serves is in
// use (#696) — a set value requires it, or it is the DNS-01 token — names
// it with the link to its own form. Whether a family uses a feature that
// only the app knows (Haus' Discogs) Laib cannot tell: no finding, no nag.
func keyFindings(st *state.State, sec map[string]string) []Finding {
	var out []Finding
	for _, k := range render.OwnerSecrets(st) {
		inUse := k.Name == st.Box.DNS.SecretRef && st.Box.DNS.Provider != "" && st.Box.TLS != "internal"
		for _, svc := range st.Services {
			for v, needs := range svc.Contract.Requires {
				inUse = inUse || svc.Set(v) && slices.ContainsFunc(needs, func(w string) bool { return svc.Values[w].Secret == k.Name })
			}
		}
		if inUse && sec[k.Name] == "" {
			out = append(out, Finding{Severity: "gelb", Probe: "schluessel", Service: k.Service, Topic: k.Name,
				Detail: k.Owner.Label + " fehlt: " + k.Owner.Warum, Fix: "Schlüssel hinterlegen: " + render.KeyLink(st, k.Name)})
		}
	}
	return out
}
