// Package render turns a loaded state into the files that go on the box.
// Pure: state in, files out. Golden-tested (docs/TESTING.md).
package render

import (
	"cmp"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/pbkdf2"

	"github.com/mdopp/laib/internal/state"
)

// Output maps a relative path to file content. Paths:
//
//	quadlet/<service>/<file>      → ~/.config/containers/systemd/<service>/<file>
//	caddy/Caddyfile               → /mnt/data/laib/render/caddy/Caddyfile
//	authelia/laib-access.yml      → /mnt/data/laib/render/authelia/…
//	authelia/laib-clients.yml
//	adguard/rewrites.json
//	nftables/laib.nft
type Output map[string]string

// Placeholder is what a secret renders as when no secret values are given
// (lint, review) and what Redact turns a real value back into.
func Placeholder(name string) string { return "${secret:" + name + "}" }

// Render produces every file. secrets may be nil: then secret references
// render as placeholders, which is what `laib template lint` and a review
// see. Plan and apply pass the real values; an unset one renders empty.
func Render(st *state.State, secrets map[string]string) (Output, error) {
	out := Output{}
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		if unmet := svc.Unmet(secrets); secrets != nil && len(unmet) > 0 {
			return nil, fmt.Errorf("services/%s: %s", name, strings.Join(unmet, "; ")) // before anything is written (#184)
		}
		vars := variables(st, svc, secrets)
		for file, content := range svc.Units {
			if err := secretOffEnvironment(svc, file, content); err != nil {
				return nil, err
			}
			switch strings.TrimPrefix(filepath.Ext(file), ".") {
			case "service", "timer":
				out["systemd/"+file] = substitute(content, vars) // a plain host unit next to laib's own
			default:
				unit, _, err := secretEnv(svc, file, content, vars)
				if err != nil {
					return nil, err
				}
				out["quadlet/"+name+"/"+file] = unit
			}
		}
		for file, content := range svc.Assets {
			out["quadlet/"+name+"/"+file] = content
		}
		out["quadlet/"+name+"/"+ValuesFile] = values(name, svc)
		if len(svc.Hooks) > 0 {
			out["quadlet/"+name+"/"+HooksFile] = hooks(name, svc)
		}
	}
	if uses(st, "PERSONA") {
		// the packages a service mounts from /mnt/data/laib/render/persona (ADR 0020)
		for pname, p := range st.Personas {
			for file, content := range p.Files {
				out["persona/"+pname+"/"+file] = content
			}
			out["persona/"+pname+"/persona.json"] = p.JSON()
		}
		out["persona/chat.yml"] = st.ChatMap()
	}
	if uses(st, "PEOPLE_REV") {
		// box.yml people for bote-notify (ADR 0022 §3), mounted as a folder so a rewrite is seen
		out["people/people.json"] = st.PeopleJSON()
	}
	if st.Box.Roles.Proxy != "" {
		out["caddy/Caddyfile"] = caddyfile(st, secrets)
	}
	if st.Box.Roles.SSO != "" {
		out["authelia/laib-access.yml"] = autheliaAccess(st)
		out["authelia/laib-clients.yml"] = autheliaClients(st, secrets)
	}
	if st.Box.Roles.DNS != "" && st.Box.LanIP != "" {
		out["adguard/rewrites.json"] = adguardRewrites(st)
	}
	if nft := nftables(st); nft != "" {
		out["nftables/laib.nft"] = nft
	}
	if st.Box.Backup.Repo != "" {
		out["systemd/laib-backup.service"] = backupService()
		out["systemd/laib-backup.timer"] = backupTimer(st.Box.Backup.Schedule)
		// the monthly restore test (#40): restic check on a sample plus one path restored and compared
		out["systemd/laib-backup-check.service"] = "# Rendered by laib: the monthly restore test (laib backup check).\n[Unit]\nDescription=Laib backup check\n\n[Service]\nType=oneshot\nExecStart=/var/opt/laib/laib backup check\nEnvironment=LAIB_HOME=/mnt/data/laib\nEnvironment=LAIB_ACTOR=laib\n"
		out["systemd/laib-backup-check.timer"] = "# Rendered by laib: once a month, a restore test.\n[Unit]\nDescription=Laib backup check timer\n\n[Timer]\nOnCalendar=monthly\nPersistent=true\nRandomizedDelaySec=1h\n\n[Install]\nWantedBy=timers.target\n"
	}
	// Laib's own programs in their containers (ADR 0027)
	if st.Box.Operator.Enabled {
		out["quadlet/laib-operator/laib-operator.container"], _ = operatorUnit(st, secrets)
	}
	if notifier(st) {
		out["quadlet/laib-notify/laib-notify.container"], _ = notifyUnit(st, secrets)
		out["systemd/laib-notify.timer"] = "# Rendered by laib: the watchdog ping and the owner's mail every five minutes (#443).\n[Unit]\nDescription=Laib notify timer\n\n[Timer]\nOnBootSec=2m\nOnUnitActiveSec=5m\n\n[Install]\nWantedBy=timers.target\n"
	}
	if st.Policy.Autonomy["converge"] == "on" {
		out["systemd/laib-converge.service"] = "# Rendered by laib: the converge loop (docs/DESIGN.md Repair).\n[Unit]\nDescription=Laib converge\n\n[Service]\nType=oneshot\nExecStart=/var/opt/laib/laib converge\nEnvironment=LAIB_HOME=/mnt/data/laib\nEnvironment=LAIB_ACTOR=laib\n"
		out["systemd/laib-converge.timer"] = "# Rendered by laib: every ten minutes, and after boot.\n[Unit]\nDescription=Laib converge timer\n\n[Timer]\nOnBootSec=2m\nOnUnitActiveSec=10m\n\n[Install]\nWantedBy=timers.target\n"
	}
	if v := st.Policy.Autonomy["template-update"]; v != "" && len(st.Box.Registries) > 0 {
		out["systemd/laib-template-sync.service"] = "# Rendered by laib: pull registries and propose template updates.\n[Unit]\nDescription=Laib template sync\n\n[Service]\nType=oneshot\nExecStart=/var/opt/laib/laib template sync --propose\nEnvironment=LAIB_HOME=/mnt/data/laib\nEnvironment=LAIB_ACTOR=laib\n"
		out["systemd/laib-template-sync.timer"] = "# Rendered by laib: weekly, inside the update window's day when one is set.\n[Unit]\nDescription=Laib template sync timer\n\n[Timer]\nOnCalendar=Sat *-*-* 04:00:00\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
	}
	// #528: the images of the services without data, nightly after the
	// backup (box.yml updates: days and time; default every night, 90 minutes
	// after backup.schedule). It replaces podman auto-update, whose timer
	// stays masked (ADR 0002 amendment).
	bt, _ := time.Parse("15:04", cmp.Or(st.Box.Backup.Schedule, "03:00"))
	at := strings.TrimSpace(strings.Join(st.Box.Updates.Days, ",") + " *-*-* " + cmp.Or(st.Box.Updates.Time, bt.Add(90*time.Minute).Format("15:04")) + ":00")
	// #547: Laib itself first (box.yml updates.laib); a failed self-update never stops the image run
	out["systemd/laib-images.service"] = "# Rendered by laib (#528, #547): Laib's own update, then the images laib keeps current, verify, the previous one back on red.\n[Unit]\nDescription=Laib images\n\n[Service]\nType=oneshot\nExecStartPre=-/bin/bash /var/opt/laib/self-update.sh\nExecStart=/var/opt/laib/laib updates --run\nExecStartPost=-/var/opt/laib/laib template sync --propose\nEnvironment=LAIB_HOME=/mnt/data/laib\nEnvironment=LAIB_ACTOR=laib\nEnvironment=LAIB_UPDATE=" + cmp.Or(st.Box.Updates.Laib, "auto") + "\n"
	out["systemd/laib-images.timer"] = "# Rendered by laib from box.yml updates and backup.schedule (#528).\n[Unit]\nDescription=Laib images timer\n\n[Timer]\nOnCalendar=" + at + "\nRandomizedDelaySec=15m\n\n[Install]\nWantedBy=timers.target\n"
	return out, nil
}

