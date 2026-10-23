// Package review reads a change to the state repo the way a careful admin
// would before saying yes (#34): what it touches, in plain German, and
// whether only a human may allow it. It compares two checkouts — main and
// the change — by their files, their contracts and what they render to, and
// it lets through only what it knows to be harmless. Anything it does not
// know, and any state that does not load, goes to a human with a reason.
package review

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

// Result is what a change does that the autonomy dial alone may not decide.
type Result struct {
	Human []string // why only a human may allow the change, in plain German; empty = the dial decides
	Mints []string // hands a newly declared secret mints a Laib token for
	Added []string // services the change adds
	Facts []string // plain facts for the decision card (#58): where a new service is reachable …
	// Ask and Snapshot (#528): a change to a service with data a new image
	// or setting may migrate — a person decides, not the dial (a warning),
	// and laib snapshots these services before the approved change applies.
	Ask      []string
	Snapshot []string

	domain string // the box's domain, for the facts
}

type file struct {
	hash string
	exec bool // the executable bit, the only mode Git tracks (a checkout may be 0600, a worktree 0644)
	link bool
}

// tree lists every file under dir (not .git), keyed by its relative path.
func tree(dir string) (map[string]file, error) {
	out := map[string]file{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == ".git" { // a directory in a repo, a pointer file in a worktree
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		f := file{exec: info.Mode()&0o111 != 0}
		if info.Mode()&os.ModeSymlink != 0 {
			f.link = true
			target, _ := os.Readlink(p)
			f.hash = "link:" + target
		} else {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			f.hash = fmt.Sprintf("%x", sha256.Sum256(b))
		}
		out[filepath.ToSlash(rel)] = f
		return nil
	})
	return out, err
}

// data marks a change to a service with data (#528): "Zurücknehmen" with
// laib rollback brings the image back, the data before it come from the
// snapshot laib takes first (laib restore <Snapshot> <Dienst>).
func (r *Result) data(svc, why string) {
	if !slices.Contains(r.Snapshot, svc) {
		r.Ask = append(r.Ask, why+": vorher sichert Laib "+svc+"; Zurücknehmen mit laib rollback, die Daten von vorher mit laib restore")
		r.Snapshot = append(r.Snapshot, svc)
	}
}

// knownUnit says whether a file in a service folder is one Laib renders.
func knownUnit(name string) bool {
	switch filepath.Ext(name) {
	case ".container", ".pod", ".volume", ".network":
		return true
	}
	return false
}

var boxKeyReason = map[string]string{
	"roles":       "ändert die Rollen der Box (box.yml roles)",
	"hostNetwork": "ändert, welche Dienste im Netz der Box laufen (box.yml hostNetwork)",
	"laib":        "ändert Laibs eigene Seite (box.yml laib)",
	"backup":      "ändert die Sicherung (box.yml backup)",
	"tls":         "ändert die Zertifikate (box.yml tls)",
	"dns":         "ändert den DNS-Anbieter (box.yml dns)",
	"model":       "ändert das Modell (box.yml model)",
	"operator":    "ändert den Operator (box.yml operator)",
	"zuhause":     "ändert die Adresse des Haushalts (box.yml zuhause)",
	"people":      "ändert, wer im Haushalt wie erreicht wird und dem Boten schreiben darf (box.yml people)",
}

