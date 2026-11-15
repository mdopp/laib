package main

import (
	"fmt"
	"strings"
)

func (t *tree) standText(s stand) string {
	out := s.Status
	if s.Status == "Lücke" {
		out = "**Lücke**"
	}
	if s.Notiz != "" {
		out += ": " + s.Notiz
	}
	owner := strings.Split(t.module, "/")[1]
	var links []string
	for _, ref := range s.Issues {
		repo, n := issueRepo(t, ref)
		text := ref
		if !strings.Contains(ref, "#") {
			text = "#" + ref
		}
		links = append(links, fmt.Sprintf("[%s](https://github.com/%s/%s/issues/%s)", text, owner, repo, n))
	}
	if len(links) > 0 {
		out += " (" + strings.Join(links, ", ") + ")"
	}
	return out
}

func secpage(t *tree) string {
	var b md
	b.WriteString("# Sicherheitskonzept der Box\n\n<!-- erzeugt von scripts/c4.sh aus docs/sicherheit.yml, templates/, templates/hostnet.yml und internal/state; ändere die Quellen, nicht diese Datei -->\n\n")
	b.p("%s", t.sec.Einleitung)
	if n := len(t.sec.Grundsaetze); n == 1 {
		b.p("## Ein Grundsatz")
	} else {
		b.p("## %s Grundsätze", grundsatzZahl(n))
	}
	for i, g := range t.sec.Grundsaetze {
		fmt.Fprintf(&b, "%d. **%s** %s\n", i+1, g.Titel, g.Text)
	}
	b.WriteString("\n")
	b.p("## Zonen und Grenzen")
	b.mermaid(t.sec.Zonen.Mermaid)
	b.p("%s", t.sec.Zonen.Text)
	t.identities(&b)
	t.network(&b)
	t.data(&b)
	t.layerRules(&b)
	b.p("## Was passiert, wenn …")
	var rows [][]string
	for _, w := range t.sec.WasPassiert {
		rows = append(rows, []string{w.Fall, w.Folge})
	}
	b.table([]string{"Fall", "Was die Grenzen bewirken"}, rows)
	b.p("## Offene Punkte")
	for _, o := range t.sec.Offen {
		b.p("**%s** · %s", o.Titel, t.standText(o.Stand))
		b.list(o.Punkte)
	}
	if len(t.sec.Aenderungen) > 0 {
		b.p("## Bestätigte Änderungen ohne Folgen fürs Konzept")
		rows = nil
		for _, a := range t.sec.Aenderungen {
			rows = append(rows, []string{"`" + a.Fakt + "`", a.Grund})
		}
		b.table([]string{"Fakt", "Warum sich nichts ändert"}, rows)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func grundsatzZahl(n int) string {
	words := []string{"Keine", "Ein", "Zwei", "Drei", "Vier", "Fünf", "Sechs", "Sieben", "Acht", "Neun", "Zehn"}
	if n < len(words) {
		return words[n]
	}
	return fmt.Sprint(n)
}

func (t *tree) identities(b *md) {
	b.p("## Wer ist wer: Identitäten")
	var rows [][]string
	for _, x := range t.sec.Identitaeten {
		rows = append(rows, []string{x.Wer, x.Wie, x.Darf, t.standText(x.Stand)})
	}
	b.table([]string{"Wer", "Wie erkannt", "Darf", "Stand"}, rows)
	b.p("### Hände und Stufen")
	b.p("Aus `internal/state` (die `policy.yml`, die `laib init` schreibt, und die Hände, die im Code feststehen). Stufen von unten nach oben: %s; eine Hand darf bis zu ihrer Stufe, mit einer Verb-Liste nur diese Verben.", codes(t.policy.Tiers))
	rows = nil
	for _, h := range hands(t) {
		where := "policy.yml"
		if h.fixed {
			where = "fest im Code"
		}
		verbs := "alle bis zur Stufe"
		if len(h.Verbs) > 0 {
			verbs = codes(h.Verbs)
		}
		rows = append(rows, []string{"`" + h.name + "`", h.Ceiling, verbs, dash(codes(h.Paths)), dash(h.TTL), where})
	}
	b.table([]string{"Hand", "Stufe bis", "Verben", "Pfade", "Ablauf", "Quelle"}, rows)
	rows = nil
	for _, k := range sortedKeys(t.policy.Autonomy) {
		rows = append(rows, []string{"`" + k + "`", t.policy.Autonomy[k]})
	}
	b.p("Was die Box selbst anwendet (`autonomy:`, `auto`) und was ein Mensch freigibt (`proposal`); was die Inhaltsprüfung markiert (ein Skript auf der Box, eine öffentliche Adresse, `policy.yml`, ein Token, ein Host-Ordner), wartet immer auf einen Menschen:")
	b.table([]string{"Klasse", "Einstellung"}, rows)
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func (t *tree) network(b *md) {
	b.p("## Wo man herankommt: Netz und Türen")
	var rows [][]string
	for _, x := range t.sec.Netz {
		rows = append(rows, []string{x.Weg, x.Schutz, t.standText(x.Stand)})
	}
	b.table([]string{"Weg", "Schutz", "Stand"}, rows)
	b.p("### Adressen")
	b.p("Jede Route jeder Vorlage, aus `templates/*/laib.yml`. „Heimnetz“ heißt zu Hause oder über das WireGuard des Routers; „von außen“ ist eine Entscheidung des Besitzers, eine Karte je Adresse.")
	rows = nil
	for _, x := range t.tpls {
		for _, r := range x.c.Routes {
			host := r.Host + ".&lt;domain&gt;"
			if r.Host == "@" {
				host = "&lt;domain&gt;"
			}
			rows = append(rows, []string{host, "`" + x.name + "`", fmt.Sprint(t.layer(x.name)), reach(r.Exposure, r.PublicPaths), login(x, r.Auth, r.Outside, r.LoginProbe), extras(r.Group, r.Bypass)})
		}
	}
	b.table([]string{"Adresse", "Dienst", "Schicht", "Erreichbar", "Anmeldung", "Ausnahmen"}, rows)
	b.p("### Direkte Ports im Heimnetz")
	b.p("Ports, die ein Gerät zu Hause ohne Caddy erreicht (`ports:` mit `lan: true`); jeder andere veröffentlichte Port ist per nftables nur vom Host und den Containern erreichbar.")
	rows = nil
	for _, x := range t.tpls {
		for _, p := range x.c.Ports {
			if p.LAN {
				rows = append(rows, []string{"`" + x.name + "`", fmt.Sprint(p.Port), x.c.Title})
			}
		}
	}
	b.table([]string{"Dienst", "Port", "Titel"}, rows)
}

func reach(exposure string, public []string) string {
	s := "Heimnetz"
	if exposure == "public" {
		s = "von außen"
	}
	if len(public) > 0 {
		s += "; öffentlich: " + codes(public)
	}
	return s
}

func login(x tpl, auth, outside, probe string) string {
	switch {
	case auth == "one_factor" && outside == "two_factor":
		return "Authelia, ein Faktor, von außen zwei"
	case auth == "one_factor":
		return "Authelia, ein Faktor"
	case auth == "two_factor":
		return "Authelia, zwei Faktoren"
	case x.c.OIDC != nil:
		return "eigene Anmeldung (OIDC über Authelia)"
	case probe != "":
		return "eigene Anmeldung (geprüft an `" + probe + "`)"
	}
	return "keine"
}

func extras(group string, bypass []string) string {
	var out []string
	if group != "" {
		out = append(out, "nur Gruppe `"+group+"`")
	}
	if len(bypass) > 0 {
		out = append(out, "ohne Authelia: "+codes(bypass))
	}
	return dash(strings.Join(out, "; "))
}

var privacy = map[string]string{"sealed": "versiegelt: kein Verb sieht hinein", "approval": "Einblick nur nach Freigabe, eine Stunde"}

func (t *tree) data(b *md) {
	b.p("## Womit die Daten geschützt sind")
	var rows [][]string
	for _, x := range t.sec.Daten {
		rows = append(rows, []string{x.Daten, x.Liegen, x.Wer, t.standText(x.Stand)})
	}
	b.table([]string{"Daten", "Liegen", "Wer kommt heran", "Stand"}, rows)
	b.p("### Private Dienste")
	b.p("Aus `private:`, `oidc:` und `perPerson:` jeder `laib.yml` (ADR 0032; wer sich wo anmeldet: `docs/DATA.md`).")
	rows = nil
	for _, x := range t.tpls {
		if x.c.Private == "" && x.c.OIDC == nil && !x.c.PerPerson {
			continue
		}
		client := "—"
		if x.c.OIDC != nil {
			client = "`" + x.c.OIDC.ClientID + "`"
		}
		per := "—"
		if x.c.PerPerson {
			per = "je Person"
		}
		rows = append(rows, []string{"`" + x.name + "`", dash(privacy[string(x.c.Private)]), client, per})
	}
	b.table([]string{"Dienst", "Private Daten", "OIDC-Client", "Instanzen"}, rows)
	b.p("### Geheimnisse je Dienst")
	b.p("Nur die Namen, aus `secrets:` jeder `laib.yml`; die Werte liegen nur in `secrets.age` auf der Box. „Von außen“ heißt: ein anderer Dienst, Laib (ein Token für eine Hand) oder der Besitzer bringt es.")
	rows = nil
	for _, x := range t.tpls {
		if len(x.c.Secrets) == 0 {
			continue
		}
		var gen, ext []string
		for _, s := range x.c.Secrets {
			if !s.External {
				gen = append(gen, "`"+s.Name+"`")
				continue
			}
			ext = append(ext, "`"+s.Name+"`"+secretFrom(s.Mint, s.Owner != nil, s.Optional))
		}
		rows = append(rows, []string{"`" + x.name + "`", dash(strings.Join(gen, ", ")), dash(strings.Join(ext, ", "))})
	}
	b.table([]string{"Dienst", "erzeugt die Box", "von außen"}, rows)
}

func secretFrom(mint string, owner, optional bool) string {
	var out []string
	if mint != "" {
		out = append(out, "Token der Hand `"+mint+"`")
	}
	if owner {
		out = append(out, "trägt der Besitzer ein")
	}
	if optional {
		out = append(out, "kann fehlen")
	}
	if len(out) == 0 {
		return ""
	}
	return " (" + strings.Join(out, ", ") + ")"
}

func (t *tree) layerRules(b *md) {
	b.p("## Pro Schicht: was gilt")
	for _, g := range t.sec.Schichtregeln {
		b.p("**%s**", g.Titel)
		b.list(g.Punkte)
	}
	b.p("### Ausnahmen: Host-Netz und Label aus (#1166)")
	b.p("Jede Zeile `Network=host` und `SecurityLabelDisable=true` der Vorlagen, mit ihrem Grund aus `templates/hostnet.yml` (englisch, wie dort).")
	var rows [][]string
	for _, x := range t.tpls {
		for _, e := range exceptions(x) {
			kind := "Host-Netz"
			if e[1] == "labelling off" {
				kind = "Label aus"
			}
			rows = append(rows, []string{"`" + x.name + "/" + e[0] + "`", fmt.Sprint(t.layer(x.name)), kind, t.hostnet[x.name+"/"+e[0]][e[1]]})
		}
	}
	b.table([]string{"Unit", "Schicht", "Art", "Grund"}, rows)
	b.p("### Schicht 2 am Betriebssystem (#1245)")
	b.p("Was eine Vorlage der Schicht 2 heute noch an den Host bindet, gefunden in ihren Units: Host-Netz, Label aus, Capabilities, Geräte, fremde Ordner und Host-Pfade, Host-Units, die `podman exec` oder `podman run` aufrufen. ADR 0035 erlaubt nichts davon; die Liste steht hier, bis sie leer ist.")
	rows = nil
	count := map[string]int{}
	for _, x := range bindings(t) {
		count[x.tpl]++
		rows = append(rows, []string{"`" + x.tpl + "`", "`" + x.unit + "`", x.what})
	}
	var sum []string
	for _, n := range sortedKeys(count) {
		sum = append(sum, fmt.Sprintf("`%s` %d", n, count[n]))
	}
	if len(rows) == 0 {
		b.p("Keine: jede Vorlage der Schicht 2 erreicht die Box nur über Schnittstellen.")
		return
	}
	b.p("Je Vorlage: %s.", strings.Join(sum, ", "))
	b.table([]string{"Dienst", "Unit", "Bindung"}, rows)
}