// BackupPaths lists what `laib backup` snapshots: the state repo, the
// journal and every declared data path not marked backup: false, with
// ${DATA_DIR} expanded. Sorted and deduplicated.
func BackupPaths(st *state.State, stateDir, journalDir string) []string {
	seen := map[string]bool{stateDir: true, journalDir: true}
	out := []string{stateDir, journalDir}
	for _, name := range st.ServiceNames() {
		for _, d := range st.Services[name].Contract.Data {
			if d.Backup != nil && !*d.Backup {
				continue
			}
			p := strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir)
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out[2:])
	return out
}

// ValuesFile holds a service's values (#364): a value only a hook reads
// changes no unit, and through this file it is still a change of the
// service, so apply re-runs its hooks and restarts it. Quadlet ignores it.
const ValuesFile = "values.laib"

// HooksFile holds a hash of each hook (#496): a template update that only
// changes pre-start or post-deploy changes no unit, and through this file
// it is still a change of the service, so apply runs the new hook.
const HooksFile = "hooks.laib"

func hooks(name string, svc *state.Service) string {
	b := "# Rendered by laib: the hooks of " + name + " (#496), by their hash.\n"
	for _, h := range slices.Sorted(maps.Keys(svc.Hooks)) {
		b += fmt.Sprintf("%s=sha256:%x\n", h, sha256.Sum256([]byte(svc.Hooks[h])))
	}
	return b
}

func values(name string, svc *state.Service) string {
	var b strings.Builder
	b.WriteString("# Rendered by laib: the values of " + name + " (#364); a secret by its name only.\n")
	for _, k := range slices.Sorted(maps.Keys(svc.Values)) {
		v := svc.Values[k].Literal
		if s := svc.Values[k].Secret; s != "" {
			v = "!secret " + s
		}
		b.WriteString(k + "=" + v + "\n")
	}
	return b.String()
}

func backupService() string {
	return "# Rendered by laib: the nightly backup run (docs/BACKUP.md).\n[Unit]\nDescription=Laib backup\n\n[Service]\nType=oneshot\nExecStart=/var/opt/laib/laib backup\nEnvironment=LAIB_HOME=/mnt/data/laib\nEnvironment=LAIB_ACTOR=laib\n"
}

func backupTimer(schedule string) string {
	schedule = cmp.Or(schedule, "03:00")
	return "# Rendered by laib from box.yml backup.schedule.\n[Unit]\nDescription=Laib backup timer\n\n[Timer]\nOnCalendar=*-*-* " + schedule + ":00\nPersistent=true\nRandomizedDelaySec=10m\n\n[Install]\nWantedBy=timers.target\n"
}

// Redact replaces every secret value in the output with its placeholder, so
// a diff or a repo copy never carries a secret.
func Redact(out Output, secrets map[string]string) Output {
	if len(secrets) == 0 {
		return out
	}
	red := Output{}
	for p, c := range out {
		for name, val := range secrets {
			if val != "" {
				c = strings.ReplaceAll(c, val, Placeholder(name))
			}
		}
		red[p] = c
	}
	return red
}

// ProxySecret is the header value Caddy adds so the pages know a request
// came through Authelia and not from a process on the box (ADR 0012).
const ProxySecret = "LAIB_PROXY_SECRET"

// SecretNames lists every secret a state declares, sorted. Laib's own proxy
// secret is always declared when a proxy role exists.
// ModelKeySecret is the model key's name in secrets.age: declared by the
// model role's template on the GPU box, set by hand on a box that uses a
// remote proxy (box.yml model.keySecret).
const ModelKeySecret = "MODEL_API_KEY"

