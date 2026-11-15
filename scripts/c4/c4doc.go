package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// md is a Markdown page under construction.
type md struct{ strings.Builder }

func (b *md) p(format string, a ...any) {
	fmt.Fprintf(b, format, a...)
	b.WriteString("\n\n")
}

func (b *md) mermaid(src string) {
	b.WriteString("```mermaid\n" + strings.TrimRight(src, "\n") + "\n```\n\n")
}

func (b *md) table(head []string, rows [][]string) {
	b.WriteString("| " + strings.Join(head, " | ") + " |\n|")
	b.WriteString(strings.Repeat("---|", len(head)) + "\n")
	for _, r := range rows {
		for i := range r {
			r[i] = strings.ReplaceAll(strings.ReplaceAll(r[i], "|", `\|`), "\n", " ")
		}
		b.WriteString("| " + strings.Join(r, " | ") + " |\n")
	}
	b.WriteString("\n")
}

func (b *md) list(items []string) {
	for _, s := range items {
		b.WriteString("- " + s + "\n")
	}
	b.WriteString("\n")
}

// label is a mermaid node label: quotes and angle brackets escaped, lines
// joined by <br/>.
func label(lines ...string) string {
	var out []string
	for _, l := range lines {
		if l == "" {
			continue
		}
		l = strings.NewReplacer(`"`, "#quot;", "<", "&lt;", ">", "&gt;").Replace(l)
		out = append(out, l)
	}
	return `"` + strings.Join(out, "<br/>") + `"`
}

func id(prefix, name string) string {
	return prefix + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, strings.ToLower(name))
}

// num writes 22493 as 22.493.
func num(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return s
}

// approx rounds lines to the hundred, so a PR that moves a few lines does
// not change the page; the exact counts are README's (scripts/architektur.sh).
func approx(n int) string {
	if n < 100 {
		return "< 100"
	}
	return "≈ " + num((n+50)/100*100)
}

var layerNames = []string{"Schicht 0 · Laib", "Schicht 1 · Bausteine", "Schicht 2 · Mehrwert"}

const classes = `  classDef kern fill:#1f4d38,stroke:#1f4d38,color:#fff
  classDef helfer fill:#2f6f4f,stroke:#2f6f4f,color:#fff
  classDef s0 fill:#3d6b57,stroke:#3d6b57,color:#fff
  classDef s1 fill:#e2efe7,stroke:#2f6f4f,color:#1c1f1d
  classDef s2 fill:#cfe3d6,stroke:#2f6f4f,color:#1c1f1d
  classDef plan fill:#fff,stroke:#8a918c,color:#5d6a63,stroke-dasharray:4 3
`

// containers is the level-2 diagram: every template in its layer, Laib's
// kernel and helpers in layer 0, and the edges of each laib.yml: after:
// (starts after), uses: (talks to), needs: [mcp] and instances:.
func containers(t *tree) string {
	var b strings.Builder
	b.WriteString("flowchart TB\n" + classes)
	b.WriteString("  subgraph l0[\"" + layerNames[0] + "\"]\n")
	b.WriteString("    subgraph r0a[\"Ring 0a · Kern, auf dem Host\"]\n")
	b.WriteString("      laib[" + label("laib", "Freigabe · Schlüssel · Apply · Journal · Tür") + "]:::kern\n")
	for _, n := range t.programs("host") {
		b.WriteString("      " + id("p_", n) + "[" + label(t.c4.Programme[n].Titel, t.c4.Programme[n].Text) + "]:::kern\n")
	}
	b.WriteString("    end\n    subgraph r0b[\"Ring 0b · Laibs Helfer, Container\"]\n")
	for _, n := range t.programs("helfer") {
		b.WriteString("      " + id("p_", n) + "[" + label(t.c4.Programme[n].Titel, t.c4.Programme[n].Text) + "]:::helfer\n")
	}
	b.WriteString("    end\n")
	for l := 0; l <= 2; l++ {
		if l > 0 {
			fmt.Fprintf(&b, "  subgraph l%d[\"%s\"]\n", l, layerNames[l])
		}
		for _, x := range t.tpls {
			if t.layer(x.name) == l {
				fmt.Fprintf(&b, "    %s[%s]:::s%d\n", id("t_", x.name), label(x.c.Title, x.c.Beschreibung), l)
			}
		}
		b.WriteString("  end\n")
	}
	for _, e := range edges(t) {
		b.WriteString("  " + e + "\n")
	}
	return b.String()
}