// Dirs compares a checkout of main (base) with a checkout of the change.
func Dirs(base, changed string) Result {
	var r Result
	say := func(format string, a ...any) { r.Human = append(r.Human, fmt.Sprintf(format, a...)) }

	before, err := tree(base)
	if err != nil {
		say("der bisherige Zustand ist nicht lesbar: %v", err)
		return r
	}
	after, err := tree(changed)
	if err != nil {
		say("die Änderung ist nicht lesbar: %v", err)
		return r
	}

	// --- files: what may be touched at all
	for _, p := range keyUnion(before, after) {
		b, inBase := before[p]
		a, inAfter := after[p]
		if inBase && inAfter && a.hash == b.hash && a.exec == b.exec {
			continue
		}
		if inAfter && a.link {
			say("%s ist ein Verweis (Symlink) und zeigt woandershin", p)
			continue
		}
		if !inAfter { // deleted: a whole service is judged below, a single file is reversible
			continue
		}
		parts := strings.Split(p, "/")
		switch {
		case len(parts) == 1:
			switch p {
			case "policy.yml":
				say("ändert die Freigaberegeln (policy.yml)")
			case "secrets.age":
				say("ändert die verschlüsselten Geheimnisse (secrets.age)")
			case "ignore.yml":
				say("ändert, was Laib nicht überwacht (ignore.yml)")
			case "box.yml":
				for _, k := range changedBoxKeys(filepath.Join(base, p), filepath.Join(changed, p)) {
					if reason, ok := boxKeyReason[k]; ok {
						say("%s", reason)
					} else {
						say("ändert box.yml (%s)", k)
					}
				}
			default:
				say("ändert eine Datei außerhalb der Dienste: %s", p)
			}
		case parts[0] == "services" && len(parts) == 3:
			svc, name := parts[1], parts[2]
			switch {
			case name == "post-deploy":
				say("führt ein Skript direkt auf der Box aus (services/%s/post-deploy)", svc)
			case strings.HasSuffix(name, ".service") || strings.HasSuffix(name, ".timer"):
				say("%s läuft direkt auf der Box, nicht im Container (Host-Unit)", p)
			case a.exec:
				say("%s ist ausführbar", p)
			case name == "laib.yml", name == "values.yml", name == "README.md", knownUnit(name):
				// judged by content below
			default:
				say("ändert eine Datei, die Laib nicht kennt: %s", p)
			}
		case parts[0] == "services" && len(parts) == 4 && parts[2] == state.BaseDir && !a.exec:
			// the template as copied in (#511): only the next update's merge reads it
		case parts[0] == "repairs" && len(parts) == 2:
			say("eine Reparatur (%s): nach der Freigabe führt Laib den darin genannten Befehl aus", p)
		case p == "host/image":
			say("das Betriebssystem der Box (%s): nach der Freigabe wird das Image bereitgestellt, der Neustart kommt im Update-Fenster (#368)", p)
		case parts[0] == "wishes" && len(parts) == 2:
			say("ein Wunsch an die Werkstatt (%s): nach der Freigabe wird daraus ein Ticket", p)
		case parts[0] == "lessons" && len(parts) == 2:
			say("eine Lehre für den Operator (%s): steht danach im Kontext jeder seiner Sitzungen (#544)", p)
		case parts[0] == "skills" && len(parts) == 2:
			say("gibt dem Operator eine Fähigkeit oder ändert eine (%s): was er tun darf, entscheidet ein Mensch (ADR 0023)", p)
		default:
			say("ändert eine Datei, die Laib nicht kennt: %s", p)
		}
	}

	// --- the states: everything below needs both to load
	bst, err := state.Load(base)
	if err != nil {
		say("der bisherige Zustand lädt nicht: %v", err)
		return r
	}
	ast, err := state.Load(changed)
	if err == nil {
		r.domain = ast.Box.Domain
	}
	if err != nil {
		say("%s", loadReason(err))
		return r
	}
	for name := range bst.Services {
		if _, ok := ast.Services[name]; !ok {
			say("entfernt den Dienst %s", name)
		}
	}
	mints := map[string]bool{}
	for _, name := range ast.ServiceNames() {
		a := ast.Services[name]
		b, existed := bst.Services[name]
		if !existed {
			r.Added = append(r.Added, name)
		}
		var bc state.Contract
		if existed {
			bc = b.Contract
		}
		r.contract(name, bc, a.Contract, existed, mints)
		r.values(name, ast, b, a, existed)
		if existed && !maps.Equal(b.Values, a.Values) && slices.ContainsFunc(a.Contract.Data, func(d state.DataPath) bool { return d.Quiesce }) {
			r.data(name, "ändert die Werte (ein Image, eine Einstellung) von "+name+", einem Dienst mit Datenbank")
		}
	}
	for h := range mints {
		r.Mints = append(r.Mints, h)
	}
	sort.Strings(r.Mints)
	sort.Strings(r.Added)

	// --- what it renders to: the units systemd will run
	bout, err := render.Render(bst, nil)
	if err != nil {
		say("der bisherige Zustand rendert nicht: %v", err)
		return r
	}
	aout, err := render.Render(ast, nil)
	if err != nil {
		say("die Änderung rendert nicht: %v", err)
		return r
	}
	before2 := len(r.Human)
	for key, text := range aout {
		if !strings.HasPrefix(key, "quadlet/") || strings.HasSuffix(key, ".laib") { // values and hook hashes are judged above, not as a unit
			continue
		}
		svc := strings.Split(key, "/")[1]
		added, removed := diffUnits(parseUnit(bout[key]), parseUnit(text))
		_, existedUnit := bout[key]
		r.units(svc, key, ast, added, removed, existedUnit, !slices.Contains(r.Added, svc), strings.Contains(text, "\nAutoUpdate=registry"))
	}
	// rendered platform files (Caddy, Authelia, AdGuard, nftables, host units)
	// that changed without a reason above: not understood, so a human
	if len(r.Human) == before2 {
		for key, text := range aout {
			if strings.HasPrefix(key, "quadlet/") {
				continue
			}
			if bout[key] != text {
				say("ändert %s, ohne dass Laib den Grund erkennt", key)
			}
		}
	}
	return r
}