func SecretNames(st *state.State) []state.Secret {
	var out []state.Secret
	if st.Box.Roles.Proxy != "" {
		out = append(out, state.Secret{Name: ProxySecret})
	}
	if st.Box.Model.KeySecret != "" {
		out = append(out, state.Secret{Name: st.Box.Model.KeySecret, External: true})
	}
	if st.Box.Operator.Enabled {
		out = append(out, state.Secret{Name: OperatorToken, External: true}) // minted by laib operator enable
	}
	if notifier(st) {
		out = append(out, state.Secret{Name: NotifyToken, External: true, Mint: "notify"}) // minted on apply
	}
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		out = append(out, svc.Contract.Secrets...)
		if svc.Contract.OIDC != nil {
			out = append(out, state.Secret{Name: state.OIDCSecretName(svc.Contract.OIDC.ClientID)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func variables(st *state.State, svc *state.Service, secrets map[string]string) map[string]string {
	vars := map[string]string{
		"DOMAIN":   st.Box.Domain,
		"DATA_DIR": st.Box.DataDir,
		"TZ":       st.Box.Timezone,
		"SERVICE":  svc.Name,
		"LAN_IP":   st.Box.LanIP,
		// the box page, never a hard-coded laib.<domain> (#381)
		"LAIB_PAGE_URL": "https://" + fqdn(st, st.Box.Laib.Host) + "/",
	}
	maps.Copy(vars, ModelVars(st, secrets))
	if p := st.HousePersona(); p != nil {
		vars["PERSONA"], vars["PERSONA_DISPLAY"], vars["PERSONA_PHRASE"] = p.Name, p.Display, p.Wakeword.Phrase
		vars["PERSONA_WAKEWORD"], vars["PERSONA_VOICE"], vars["PERSONA_REV"] = p.WakewordID(), p.Voice, st.PersonaRev()
		vars["PERSONA_LANGUAGES"] = strings.Join(p.Languages, ",")
	}
	vars["PEOPLE_REV"], vars["NOTIFY_PERSON"] = st.PeopleRev(), st.Box.Notify.Person
	maps.Copy(vars, st.ZuhauseVars())
	for k, v := range svc.Values {
		if v.Secret != "" {
			vars[k] = secretValue(secrets, v.Secret)
			continue
		}
		vars[k] = v.Literal
	}
	return vars
}

// ModelVars are the builtins MODEL_URL, MODEL_LEASE and MODEL_KEY as units
// and hooks get them. Both read them in a container, whose 127.0.0.1 is its
// own: the model role's proxy and lease are host.containers.internal there
// (ADR 0007, #198, #200); no template's host .service reads them. The key
// is box.yml's, or the local proxy's own (the llama template declares it):
// the Bote, the Werkstatt and the Operator take the lease with it (#42).
func ModelVars(st *state.State, secrets map[string]string) map[string]string {
	inContainer := func(url string) string {
		return strings.Replace(url, "://127.0.0.1:", "://host.containers.internal:", 1)
	}
	key := ""
	if st.Box.Model.KeySecret != "" {
		key = secretValue(secrets, st.Box.Model.KeySecret)
	} else if st.Box.Roles.Model != "" {
		key = secretValue(secrets, ModelKeySecret)
	}
	return map[string]string{"MODEL_URL": inContainer(st.ModelURL()), "MODEL_LEASE": inContainer(st.ModelLease()), "MODEL_KEY": key}
}

// uses: some service's unit names a value starting with prefix (PERSONA…, PEOPLE_REV).
func uses(st *state.State, prefix string) bool {
	return slices.ContainsFunc(slices.Collect(maps.Values(st.Services)), func(svc *state.Service) bool { return svc.Uses(prefix) })
}

// secretValue: the placeholder only while no secrets are given (plan,
// lint); with them an unset secret is an empty value, never the
// placeholder text (#185), as in a hook's LAIB_SECRET_<NAME>.
func secretValue(secrets map[string]string, name string) string {
	if secrets == nil {
		return Placeholder(name)
	}
	return secrets[name]
}

// PodmanSecrets: every podman secret the rendered units name (#224), name →
// value. Apply hands each to podman on stdin before a unit starts.
func PodmanSecrets(st *state.State, secrets map[string]string) map[string]string {
	all := map[string]string{}
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		vars := variables(st, svc, secrets)
		for file, content := range svc.Units {
			_, sec, _ := secretEnv(svc, file, content, vars) // Render reported an error first
			maps.Copy(all, sec)
		}
	}
	if st.Box.Operator.Enabled {
		_, sec := operatorUnit(st, secrets)
		maps.Copy(all, sec)
	}
	if notifier(st) {
		_, sec := notifyUnit(st, secrets)
		maps.Copy(all, sec)
	}
	return all
}

// secretEnv substitutes a unit; in a .container each Environment= line that
// names a secret-backed value becomes Secret=<name>,type=env,target=<KEY>
// (#224): Quadlet puts every Environment= value on podman run's argv, where
// ps shows it to every user on the box. The values come back under the
// names. An empty value stays an empty Environment= line: nothing to hide,
// and podman refuses an empty secret. What podman cannot take as one secret
// (two assignments, a key with a comma, quote or blank) is an error, never
// an Environment= line.
func secretEnv(svc *state.Service, file, content string, vars map[string]string) (string, map[string]string, error) {
	if !strings.HasSuffix(file, ".container") {
		return substitute(content, vars), nil, nil
	}
	sec := map[string]string{}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		k, rest, _ := strings.Cut(line, "=") // systemd takes blanks around the =
		if strings.TrimSpace(k) != "Environment" || !slices.ContainsFunc(state.Placeholders(rest), isSecret(svc)) {
			lines[i] = substitute(line, vars)
			continue
		}
		words := envWords(rest)
		if len(words) != 1 {
			return "", nil, fmt.Errorf("services/%s/%s: %s: a line that names a secret holds one assignment (#224)", svc.Name, file, line)
		}
		key, val, _ := strings.Cut(substitute(words[0], vars), "=")
		if key == "" || strings.ContainsAny(key, ",\"'") || strings.ContainsFunc(key, unicode.IsSpace) {
			return "", nil, fmt.Errorf("services/%s/%s: %s: its key is no podman secret target (a comma, quote or blank)", svc.Name, file, line)
		}
		if val == "" {
			lines[i] = "Environment=" + key + "="
			continue
		}
		name := podmanSecretName(svc.Name, strings.TrimSuffix(file, ".container"), key)
		sec[name] = val
		lines[i] = "Secret=" + name + ",type=env,target=" + key
	}
	return strings.Join(lines, "\n"), sec, nil
}

// isSecret: the value is secret-backed, a !secret or the builtin MODEL_KEY.
func isSecret(svc *state.Service) func(string) bool {
	return func(v string) bool { return v == "MODEL_KEY" || svc.Values[v].Secret != "" }
}

// secretOffEnvironment refuses a secret-backed value anywhere in a unit but
// an Environment= line (#233): substituted into Exec=, PodmanArgs=, Label=
// or a host unit's ExecStart= it lands in the unit file and on argv, where
// ps shows it to every user on the box. The error names the template line,
// which holds the placeholder, never the value.
func secretOffEnvironment(svc *state.Service, file, content string) error {
	for i, line := range strings.Split(content, "\n") {
		k, _, _ := strings.Cut(line, "=")
		if strings.TrimSpace(k) == "Environment" {
			continue
		}
		names := state.Placeholders(line)
		if v := slices.IndexFunc(names, isSecret(svc)); v >= 0 {
			return fmt.Errorf("services/%s/%s:%d: %s: ${%s} is a secret; only an Environment= line may carry one (#224, #233)", svc.Name, file, i+1, line, names[v])
		}
	}
	return nil
}

// envWords splits an Environment= value into its assignments as systemd
// does: at blanks outside quotes, the quotes dropped.
func envWords(s string) (words []string) {
	var w strings.Builder
	quote, in := rune(0), false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '"' || r == '\''):
			quote, in = r, true
		case quote == 0 && unicode.IsSpace(r):
			if in {
				words, in = append(words, w.String()), false
				w.Reset()
			}
		default:
			w.WriteRune(r)
			in = true
		}
	}
	if in {
		words = append(words, w.String())
	}
	return words
}