func (t *tree) programs(ring string) []string {
	var out []string
	for _, n := range sortedKeys(t.c4.Programme) {
		if t.c4.Programme[n].Ring == ring {
			out = append(out, n)
		}
	}
	return out
}

func edges(t *tree) []string {
	var out []string
	known := map[string]bool{}
	for _, x := range t.tpls {
		known[x.name] = true
	}
	for _, n := range t.programs("helfer") {
		out = append(out, id("p_", n)+" ==>|Tür| laib")
	}
	for _, n := range t.programs("host") {
		out = append(out, "laib --> "+id("p_", n))
	}
	for _, x := range t.tpls {
		from := id("t_", x.name)
		if slices.Contains(x.c.Needs, "mcp") {
			out = append(out, from+" ==>|Tür| laib")
		}
		seen := map[string]bool{x.name: true}
		add := func(to []string, arrow string) {
			for _, n := range to {
				if known[n] && !seen[n] {
					seen[n] = true
					out = append(out, from+" "+arrow+" "+id("t_", n))
				}
			}
		}
		add(x.c.After, "-->")
		add(x.c.Uses, "-.->")
		var inst []string
		for _, r := range x.c.Instances {
			inst = append(inst, r.Template)
		}
		add(inst, "-.->")
	}
	return out
}

// components is the level-3 diagram: the kernel's packages in ring 0a with
// their imports (without the ones another path implies), ring 0b's
// programs, and what ADR 0035 moves from one to the other.
func components(t *tree) string {
	var b strings.Builder
	b.WriteString("flowchart LR\n" + classes)
	k, _ := t.budget("kernel")
	fmt.Fprintf(&b, "  subgraph r0a[\"Ring 0a · der Kern, laib auf dem Host · %d %% von %s Zeilen\"]\n", k.lines*100/max(k.cap, 1), num(k.cap))
	for _, p := range t.pkgs {
		fmt.Fprintf(&b, "    %s[%s]:::kern\n", id("k_", p.path), label(t.c4.Kern[p.path].Titel, p.path+" · "+approx(p.lines)))
	}
	b.WriteString("  end\n  subgraph r0b[\"Ring 0b · Laibs Helfer: Container, eigenes Token, kein age-Schlüssel\"]\n")
	titles := map[string]string{}
	for _, n := range t.programs("helfer") {
		p, bud := t.c4.Programme[n], t.mustBudget(n)
		titles[p.Titel] = id("p_", n)
		fmt.Fprintf(&b, "    %s[%s]:::helfer\n", id("p_", n), label(p.Titel, fmt.Sprintf("über %d %% der Grenze %s", bud.lines*10/max(bud.cap, 1)*10, num(bud.cap))))
	}
	var moves []string
	for _, p := range t.pkgs {
		z := t.c4.Kern[p.path].Zieht
		if z == nil {
			continue
		}
		to, ok := titles[z.Nach]
		if !ok {
			to = id("z_", z.Nach)
			titles[z.Nach] = to
			fmt.Fprintf(&b, "    %s[%s]:::plan\n", to, label(z.Nach, "geplant, #"+z.Issue))
		}
		moves = append(moves, fmt.Sprintf("%s -.->|%s| %s", id("k_", p.path), label(z.Was+", #"+z.Issue), to))
	}
	b.WriteString("  end\n")
	for _, e := range reduce(t.pkgs) {
		fmt.Fprintf(&b, "  %s --> %s\n", id("k_", e[0]), id("k_", e[1]))
	}
	door := ""
	if _, ok := t.c4.Kern["internal/mcp"]; ok {
		door = id("k_", "internal/mcp")
	}
	for _, n := range t.programs("helfer") {
		if door != "" {
			fmt.Fprintf(&b, "  %s ==>|Tür| %s\n", id("p_", n), door)
		}
	}
	for _, m := range moves {
		b.WriteString("  " + m + "\n")
	}
	return b.String()
}

func (t *tree) mustBudget(n string) budget {
	b, _ := t.budget(n)
	return b
}

// reduce is the import graph without the edges another path implies.
func reduce(pkgs []pkg) [][2]string {
	deps := map[string][]string{}
	for _, p := range pkgs {
		deps[p.path] = p.imports
	}
	var out [][2]string
	for _, p := range pkgs {
		for _, d := range p.imports {
			if _, ok := deps[d]; ok && !reachableAround(deps, p.path, d) {
				out = append(out, [2]string{p.path, d})
			}
		}
	}
	return out
}