// loadReason turns a loader error into the plain reason a human reads. The
// loader refuses some of the very things a human must see (a foreign secret,
// hostNetwork without Network=host); the reason names them before the error.
func loadReason(err error) string {
	msg := err.Error()
	if _, after, ok := strings.Cut(msg, "undeclared secret "); ok {
		name := strings.Fields(after)[0]
		return fmt.Sprintf("nutzt das Geheimnis %s, das dem Dienst nicht gehört (%s)", name, msg)
	}
	if strings.Contains(msg, "hostNetwork") {
		return fmt.Sprintf("läuft im Netz der Box (hostNetwork) — und die Änderung lädt nicht: %s", msg)
	}
	return "die Änderung lädt nicht: " + msg
}

// changedBoxKeys lists the top-level keys of box.yml whose value differs.
func changedBoxKeys(basePath, changedPath string) []string {
	load := func(p string) map[string]any {
		m := map[string]any{}
		b, _ := os.ReadFile(p)
		yaml.Unmarshal(b, &m)
		return m
	}
	b, a := load(basePath), load(changedPath)
	var out []string
	for _, k := range keyUnion(b, a) {
		bb, _ := yaml.Marshal(b[k])
		ab, _ := yaml.Marshal(a[k])
		if !bytes.Equal(bb, ab) {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		out = append(out, "(unverändert im Inhalt, anders geschrieben)")
	}
	return out
}

func authRank(a string) int {
	switch a {
	case "none":
		return 0
	case "one_factor":
		return 1
	case "two_factor":
		return 2
	}
	return 1
}

// contract judges a service's laib.yml against its previous one.
func (r *Result) contract(svc string, b, a state.Contract, existed bool, mints map[string]bool) {
	say := func(format string, x ...any) { r.Human = append(r.Human, fmt.Sprintf(format, x...)) }
	// routes by host
	broutes := map[string]state.Route{}
	for _, rt := range b.Routes {
		broutes[rt.Host] = rt
	}
	seen := map[string]bool{}
	for _, rt := range a.Routes {
		seen[rt.Host] = true
		old, had := broutes[rt.Host]
		if !had {
			if existed {
				say("veröffentlicht eine neue Adresse für %s: %s", svc, rt.Host)
			}
			where := "nur im Heimnetz erreichbar"
			if rt.Exposure == "public" {
				where = "aus dem Internet erreichbar"
			}
			login := ""
			if rt.Auth == "none" {
				login = ", mit eigener Anmeldung"
			}
			r.Facts = append(r.Facts, fmt.Sprintf("%s ist unter %s.%s %s%s", svc, rt.Host, r.domain, where, login))
			if rt.Exposure == "public" {
				say("macht %s aus dem Internet erreichbar (%s)", svc, rt.Host)
			}
			if rt.Auth == "none" {
				say("%s ist ohne Anmeldung erreichbar (%s)", svc, rt.Host)
			}
			if len(rt.Bypass) > 0 {
				say("%s hat Pfade ohne Anmeldung: %s", rt.Host, strings.Join(rt.Bypass, ", "))
			}
			if rt.Caddy != "" {
				say("%s bringt eigene Caddy-Konfiguration mit", rt.Host)
			}
			continue
		}
		if old.Exposure != "public" && rt.Exposure == "public" {
			say("macht %s aus dem Internet erreichbar (%s)", svc, rt.Host)
		}
		if authRank(rt.Auth) < authRank(old.Auth) {
			say("schwächt die Anmeldung für %s (%s → %s)", rt.Host, old.Auth, rt.Auth)
		}
		if old.Group != rt.Group {
			say("ändert, wer sich bei %s anmelden darf (%q → %q)", rt.Host, old.Group, rt.Group)
		}
		if strings.Join(rt.Bypass, ",") != strings.Join(old.Bypass, ",") && len(rt.Bypass) > 0 {
			say("%s bekommt Pfade ohne Anmeldung: %s", rt.Host, strings.Join(rt.Bypass, ", "))
		}
		if old.Caddy != rt.Caddy {
			say("ändert die Caddy-Konfiguration von %s von Hand", rt.Host)
		}
		if old.Port != rt.Port {
			say("ändert, wohin %s verbindet (Port %d → %d)", rt.Host, old.Port, rt.Port)
		}
	}
	for host := range broutes {
		if !seen[host] {
			say("entfernt die Adresse %s von %s", host, svc)
		}
	}
	// secrets
	bsec := map[string]state.Secret{}
	for _, s := range b.Secrets {
		bsec[s.Name] = s
	}
	for _, s := range a.Secrets {
		old, had := bsec[s.Name]
		if had && old == s {
			continue
		}
		if s.Mint != "" {
			mints[s.Mint] = true
			say("legt einen Laib-Schlüssel für die Hand %s an (%s in %s)", s.Mint, s.Name, svc)
		} else if s.External {
			say("erwartet ein Geheimnis von außen (%s in %s)", s.Name, svc)
		}
	}
	// oidc
	if fmt.Sprint(b.OIDC) != fmt.Sprint(a.OIDC) && (b.OIDC != nil || a.OIDC != nil) {
		say("ändert die Anmeldung über OIDC für %s", svc)
	}
	// data: removed from the state or from the backup
	bdata := map[string]*bool{}
	for _, d := range b.Data {
		bdata[d.Path] = d.Backup
	}
	for _, d := range a.Data {
		old, had := bdata[d.Path]
		wasBacked := !had || old == nil || *old
		if existed && had && wasBacked && d.Backup != nil && !*d.Backup {
			say("nimmt %s von %s aus der Sicherung", d.Path, svc)
		}
		delete(bdata, d.Path)
	}
	for p := range bdata {
		say("entfernt den Datenpfad %s von %s (nicht mehr gesichert)", p, svc)
	}
	// ports to the LAN
	bports := map[int]bool{}
	for _, p := range b.Ports {
		bports[p.Port] = p.LAN
	}
	for _, p := range a.Ports {
		if p.LAN && !bports[p.Port] {
			say("öffnet Port %d von %s ins Heimnetz", p.Port, svc)
		}
	}
	if !b.HostNetwork && a.HostNetwork {
		say("%s läuft im Netz der Box (hostNetwork)", svc)
	}
	if !b.GPU && a.GPU {
		say("%s nimmt die GPU", svc)
	}
	if existed && strings.Join(b.Provides, ",") != strings.Join(a.Provides, ",") {
		say("ändert, wofür %s zuständig ist (provides)", svc)
	}
}

// values: a `!secret` the service does not own is a human's call.
func (r *Result) values(svc string, ast *state.State, b, a *state.Service, existed bool) {
	owned := map[string]bool{}
	for _, s := range a.Contract.Secrets {
		owned[s.Name] = true
	}
	if a.Contract.OIDC != nil {
		owned[state.OIDCSecretName(a.Contract.OIDC.ClientID)] = true
	}
	if ast.Box.Model.KeySecret != "" {
		owned[ast.Box.Model.KeySecret] = true
	}
	for k, v := range a.Values {
		if v.Secret == "" || owned[v.Secret] {
			continue
		}
		if existed {
			if old, ok := b.Values[k]; ok && old.Secret == v.Secret {
				continue // was there before: not this change's doing
			}
		}
		r.Human = append(r.Human, fmt.Sprintf("%s nutzt das Geheimnis %s, das ihm nicht gehört (values.yml %s)", svc, v.Secret, k))
	}
}

var restrictive = map[string]bool{"ReadOnly": true, "DropCapability": true, "NoNewPrivileges": true, "ReadOnlyTmpfs": true, "SecurityLabelNested": true}

// harmless keys per section: added or changed values stay with the dial
var harmless = map[string]map[string]bool{
	"Unit":    set("Description", "After", "Before", "Wants", "Requires", "Requisite", "BindsTo", "PartOf", "Documentation", "StartLimitIntervalSec", "StartLimitBurst", "ConditionPathExists"),
	"Install": set("WantedBy", "RequiredBy", "Also", "Alias"),
	"Service": set("Restart", "RestartSec", "TimeoutStartSec", "TimeoutStopSec", "TimeoutSec", "KillMode", "KillSignal", "StandardOutput", "StandardError", "SyslogIdentifier", "Type", "Delegate", "OOMScoreAdjust"),
	"Container": set("Environment", "Label", "Annotation", "Exec", "Entrypoint", "AutoUpdate", "Notify", "Timezone", "WorkingDir", "HealthCmd", "HealthInterval", "HealthRetries", "HealthStartPeriod", "HealthTimeout", "HealthOnFailure", "HealthStartupCmd", "HealthStartupInterval", "HealthStartupRetries", "HealthStartupSuccess", "HealthStartupTimeout", "HealthLogDestination", "HealthMaxLogCount", "HealthMaxLogSize",
		"Pull", "LogDriver", "LogOpt", "StopTimeout", "StopSignal", "Tmpfs", "ContainerName", "HostName", "IP", "IP6", "DNS", "DNSOption", "DNSSearch", "Retry", "RetryTime", "ReloadCmd", "ReloadSignal", "RunInit", "ShmSize", "PidsLimit", "Pod", "Secret", "Memory", "ExposeHostPort"),
	"Pod":     set("PodName", "Label", "DNS", "DNSOption", "DNSSearch", "AddHost", "HostName", "IP", "IP6", "ShmSize", "ServiceName"),
	"Volume":  set("Label", "VolumeName", "User", "Group", "Copy", "Type"),
	"Network": set("Label", "NetworkName", "Subnet", "Gateway", "IPRange", "Internal", "IPv6", "DisableDNS", "DNS"),
}

func set(keys ...string) map[string]bool {
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	return m
}

var commandKeys = set("ExecStart", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload", "ExecCondition")
var privilegeKeys = set("PodmanArgs", "GlobalArgs", "AddCapability", "AddDevice", "SecurityLabelDisable", "SecurityLabelType", "SecurityLabelLevel", "SecurityLabelFileType", "SeccompProfile", "UserNS", "Sysctl", "Mask", "Unmask", "Ulimit", "CgroupsMode", "User", "Group", "GIDMap", "UIDMap", "SubGIDMap", "SubUIDMap", "Rootfs", "EnvironmentHost", "EnvironmentFile", "ContainersConfModule", "Module", "Driver", "Device", "Options", "Yaml", "KubeDownForce")

// units judges the entries added to and removed from one rendered unit.
// auto: the unit carries AutoUpdate=registry, no data a new image migrates (#528).
func (r *Result) units(svc, key string, st *state.State, added, removed []entry, existedUnit, existedService, auto bool) {
	say := func(format string, x ...any) { r.Human = append(r.Human, fmt.Sprintf(format, x...)) }
	unit := strings.TrimPrefix(key, "quadlet/")
	var dataDirs []string
	if s, ok := st.Services[svc]; ok {
		for _, d := range s.Contract.Data {
			dataDirs = append(dataDirs, strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir))
		}
	}
	oldImage := ""
	for _, e := range removed {
		if e.Key == "Image" {
			oldImage = e.Value
		}
		if restrictive[e.Key] {
			say("nimmt eine Einschränkung weg: %s=%s in %s", e.Key, e.Value, unit)
		}
	}
	for _, e := range added {
		switch {
		case commandKeys[e.Key]:
			say("führt einen Befehl direkt auf der Box aus (%s in %s)", e.Key, unit)
		case e.Section == "Service", e.Section == "Unit", e.Section == "Install":
			if !harmless[e.Section][e.Key] {
				say("ändert %s=%s in %s, was Laib nicht einschätzen kann", e.Key, e.Value, unit)
			}
		case e.Key == "Image":
			if existedUnit && imageRepo(e.Value) != imageRepo(oldImage) {
				say("startet ein anderes Programm in %s (%s statt %s)", svc, e.Value, oldImage)
			} else if existedUnit && !auto {
				r.data(svc, fmt.Sprintf("ein neues Image für %s (%s → %s), dessen Daten es migrieren kann", svc, oldImage, e.Value))
			} else if !existedUnit && existedService {
				say("startet ein neues Programm in %s (%s)", svc, e.Value)
			}
		case e.Key == "Volume":
			src, _, _ := strings.Cut(e.Value, ":")
			if strings.HasPrefix(src, "/") || strings.HasPrefix(src, "%") || strings.HasPrefix(src, "~") || strings.HasPrefix(src, ".") {
				inData := false
				for _, d := range dataDirs {
					if src == d || strings.HasPrefix(src, strings.TrimSuffix(d, "/")+"/") {
						inData = true
					}
				}
				if !inData {
					say("Zugriff auf den Ordner %s der Box aus %s", src, unit)
				}
			}
		case e.Key == "Mount":
			say("Zugriff auf einen Ordner der Box (Mount=%s in %s)", e.Value, unit)
		case e.Key == "PublishPort":
			if ip, port := publishHost(e.Value); !loopback(ip) {
				say("öffnet den Port %s von %s für andere Rechner (%s)", port, svc, e.Value)
			}
		case e.Key == "Network":
			if e.Value == "host" {
				say("%s läuft im Netz der Box (Network=host in %s)", svc, unit)
			} else if !strings.HasSuffix(e.Value, ".network") {
				say("ändert das Netz von %s (Network=%s)", svc, e.Value)
			}
		case privilegeKeys[e.Key]:
			say("gibt %s Sonderrechte (%s=%s in %s)", svc, e.Key, e.Value, unit)
		case restrictive[e.Key]:
			// a restriction added: fine
		case harmless[e.Section][e.Key]:
			// fine
		default:
			say("ändert %s=%s in %s, was Laib nicht einschätzen kann", e.Key, e.Value, unit)
		}
	}
}

// keyUnion lists the keys of both maps, sorted, each once.
func keyUnion[V any](a, b map[string]V) []string {
	keys := slices.AppendSeq(slices.Collect(maps.Keys(a)), maps.Keys(b))
	slices.Sort(keys)
	return slices.Compact(keys)
}
