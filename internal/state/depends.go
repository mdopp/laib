package state

import (
	"maps"
	"slices"
)

// Depends: the services whose planned stop explains a service's errors
// (#978) — its after:, its uses:, laib for needs: [mcp], the SSO role for
// an oidc: client — and theirs, sorted, without itself.
func (st *State) Depends(name string) []string {
	seen, todo := map[string]bool{name: true}, []string{name}
	for len(todo) > 0 {
		svc := st.Services[todo[0]]
		todo = todo[1:]
		if svc == nil {
			continue
		}
		c := svc.Contract
		next := slices.Concat(c.After, c.Uses)
		if slices.Contains(c.Needs, "mcp") {
			next = append(next, "laib")
		}
		if c.OIDC != nil && st.Box.Roles.SSO != "" {
			next = append(next, st.Box.Roles.SSO)
		}
		for _, d := range next {
			if !seen[d] {
				seen[d] = true
				todo = append(todo, d)
			}
		}
	}
	delete(seen, name)
	return slices.Sorted(maps.Keys(seen))
}

// Dependents: the services that depend on any of names, transitively,
// sorted, without names themselves.
func (st *State) Dependents(names ...string) []string {
	var out []string
	for _, s := range st.ServiceNames() {
		if !slices.Contains(names, s) && slices.ContainsFunc(st.Depends(s), func(d string) bool { return slices.Contains(names, d) }) {
			out = append(out, s)
		}
	}
	return out
}
