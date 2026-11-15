package main

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// A fact is one security-relevant line the code says: a route, a LAN port,
// a hand, a secret between services, a host-network or label-off line, a
// service's layer, a layer-2 tie to the OS. The gate compares the PR's
// facts with main's; key names a fact, val is what it is now.
type fact struct{ kind, key, val string }

func facts(t *tree) []fact {
	var out []fact
	for _, x := range t.tpls {
		lay := "?"
		if l := t.layer(x.name); l >= 0 {
			lay = fmt.Sprint(l)
		}
		out = append(out, fact{"dienst", "dienst " + x.name, "schicht " + lay})
		for _, r := range x.c.Routes {
			val := fmt.Sprintf("%s auth=%s outside=%s group=%s bypass=%s public=%s", r.Exposure, orNone(r.Auth), r.Outside, r.Group,
				strings.Join(r.Bypass, ","), strings.Join(r.PublicPaths, ","))
			out = append(out, fact{"route", "route " + x.name + " " + r.Host, val})
		}
		for _, p := range x.c.Ports {
			if p.LAN {
				out = append(out, fact{"port", fmt.Sprintf("port %s %d", x.name, p.Port), "lan"})
			}
		}
		for _, s := range x.c.Secrets {
			if s.External {
				out = append(out, fact{"secret", "secret " + x.name + " " + s.Name, ""})
			}
		}
		for _, e := range exceptions(x) {
			out = append(out, fact{"ausnahme", "ausnahme " + x.name + "/" + e[0] + " " + e[1], ""})
		}
	}
	for _, h := range hands(t) {
		out = append(out, fact{"hand", "hand " + h.name, fmt.Sprintf("ceiling=%s verbs=%s paths=%s ttl=%s session=%s",
			h.Ceiling, strings.Join(h.Verbs, ","), strings.Join(h.Paths, ","), h.TTL, h.Session)})
	}
	for _, b := range bindings(t) {
		out = append(out, fact{"bindung", "bindung " + b.tpl + " " + b.unit + " " + b.what, ""})
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// exceptions are a template's #1166 lines: [unit, "host network" | "labelling off"].
func exceptions(x tpl) [][2]string {
	var out [][2]string
	for _, u := range x.units {
		for _, l := range strings.Split(u.text, "\n") {
			switch strings.TrimSpace(l) {
			case "Network=host":
				out = append(out, [2]string{u.file, "host network"})
			case "SecurityLabelDisable=true":
				out = append(out, [2]string{u.file, "labelling off"})
			}
		}
	}
	return out
}

type binding struct{ tpl, unit, what string }

var volume = regexp.MustCompile(`^Volume=([^:]+):`)

// bindings scans every layer-2 template for what ADR 0035 forbids it (the
// list of #1245): host network, labelling off, a capability, a device, a
// folder that is not its own, a host path, a host unit that runs podman.
func bindings(t *tree) []binding {
	var out []binding
	for _, x := range t.tpls {
		if t.layer(x.name) != 2 {
			continue
		}
		for _, u := range x.units {
			for _, w := range unitTies(x.name, u) {
				out = append(out, binding{x.name, u.file, w})
			}
		}
	}
	return out
}

func unitTies(name string, u unit) []string {
	var out []string
	own := strings.TrimSuffix(u.file, path.Ext(u.file))
	hostUnit := false
	for _, l := range strings.Split(u.text, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "Network=host":
			out = append(out, "Host-Netz")
		case l == "SecurityLabelDisable=true":
			out = append(out, "Label aus")
		case strings.HasPrefix(l, "AddCapability="):
			out = append(out, "Capability "+strings.TrimPrefix(l, "AddCapability="))
		case strings.HasPrefix(l, "AddDevice="):
			out = append(out, "Gerät `"+strings.TrimPrefix(l, "AddDevice=")+"`")
		case strings.HasPrefix(l, "ExecStart") && path.Ext(u.file) == ".service" && (strings.Contains(l, "podman exec") || strings.Contains(l, "podman run")):
			hostUnit = true
		}
		if m := volume.FindStringSubmatch(l); m != nil {
			if w := foreign(m[1], name, own); w != "" {
				out = append(out, w)
			}
		}
	}
	if hostUnit {
		out = append(out, "Host-Unit mit podman")
	}
	return out
}

// foreign says what a mount source is when it is not the template's own:
// ${DATA_DIR}/<name>[/…], ${DATA_DIR}/${SERVICE}, ${DATA_DIR}/<unit> and the
// template's own files (./…) are its own.
func foreign(src, name, unitName string) string {
	rest, ok := strings.CutPrefix(src, "${DATA_DIR}/")
	if !ok {
		if strings.HasPrefix(src, "/") {
			return "Host-Pfad `" + src + "`"
		}
		return ""
	}
	first := strings.SplitN(rest, "/", 2)[0]
	if first == name || first == "${SERVICE}" || first == unitName {
		return ""
	}
	return "fremder Ordner `" + src + "`"
}

type namedHand struct {
	name  string
	fixed bool
	Hand
}

// Hand is state.Hand's fields the pages show.
type Hand struct {
	Ceiling, TTL, Session string
	Verbs, Paths          []string
}

func hands(t *tree) []namedHand {
	var out []namedHand
	for _, n := range sortedKeys(t.policy.Hands) {
		h := t.policy.Hands[n]
		out = append(out, namedHand{n, false, Hand{h.Ceiling, h.TTL, h.Session, h.Verbs, h.Paths}})
	}
	for _, n := range sortedKeys(t.fixed) {
		if _, ok := t.policy.Hands[n]; ok {
			continue
		}
		h := t.fixed[n]
		out = append(out, namedHand{n, true, Hand{h.Ceiling, h.TTL, h.Session, h.Verbs, h.Paths}})
	}
	return out
}

// flagged is the gate's rule: which new or changed facts need a judgment.
func flagged(f fact, old string, existed bool) bool {
	switch {
	case !existed && f.kind == "route":
		return strings.HasPrefix(f.val, "public") || strings.Contains(f.val, "auth=none")
	case !existed:
		return true
	default:
		return old != f.val
	}
}

type change struct {
	f        fact
	old      string
	existed  bool
	resolved bool
}

var kindWords = map[string]string{
	"route":    "Adresse und Anmeldung",
	"port":     "direkter Port im Heimnetz",
	"hand":     "Hand oder Stufe",
	"secret":   "Geheimnis zwischen Diensten",
	"ausnahme": "Host-Netz- oder Label-Ausnahme",
	"dienst":   "Dienst oder Schicht",
	"bindung":  "Bindung der Schicht 2 ans Betriebssystem",
}

// changes are the flagged facts of head against base, sorted by key.
func changes(head, base *tree) []change {
	old := map[string]string{}
	for _, f := range facts(base) {
		old[f.key] = f.val
	}
	var out []change
	for _, f := range facts(head) {
		o, ok := old[f.key]
		if flagged(f, o, ok) {
			out = append(out, change{f: f, old: o, existed: ok})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].f.key < out[j].f.key })
	return out
}

func (c change) String() string {
	s := "neu"
	if c.existed {
		s = "geändert"
	}
	line := fmt.Sprintf("%s: %s `%s`", s, kindWords[c.f.kind], c.f.key)
	if c.f.val != "" {
		line += " (" + c.f.val + ")"
	}
	if c.existed {
		line += ", vorher (" + c.old + ")"
	}
	return line
}