var plainSecretName = regexp.MustCompile(`^laib\.[a-z0-9-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_]*[A-Za-z0-9]$`)

// podmanSecretName is laib.<service>.<container>.<KEY> when podman takes it
// (253 of [a-zA-Z0-9_.-], alphanumeric at both ends) and its three dots are
// the only ones. A key built from a value (ACCOUNT_${SHARE_USER}) can break
// either; that line gets a hash of container and key instead, a name with
// two dots, so it never meets a readable one.
func podmanSecretName(svc, stem, key string) string {
	name := "laib." + svc + "." + stem + "." + key
	if len(name) <= 253 && plainSecretName.MatchString(name) {
		return name
	}
	sum := sha256.Sum256([]byte(stem + "\x00" + key))
	return "laib." + svc + "." + hex.EncodeToString(sum[:12])
}

func substitute(content string, vars map[string]string) string {
	for _, name := range state.Placeholders(content) {
		if v, ok := vars[name]; ok {
			content = strings.ReplaceAll(content, "${"+name+"}", v)
		}
	}
	return content
}

func fqdn(st *state.State, host string) string { return host + "." + st.Box.Domain }

// ssoPort is the Authelia listener Caddy forwards auth to: the sso service's
// first route port.
func ssoPort(st *state.State) int {
	if svc, ok := st.Services[st.Box.Roles.SSO]; ok && len(svc.Contract.Routes) > 0 {
		return svc.Contract.Routes[0].Port
	}
	return 9091
}

// HausURL is the family's entry, Haus's first route, or "" without a
// service that provides haus.
func HausURL(st *state.State) string {
	for _, name := range st.ServiceNames() {
		if c := st.Services[name].Contract; slices.Contains(c.Provides, "haus") && len(c.Routes) > 0 {
			return "https://" + fqdn(st, c.Routes[0].Host) + "/"
		}
	}
	return ""
}

// StartURL is where a login without a target lands (#381): Haus once it is
// installed, else the box page (box.yml laib.host). Both appear in
// laib-access.yml, so a change to either reruns the sso hook that reads it.
func StartURL(st *state.State) string {
	return cmp.Or(HausURL(st), "https://"+fqdn(st, st.Box.Laib.Host)+"/")
}

// LogoutURL is Authelia's logout (#480); its rd, escaped, is where the
// login afterwards returns to.
func LogoutURL(st *state.State) string {
	host := "auth"
	if svc, ok := st.Services[st.Box.Roles.SSO]; ok && len(svc.Contract.Routes) > 0 {
		host = svc.Contract.Routes[0].Host
	}
	return "https://" + fqdn(st, host) + "/logout"
}