// reachableAround: to is reachable from from through some other import.
func reachableAround(deps map[string][]string, from, to string) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(n string) bool {
		if n == to {
			return true
		}
		if seen[n] {
			return false
		}
		seen[n] = true
		for _, d := range deps[n] {
			if walk(d) {
				return true
			}
		}
		return false
	}
	for _, d := range deps[from] {
		if d != to && walk(d) {
			return true
		}
	}
	return false
}

func c4page(t *tree) string {
	var b md
	b.WriteString("# Laib in C4\n\n<!-- erzeugt von scripts/c4.sh aus docs/c4.yml, templates/*/laib.yml, internal/, cmd/laib und scripts/loc.sh; ändere die Quellen, nicht diese Datei -->\n\n")
	b.p("%s", t.c4.Einleitung)
	b.p("## Ebene 1 · Umfeld: wer mit der Box spricht")
	b.mermaid(t.c4.Kontext.Mermaid)
	b.p("%s", t.c4.Kontext.Text)
	b.p("## Ebene 2 · Container: die Dienste der Box und wer wen braucht")
	b.p("Jede Vorlage in ihrer Schicht (`docs/c4.yml`), die Pfeile aus ihrer `laib.yml`: durchgezogen `after:` (startet nach), gestrichelt `uses:` und `instances:` (spricht mit), doppelt die Tür zu Laib (`needs: [mcp]`, Laibs Helfer). Abhängigkeiten zeigen nur nach unten (ADR 0035).")
	b.mermaid(containers(t))
	b.table([]string{"Dienst", "Schicht", "Was", "startet nach", "spricht mit"}, serviceRows(t))
	b.p("## Ebene 2b · Haushaltsdaten: wessen Daten, wer liest, wer schreibt")
	b.p("%s", t.c4.Daten.Text)
	b.mermaid(t.c4.Daten.Mermaid)
	b.p("Regeln, die diese Karte trägt (Einzelheiten in `docs/DATA.md`):")
	b.list(t.c4.Daten.Regeln)
	b.p("## Ebene 3 · Komponenten: Laibs Kern von innen")
	b.p("Die Pakete des Kerns mit ihren Importen (ohne die, die ein anderer Weg schon zeigt) und Laibs Helfer im Ring 0b; gestrichelt, was ADR 0035 aus dem Kern in den Ring 0b verlegt. Zeilen auf hundert gerundet, wie `scripts/loc.sh` zählt; genau stehen sie im README.")
	b.mermaid(components(t))
	b.table([]string{"Paket", "Teil", "Was", "Zeilen", "Zieht nach 0b"}, kernRows(t))
	b.p("## Ebene 4 · Fähigkeiten: was Laib kann")
	for _, g := range t.c4.Faehigkeiten {
		b.p("**%s**", g.Titel)
		b.list(g.Punkte)
	}
	b.p("## Ablauf · %s", t.c4.Ablauf.Titel)
	b.mermaid(t.c4.Ablauf.Mermaid)
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func serviceRows(t *tree) [][]string {
	var rows [][]string
	for _, x := range t.tpls {
		uses := append(append([]string{}, x.c.Uses...), instanceNames(x)...)
		sort.Strings(uses)
		rows = append(rows, []string{fmt.Sprintf("[%s](../templates/%s/) `%s`", x.c.Title, x.name, x.name), fmt.Sprint(t.layer(x.name)), x.c.Beschreibung, codes(x.c.After), codes(uses)})
	}
	return rows
}

func instanceNames(x tpl) []string {
	var out []string
	for _, r := range x.c.Instances {
		out = append(out, r.Template)
	}
	return out
}

func codes(xs []string) string {
	if len(xs) == 0 {
		return "—"
	}
	var out []string
	for _, x := range xs {
		out = append(out, "`"+x+"`")
	}
	return strings.Join(out, ", ")
}

func kernRows(t *tree) [][]string {
	var rows [][]string
	for _, p := range t.pkgs {
		k := t.c4.Kern[p.path]
		move := "—"
		if k.Zieht != nil {
			move = fmt.Sprintf("%s: %s ([#%s](https://github.com/%s/issues/%s))", k.Zieht.Nach, k.Zieht.Was, k.Zieht.Issue, strings.TrimPrefix(t.module, "github.com/"), k.Zieht.Issue)
		}
		rows = append(rows, []string{fmt.Sprintf("[`%s`](../%s/)", p.path, p.path), k.Titel, k.Text, approx(p.lines), move})
	}
	return rows
}
