package main

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/render"
)

// cmdNet is `laib net [--mermaid]` (#439): the box's network as the rendered
// config makes it. Read only; it probes nothing. `laib net geraete` is the
// LAN's devices as the host sees them (#577), served by laib-haushalt
// behind the verb door (ADR 0026).
func cmdNet(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) > 0 && args[0] == "geraete" {
		return door(ctx, env, "haushalt", "read", append([]string{"net"}, args...))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	n := render.Net(st)
	if env.JSON {
		return cli.Emit(env, struct {
			render.NetMap
			Mermaid string `json:"mermaid"`
		}{n, n.Mermaid(st)})
	}
	if len(args) > 0 && args[0] == "--mermaid" {
		fmt.Fprint(env.W(), n.Mermaid(st))
		return cli.ExitOK
	}
	w := tabwriter.NewWriter(env.W(), 0, 2, 2, ' ', 0)
	fmt.Fprintf(w, "Name\tErreichbar\tVorab Authelia\tRegel\tZiel\tohne Login\n")
	for _, r := range n.Names {
		fa := map[bool]string{true: "ja", false: "nein"}[r.ForwardAuth]
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.Reach, fa, strings.TrimSpace(r.Policy+" "+r.Subject), r.Upstream, strings.Join(r.Bypass, " "))
	}
	w.Flush()
	fmt.Fprintf(env.W(), "\nLAN: AdGuard schreibt *.%s und %s auf %s um, dann Caddy (80, 443); Internet: Fritz!Box 80/443 an Caddy.\n\nPort\tDienst\tErreichbar\n", st.Box.Domain, st.Box.Domain, st.Box.LanIP)
	for _, p := range n.Ports {
		fmt.Fprintf(env.W(), "%d\t%s\t%s\n", p.Port, p.Service, p.Reach)
	}
	fmt.Fprintf(env.W(), "\nIm Netz der Box (alle ihre Ports): %s\n", strings.Join(n.HostNetwork, ", "))
	for _, l := range n.Links {
		fmt.Fprintf(env.W(), "%s → %s über host.containers.internal:%d\n", l.From, l.To, l.Port)
	}
	return cli.ExitOK
}
