package pages

import (
	"cmp"
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/proposal"
)

// order sorts the Inbox's cards (ADR 0037 §7): hoch first, then mittel,
// then niedrig; cards that wait on one another (wartet: vorschlag:N) stand
// together as steps, the first step first, under one line „Mosquitto vor
// Scanner – 2 Schritte“; else by number. A card's weight and its chain stay
// when it is decided, so nothing moves (#941). heads: that line, by the
// first step's id.
func order(all []*proposal.Proposal, domain string) (heads map[int]string) {
	ids := map[int]*proposal.Proposal{}
	for _, p := range all {
		ids[p.ID] = p
	}
	shown := func(p *proposal.Proposal) bool {
		return p != nil && (p.Status == "open" || time.Since(p.Decided) < 15*time.Minute)
	}
	prev := func(p *proposal.Proposal) *proposal.Proposal {
		for _, w := range p.WartetAuf {
			n, err := strconv.Atoi(strings.TrimPrefix(w, "vorschlag:"))
			if q := ids[n]; err == nil && q != p && shown(q) {
				return q
			}
		}
		return nil
	}
	root, depth, rank, steps := map[int]int{}, map[int]int{}, map[int]int{}, map[int][]*proposal.Proposal{}
	for _, p := range all {
		r := p
		for q := prev(r); q != nil && depth[p.ID] < len(all); q = prev(r) {
			r, depth[p.ID] = q, depth[p.ID]+1
		}
		root[p.ID] = r.ID
	}
	for _, p := range all {
		k := slices.Index([]string{"hoch", "mittel", "gering"}, proposal.Card(p, domain).Risk)
		if old, ok := rank[root[p.ID]]; shown(p) && (!ok || k < old) {
			rank[root[p.ID]] = k
		}
	}
	slices.SortStableFunc(all, func(a, b *proposal.Proposal) int {
		return cmp.Or(rank[root[a.ID]]-rank[root[b.ID]], root[a.ID]-root[b.ID], depth[a.ID]-depth[b.ID], a.ID-b.ID)
	})
	for _, p := range all {
		if shown(p) {
			steps[root[p.ID]] = append(steps[root[p.ID]], p)
		}
	}
	heads = map[int]string{}
	for r, ps := range steps {
		if len(ps) < 2 {
			continue
		}
		names := make([]string, len(ps))
		for i, p := range ps {
			names[i] = stepName(p)
		}
		heads[r] = fmt.Sprintf("<p class=schritte>%s – %d Schritte</p>", html.EscapeString(strings.Join(names, " vor ")), len(ps))
	}
	return heads
}

// stepName: the service a step is about, as the steps' line names it.
func stepName(p *proposal.Proposal) string {
	name := "#" + strconv.Itoa(p.ID)
	if len(p.Adds) > 0 {
		name = p.Adds[0]
	} else if len(p.Changes) > 0 && p.Changes[0].Service != "" {
		name = p.Changes[0].Service
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