// denyPage is a 403 a person can read (#480): German, the household look
// (#382), the reason and the way out. Caddy fills {http.request.host}.
func denyPage(indent, title, body string) string {
	const css = `:root{--bg:#070709;--card:#14141A;--ink:#F0F0F0;--muted:#94A3B8;--accent:#F97316}@media (prefers-color-scheme: light){:root{--bg:#F1F5F9;--card:#FFFFFF;--ink:#0F172A;--muted:#64748B;--accent:#EA580C}}` +
		`body{margin:0 auto;max-width:34rem;padding:1.5rem 1rem;background:var(--bg);color:var(--ink);font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}main{background:var(--card);border-left:4px solid var(--accent);border-radius:12px;padding:.25rem 1.25rem}h1{font-size:1.15rem}a{color:var(--accent)}.muted{color:var(--muted)}`
	return indent + "header Content-Type \"text/html; charset=utf-8\"\n" + indent + "respond <<HTML\n" + indent + "\t<!doctype html><html lang=de><head><meta charset=utf-8><meta name=viewport content=\"width=device-width,initial-scale=1\"><meta name=color-scheme content=\"dark light\"><title>" +
		title + "</title><style>" + css + "</style></head><body><main>" + body + "</main></body></html>\n" + indent + "\tHTML 403\n"
}

// denySnippets are the two 403s as snippets: lanonly for a LAN-only name
// reached from outside (after the unchanged LAN check, before Authelia),
// denied for a login whose account may not open the name.
func denySnippets(st *state.State) string {
	home := ""
	if u := HausURL(st); u != "" {
		home = `<p><a href="` + u + `">Zur Startseite</a> <span class=muted>– die geht auch unterwegs</span></p>`
	}
	lan := "<h1>Diese Seite ist nur zu Hause im WLAN erreichbar</h1><p>{http.request.host} ist mit Absicht nicht aus dem Internet zu öffnen. Dein Gerät ist gerade nicht im WLAN zu Hause, zum Beispiel über mobile Daten.</p><p><b>So geht es:</b> mit dem WLAN zu Hause verbinden und die Seite neu laden.</p>" + home
	deny := "<h1>Kein Zugang mit diesem Konto</h1><p>Du bist angemeldet, aber dein Konto darf {http.request.host} nicht öffnen. Manche Seiten sind nur für die Admins der Box.</p><p><a href=\"" + LogoutURL(st) + "?rd=https://{http.request.host}/\">Abmelden und mit einem anderen Konto anmelden</a></p>" + home
	return "(lanonly) {\n\thandle {\n" + denyPage("\t\t", "Nur zu Hause", lan) + "\t}\n}\n\n(denied) {\n" + denyPage("\t", "Kein Zugang", deny) + "}\n\n"
}

// PublicResolvers answer what Let's Encrypt sees, not the LAN's rewrites:
// Caddy's DNS-01 checks and diag's challenge probe ask them (#378).
var PublicResolvers = []string{"9.9.9.9", "1.1.1.1"}

const stripRemote = "\trequest_header -Remote-*\n"

func caddyfile(st *state.State, secrets map[string]string) string {
	var b strings.Builder
	b.WriteString("# Rendered by laib. Do not edit; change services/*/laib.yml and run laib apply.\n")
	b.WriteString("{\n\temail admin@" + st.Box.Domain + "\n")
	// before forward_auth, which runs first by default: every site strips the
	// client's Remote-* and only Authelia's copy reaches an app (#231)
	b.WriteString("\torder request_header first\n")
	// DNS-01 (#378): one issuer for every name, the challenge at the alias if
	// there is one. Not acme_dns: it replaces the issuer's challenge config.
	// local_certs and cert_issuer exclude each other, so internal wins.
	dns01 := st.Box.DNS.Provider != "" && st.Box.TLS != "internal"
	if dns01 {
		token := "{env.LAIB_DNS_TOKEN}"
		if st.Box.DNS.Provider == "desec" { // its module takes no positional token
			token = "{\n\t\t\ttoken {env.LAIB_DNS_TOKEN}\n\t\t}"
		}
		fmt.Fprintf(&b, "\tcert_issuer acme {\n\t\tdns %s %s\n", st.Box.DNS.Provider, token)
		if a := st.Box.DNS.ChallengeAlias; a != "" {
			fmt.Fprintf(&b, "\t\tdns_challenge_override_domain %s\n", a)
		}
		// public resolvers: the box's AdGuard rewrites *.<domain>, _acme-challenge too
		fmt.Fprintf(&b, "\t\tresolvers %s\n\t}\n", strings.Join(PublicResolvers, " "))
	}
	if st.Box.TLS == "internal" {
		b.WriteString("\tlocal_certs\n") // every host from Caddy's internal CA; trust data/caddy/pki/authorities/local/root.crt on the devices
	}
	b.WriteString("}\n\n")
	b.WriteString(denySnippets(st))
	if st.Box.Roles.SSO != "" {
		// Authelia's 403 (signed in, but not this account's name) becomes the denied page
		fmt.Fprintf(&b, "(authelia) {\n\tforward_auth 127.0.0.1:%d {\n\t\turi /api/authz/forward-auth\n\t\tcopy_headers Remote-User Remote-Groups Remote-Email Remote-Name\n\t\t@denied status 403\n\t\thandle_response @denied {\n\t\t\timport denied\n\t\t}\n\t}\n}\n\n", ssoPort(st))
	}
	if st.Box.Roles.Proxy != "" {
		// Laib's own page: LAN only, Authelia, and a proxy secret so the pages
		// trust Remote-User only when it arrived through Caddy (ADR 0012).
		fmt.Fprintf(&b, "%s {\n%s\t@lan remote_ip private_ranges\n\thandle @lan {\n", fqdn(st, st.Box.Laib.Host), stripRemote)
		if st.Box.Roles.SSO != "" {
			b.WriteString("\t\timport authelia\n")
		}
		fmt.Fprintf(&b, "\t\treverse_proxy 127.0.0.1:%d {\n\t\t\theader_up X-Laib-Proxy %q\n\t\t}\n\t}\n\timport lanonly\n}\n\n", st.Box.Laib.Port, secretValue(secrets, ProxySecret))
	}
	if dns01 {
		// one wildcard certificate: Caddy (2.10+) then gets none per name, a
		// new route has a certificate at once and no name reaches the CT logs;
		// a name without a route is closed
		fmt.Fprintf(&b, "*.%s {\n%s\tabort\n}\n\n", st.Box.Domain, stripRemote)
	}
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		for _, r := range svc.Contract.Routes {
			fmt.Fprintf(&b, "%s {\n%s", fqdn(st, r.Host), stripRemote)
			indent := "\t"
			if r.Exposure == "internal" {
				b.WriteString("\t@lan remote_ip private_ranges\n\thandle @lan {\n")
				indent = "\t\t"
			}
			if len(r.Bypass) > 0 {
				// the bypass paths go straight to the service; everything else below is behind Authelia
				var ms []string
				for _, p := range r.Bypass {
					ms = append(ms, "path "+strings.TrimSuffix(p, "/")+"/*")
				}
				fmt.Fprintf(&b, "%s@bypass %s\n%shandle @bypass {\n%s\treverse_proxy 127.0.0.1:%d\n%s}\n", indent, strings.Join(ms, " "), indent, indent, r.Port, indent)
			}
			if forwardAuth(st, name, r) {
				b.WriteString(indent + "import authelia\n")
			}
			if r.Caddy != "" {
				for _, line := range strings.Split(strings.TrimRight(r.Caddy, "\n"), "\n") {
					b.WriteString(indent + line + "\n")
				}
			}
			if r.ProxySecret != "" {
				name := r.ProxyHeader
				if name == "" {
					name = "X-" + strings.ToUpper(svc.Name[:1]) + svc.Name[1:] + "-Proxy"
				}
				fmt.Fprintf(&b, "%sreverse_proxy 127.0.0.1:%d {\n%s\theader_up %s %q\n%s}\n", indent, r.Port, indent, name, secretValue(secrets, r.ProxySecret), indent)
			} else {
				fmt.Fprintf(&b, "%sreverse_proxy 127.0.0.1:%d\n", indent, r.Port)
			}
			if r.Exposure == "internal" {
				b.WriteString("\t}\n\timport lanonly\n")
			}
			b.WriteString("}\n\n")
			if len(r.RedirectFrom) > 0 {
				var from []string
				for _, h := range r.RedirectFrom {
					from = append(from, strings.TrimPrefix(fqdn(st, h), "@."))
				}
				fmt.Fprintf(&b, "%s {\n\tredir https://%s{uri}\n}\n\n", strings.Join(from, ", "), fqdn(st, r.Host))
			}
		}
	}
	return b.String()
}

// accessRule is one of Authelia's rules: autheliaAccess writes them, Net
// reads them, so the map shows the factor Authelia asks for (#487).
type accessRule struct {
	Domain, Policy string
	Subjects       []string // groups
	Resource       string   // a bypass of one path prefix
}

func accessRules(st *state.State) []accessRule {
	// the Inbox decides what the chat may not (#441): behind the second factor
	rules := []accessRule{{Domain: fqdn(st, st.Box.Laib.Host), Policy: "two_factor", Subjects: []string{st.Box.Laib.Group}}}
	for _, name := range st.ServiceNames() {
		for _, r := range st.Services[name].Contract.Routes {
			for _, p := range r.Bypass {
				rules = append(rules, accessRule{Domain: fqdn(st, r.Host), Policy: "bypass", Resource: strings.TrimSuffix(p, "/") + "/"})
			}
			rule := accessRule{Domain: fqdn(st, r.Host), Policy: policyOf(r)}
			if rule.Policy != "bypass" {
				rule.Subjects = subjects(r)
			}
			rules = append(rules, rule)
		}
	}
	return rules
}

func autheliaAccess(st *state.State) string {
	var b strings.Builder
	// Every rule Authelia has (#249): a list from a later config file replaces
	// an earlier one instead of joining it, so the auth hook's config has none.
	b.WriteString("# Rendered by laib: every access rule. Anything unlisted, the bare apex\n# included, is denied.\naccess_control:\n  default_policy: deny\n  rules:\n")
	for _, r := range accessRules(st) {
		fmt.Fprintf(&b, "    - domain: %s\n      policy: %s\n", r.Domain, r.Policy)
		if r.Resource != "" {
			fmt.Fprintf(&b, "      resources: [\"^%s.*$\"]\n", r.Resource)
		}
		// family and admins, never just any LLDAP account (radicale's bind
		// account is one); admins reach what the family reaches
		var groups []string
		for _, g := range r.Subjects {
			groups = append(groups, `"group:`+g+`"`)
		}
		switch len(groups) {
		case 0:
		case 1:
			b.WriteString("      subject: " + groups[0] + "\n")
		default:
			b.WriteString("      subject: [" + strings.Join(groups, ", ") + "]\n")
		}
	}
	return b.String()
}

func autheliaClients(st *state.State, secrets map[string]string) string {
	var b strings.Builder
	b.WriteString("# Rendered by laib: one OIDC client per service that declares oidc.\n")
	if !slices.ContainsFunc(st.ServiceNames(), func(name string) bool { return st.Services[name].Contract.OIDC != nil }) {
		// Authelia refuses an oidc block without jwks and clients (#36): no
		// block at all until the first service declares oidc
		b.WriteString("# no service declares oidc yet: Authelia gets no OIDC block (it refuses an empty one)\n")
		return b.String()
	}
	b.WriteString("identity_providers:\n  oidc:\n    clients:\n")
	for _, name := range st.ServiceNames() {
		o := st.Services[name].Contract.OIDC
		if o == nil {
			continue
		}
		// one factor, as ServiceBay registered the family's clients (Authelia's default is two, #251)
		fmt.Fprintf(&b, "      - client_id: %s\n        client_name: %s\n        client_secret: %q\n        authorization_policy: one_factor\n        token_endpoint_auth_method: %s\n        redirect_uris:\n",
			o.ClientID, name, clientSecretHash(secrets, o.ClientID), cmp.Or(o.TokenEndpointAuthMethod, "client_secret_basic"))
		for _, u := range o.RedirectURIs {
			fmt.Fprintf(&b, "          - %s\n", strings.ReplaceAll(u, "${DOMAIN}", st.Box.Domain))
		}
		b.WriteString("        scopes: [" + cmp.Or(strings.Join(o.Scopes, ", "), "openid, profile, email, groups") + "]\n")
	}
	return b.String()
}

func adguardRewrites(st *state.State) string {
	type rw struct {
		Domain string `json:"domain"`
		Answer string `json:"answer"`
	}
	rws := []rw{{Domain: "*." + st.Box.Domain, Answer: st.Box.LanIP}, {Domain: st.Box.Domain, Answer: st.Box.LanIP}}
	b, _ := json.MarshalIndent(rws, "", "  ")
	return string(b) + "\n"
}

// droppedPorts: what the nftables drop keeps off the LAN; Net reads it too (#487).
func droppedPorts(st *state.State) []int {
	var ports []int
	if st.Box.Laib.Listen != "127.0.0.1" {
		ports = append(ports, st.Box.Laib.Port)
	}
	for _, name := range st.ServiceNames() {
		for _, p := range st.Services[name].Contract.Ports {
			if !p.LAN {
				ports = append(ports, p.Port)
			}
		}
	}
	return ports
}

func nftables(st *state.State) string {
	var ports []string
	for _, p := range droppedPorts(st) {
		ports = append(ports, fmt.Sprint(p))
	}
	if len(ports) == 0 {
		return ""
	}
	// Physical interfaces only: loopback, the pasta/bridge interfaces of
	// pods (host.containers.internal) and VPN tunnels keep reaching these
	// ports; the WLAN and the wired LAN do not.
	// One nft transaction that replaces the table (#102): a table block on an
	// existing table only adds to it, so a port leaving this list stayed
	// dropped until a reboot. The bare declaration keeps the delete from
	// failing on a boot where the table does not exist yet.
	return "# Rendered by laib: ports declared lan: false are dropped on physical interfaces (en*, eth*, wl*).\n" +
		"table inet laib\ndelete table inet laib\n" +
		"table inet laib {\n\tchain input {\n\t\ttype filter hook input priority -10; policy accept;\n" +
		"\t\tiifname { \"en*\", \"eth*\", \"wl*\" } tcp dport { " + strings.Join(ports, ", ") + " } drop\n\t}\n}\n"
}

// clientSecretHash renders Authelia's expected PBKDF2-SHA512 digest of the
// client secret: `$pbkdf2-sha512$<iter>$<salt>$<key>` with the salt and key
// in passlib's adapted base64 ('.' for '+', no padding). The salt is derived
// from the secret and client id so a re-render is byte-identical and `laib
// plan` stays quiet (ADR 0001). The plain value stays with the service.
func clientSecretHash(secrets map[string]string, clientID string) string {
	name := state.OIDCSecretName(clientID)
	plain := secrets[name]
	if plain == "" {
		return secretValue(secrets, name)
	}
	return PBKDF2SHA512(plain, clientID)
}

const pbkdf2Iterations = 310000

// PBKDF2SHA512 is exported for the test that re-derives it.
func PBKDF2SHA512(plain, saltSeed string) string {
	sum := sha256.Sum256([]byte("laib-oidc-salt:" + saltSeed + ":" + plain))
	salt := sum[:16]
	key := pbkdf2.Key([]byte(plain), salt, pbkdf2Iterations, 64, sha512.New)
	return fmt.Sprintf("$pbkdf2-sha512$%d$%s$%s", pbkdf2Iterations, ab64(salt), ab64(key))
}

func ab64(b []byte) string {
	return strings.ReplaceAll(base64.RawStdEncoding.EncodeToString(b), "+", ".")
}

// NotifyToken is laib-notify's bearer (#443), as the notify hand.
const NotifyToken = "NOTIFY_TOKEN"

// mailTo is the owner's address when Authelia has mail settings (#393): one
// mail config on the box. Empty: no mail.
func mailTo(st *state.State) string {
	sso := st.Services[st.Box.Roles.SSO]
	if sso == nil || !sso.Set("SMTP_HOST") {
		return ""
	}
	return cmp.Or(st.Box.Notify.Mail, sso.Values["OPERATOR_EMAIL"].Literal)
}

// notifier: laib-notify runs when it has something to do (#443).
func notifier(st *state.State) bool {
	return st.Box.Watchdog.URL != "" || mailTo(st) != "" || st.Box.Notify.Person != ""
}

// OperatorImage and NotifyImage are the images of Laib's own programs that
// run in containers (ADR 0027). The release pins them by digest (ldflags
// -X, .github/workflows/release.yml), so they move with the binary that
// renders their units: self-update and self-rollback carry them along.
// A build from source renders the floating tag CI publishes on main.
var (
	OperatorImage = "ghcr.io/mdopp/laib-operator:latest"
	NotifyImage   = "ghcr.io/mdopp/laib-notify:latest"
)

// Own are those programs by unit name (ADR 0027): their output is
// quadlet/<name>/<name>.container, apply starts the long-running ones.
var Own = []string{"laib-operator", "laib-notify"}

// LaibHome is where they keep their folders; units carry no LAIB_HOME of
// a test, as every unit laib renders.
const LaibHome = "/mnt/data/laib"

// ownUnit renders one of them as a Quadlet container: the host's network
// (laib, the model, Frage and bote-notify stay on 127.0.0.1 as before), its
// own folder as the only mount, made on the host before it starts, a
// read-only root without capabilities, and its secrets as podman secrets
// (#224): no secret on argv, in the unit or in a file. An empty secret is
// left out, podman refuses one. It returns the unit and the secrets.
func ownUnit(name, head, folder, inside string, env, sec []string, service string) (string, map[string]string) {
	image := map[string]string{"laib-operator": OperatorImage, "laib-notify": NotifyImage}[name]
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n[Container]\nImage=%s\nNetwork=host\nTimezone=local\nReadOnly=true\nNoNewPrivileges=true\nDropCapability=all\nVolume=%s/%s:%s:Z\n", head, image, LaibHome, folder, inside)
	for i := 0; i+1 < len(env); i += 2 {
		fmt.Fprintf(&b, "Environment=\"%s=%s\"\n", env[i], strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(env[i+1]))
	}
	secrets := map[string]string{}
	for i := 0; i+1 < len(sec); i += 2 {
		if sec[i+1] == "" {
			continue
		}
		n := podmanSecretName(name, name, sec[i])
		secrets[n] = sec[i+1]
		fmt.Fprintf(&b, "Secret=%s,type=env,target=%s\n", n, sec[i])
	}
	fmt.Fprintf(&b, "\n[Service]\nExecStartPre=/usr/bin/mkdir -p %s/%s\n%s", LaibHome, folder, service)
	return b.String(), secrets
}

// notifyUnit is laib-notify's container, a oneshot its timer starts: laib's
// MCP and /ready, the watchdog URL and Authelia's mail settings; the token
// and the SMTP password as podman secrets. Its folder holds mail.json and
// watchdog.last, which /ready reads.
func notifyUnit(st *state.State, secrets map[string]string) (string, map[string]string) {
	base := fmt.Sprintf("http://127.0.0.1:%d", st.Box.Laib.Port)
	env := []string{"LAIB_MCP_URL", base + "/mcp", "LAIB_READY_URL", base + "/ready", "NOTIFY_HOME", "/notify", "LAIB_DOMAIN", st.Box.Domain,
		"WATCHDOG_URL", st.Box.Watchdog.URL, "LAIB_INBOX_URL", "https://" + fqdn(st, st.Box.Laib.Host) + "/inbox"}
	sec := []string{"LAIB_TOKEN", secretValue(secrets, NotifyToken)}
	if p := st.Box.Notify.Person; p != "" {
		// the Bote's channels first (#441): bote-notify on the host port templates/bote declares
		env = append(env, "NOTIFY_PERSON", p, "BOTE_NOTIFY_URL", "http://127.0.0.1:8645")
		sec = append(sec, "BOTE_NOTIFY_TOKEN", secretValue(secrets, "BOTE_NOTIFY_TOKEN"))
	}
	if to := mailTo(st); to != "" {
		v := st.Services[st.Box.Roles.SSO].Values
		env = append(env, "MAIL_TO", to)
		for _, k := range []string{"SMTP_HOST", "SMTP_PORT", "SMTP_SECURITY", "SMTP_USER", "SMTP_FROM"} {
			env = append(env, k, v[k].Literal)
		}
		sec = append(sec, "SMTP_PASSWORD", secretValue(secrets, "AUTHELIA_SMTP_PASSWORD"))
	}
	// #624: what the host unit kept in LAIB_HOME moves into the folder once
	move := fmt.Sprintf("ExecStartPre=-/usr/bin/mv -n %[1]s/mail.json %[1]s/watchdog.last %[1]s/notify/\nType=oneshot\n", LaibHome)
	return ownUnit("laib-notify", "# Rendered by laib: the watchdog ping and the owner's mail (#443), in its container (ADR 0027).\n[Unit]\nDescription=Laib notify\nAfter=laib.service\n",
		"notify", "/notify", env, sec, move)
}

// OperatorToken is the secret holding the operator's ops bearer.
const OperatorToken = "OPERATOR_TOKEN"

func operatorUnit(st *state.State, secrets map[string]string) (string, map[string]string) {
	env := []string{"LAIB_MCP_URL", fmt.Sprintf("http://127.0.0.1:%d/mcp", st.Box.Laib.Port), "MODEL_URL", st.ModelURL(),
		"MODEL_NAME", cmp.Or(st.Box.Operator.Model, "qwen3.6-35b-a3b"), "OPERATOR_HOME", "/operator", "OPERATOR_LISTEN", "127.0.0.1:5901",
		"OPERATOR_TICK", cmp.Or(st.Box.Operator.Tick, "10m"), "OPERATOR_REPORT_AT", cmp.Or(st.Box.Operator.ReportAt, "07:00")}
	key := ""
	if st.Box.Model.KeySecret != "" {
		key = secretValue(secrets, st.Box.Model.KeySecret)
	}
	if n := st.Box.Operator.ContextWindow; n > 0 {
		env = append(env, "OPERATOR_CONTEXT_WINDOW", fmt.Sprint(n))
	}
	sec := []string{"LAIB_TOKEN", secretValue(secrets, OperatorToken), "MODEL_KEY", key}
	for _, name := range st.ServiceNames() {
		c := st.Services[name].Contract
		if len(c.Ports) == 0 {
			continue
		}
		if slices.Contains(c.Provides, "usage-metrics") {
			// the nutzung skill reads usage-metrics with its token (#270), on the host port
			env = append(env, "USAGE_METRICS_URL", "http://127.0.0.1:"+fmt.Sprint(c.Ports[0].Port))
			sec = append(sec, "USAGE_METRICS_TOKEN", secretValue(secrets, "USAGE_METRICS_TOKEN"))
		}
		if slices.Contains(c.Provides, "frage") {
			// the web search reads through Frage's SearXNG (#544), on the host port
			env = append(env, "FRAGE_URL", "http://127.0.0.1:"+fmt.Sprint(c.Ports[0].Port))
		}
	}
	return ownUnit("laib-operator", "# Rendered by laib: the box's admin agent (ADR 0013), in its container (ADR 0027).\n[Unit]\nDescription=Laib operator\nAfter=laib.service\nWants=laib.service\n",
		"operator", "/operator", env, sec, "Restart=always\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n")
}
