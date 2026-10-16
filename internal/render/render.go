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
	if st.Box.Operator.Enabled {
		out["systemd/laib-operator.service"] = operatorUnit(st, secrets)
	}
	if st.Policy.Autonomy["converge"] == "on" {
		out["systemd/laib-converge.service"] = "# Rendered by laib: the converge loop (docs/DESIGN.md Repair).\n[Unit]\nDescription=Laib converge\n\n[Service]\nType=oneshot\nExecStart=/var/opt/laib/laib converge\nEnvironment=LAIB_HOME=/mnt/data/laib\nEnvironment=LAIB_ACTOR=laib\n"
		out["systemd/laib-converge.timer"] = "# Rendered by laib: every ten minutes, and after boot.\n[Unit]\nDescription=Laib converge timer\n\n[Timer]\nOnBootSec=2m\nOnUnitActiveSec=10m\n\n[Install]\nWantedBy=timers.target\n"
	}
	if v := st.Policy.Autonomy["template-update"]; v != "" && len(st.Box.Registries) > 0 {
		out["systemd/laib-template-sync.service"] = "# Rendered by laib: pull registries and propose template updates.\n[Unit]\nDescription=Laib template sync\n\n[Service]\nType=oneshot\nExecStart=/var/opt/laib/laib template sync --propose\nEnvironment=LAIB_HOME=/mnt/data/laib\nEnvironment=LAIB_ACTOR=laib\n"
		out["systemd/laib-template-sync.timer"] = "# Rendered by laib: weekly, inside the update window's day when one is set.\n[Unit]\nDescription=Laib template sync timer\n\n[Timer]\nOnCalendar=Sat *-*-* 04:00:00\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
	}
	// After every podman auto-update, diag writes red findings to the journal
	// (and the webhook); the timer follows the update window when one is set.
	out["systemd/podman-auto-update.service.d/laib.conf"] = "# Rendered by laib (#13, #46): a snapshot before, a verify after an image update.\n[Service]\nExecStartPre=/var/opt/laib/laib backup\nExecStartPost=/var/opt/laib/laib diag --journal\nEnvironment=LAIB_HOME=/mnt/data/laib\nEnvironment=LAIB_ACTOR=laib\n"
	if len(st.Box.Updates.Days) > 0 {
		t := cmp.Or(st.Box.Updates.Time, "04:30") // never the backup's 03:00 (#46)
		out["systemd/podman-auto-update.timer.d/laib.conf"] = "# Rendered by laib from box.yml updates.\n[Timer]\nOnCalendar=\nOnCalendar=" + strings.Join(st.Box.Updates.Days, ",") + " *-*-* " + t + ":00\nRandomizedDelaySec=15m\n"
	}
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
	}
	maps.Copy(vars, ModelVars(st, secrets))
	if p := st.HousePersona(); p != nil {
		vars["PERSONA"], vars["PERSONA_DISPLAY"], vars["PERSONA_PHRASE"] = p.Name, p.Display, p.Wakeword.Phrase
		vars["PERSONA_WAKEWORD"], vars["PERSONA_VOICE"], vars["PERSONA_REV"] = p.WakewordID(), p.Voice, st.PersonaRev()
		vars["PERSONA_LANGUAGES"] = strings.Join(p.Languages, ",")
	}
	vars["PEOPLE_REV"] = st.PeopleRev()
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
	for _, svc := range st.Services {
		for _, content := range svc.Units {
			for _, v := range state.Placeholders(content) {
				if strings.HasPrefix(v, prefix) {
					return true
				}
			}
		}
	}
	return false
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
		if strings.TrimSpace(k) != "Environment" || !slices.ContainsFunc(state.Placeholders(rest), func(v string) bool { return v == "MODEL_KEY" || svc.Values[v].Secret != "" }) {
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

func caddyfile(st *state.State, secrets map[string]string) string {
	var b strings.Builder
	b.WriteString("# Rendered by laib. Do not edit; change services/*/laib.yml and run laib apply.\n")
	b.WriteString("{\n\temail admin@" + st.Box.Domain + "\n")
	if st.Box.DNS.Provider != "" {
		fmt.Fprintf(&b, "\tacme_dns %s {env.LAIB_DNS_TOKEN}\n", st.Box.DNS.Provider)
	}
	if st.Box.TLS == "internal" {
		b.WriteString("\tlocal_certs\n") // every host from Caddy's internal CA; trust data/caddy/pki/authorities/local/root.crt on the devices
	}
	b.WriteString("}\n\n")
	if st.Box.Roles.SSO != "" {
		fmt.Fprintf(&b, "(authelia) {\n\tforward_auth 127.0.0.1:%d {\n\t\turi /api/authz/forward-auth\n\t\tcopy_headers Remote-User Remote-Groups Remote-Email Remote-Name\n\t}\n}\n\n", ssoPort(st))
	}
	if st.Box.Roles.Proxy != "" {
		// Laib's own page: LAN only, Authelia, and a proxy secret so the pages
		// trust Remote-User only when it arrived through Caddy (ADR 0012).
		fmt.Fprintf(&b, "%s {\n\t@lan remote_ip private_ranges\n\thandle @lan {\n", fqdn(st, st.Box.Laib.Host))
		if st.Box.Roles.SSO != "" {
			b.WriteString("\t\timport authelia\n")
		}
		fmt.Fprintf(&b, "\t\treverse_proxy 127.0.0.1:%d {\n\t\t\theader_up X-Laib-Proxy %q\n\t\t}\n\t}\n\trespond 403\n}\n\n", st.Box.Laib.Port, secretValue(secrets, ProxySecret))
	}
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		for _, r := range svc.Contract.Routes {
			fmt.Fprintf(&b, "%s {\n", fqdn(st, r.Host))
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
			if r.Auth != "none" && name != st.Box.Roles.SSO {
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
				b.WriteString("\t}\n\trespond 403\n")
			}
			b.WriteString("}\n\n")
		}
	}
	return b.String()
}

func autheliaAccess(st *state.State) string {
	var b strings.Builder
	b.WriteString("# Rendered by laib: one rule per route. The apex deny and the wildcard\n# default live in the auth service's own config.\naccess_control:\n  rules:\n")
	fmt.Fprintf(&b, "    - domain: %s\n      policy: one_factor\n      subject: \"group:%s\"\n", fqdn(st, st.Box.Laib.Host), st.Box.Laib.Group)
	for _, name := range st.ServiceNames() {
		for _, r := range st.Services[name].Contract.Routes {
			policy := r.Auth
			if policy == "none" {
				policy = "bypass"
			}
			for _, p := range r.Bypass {
				fmt.Fprintf(&b, "    - domain: %s\n      policy: bypass\n      resources: [\"^%s.*$\"]\n", fqdn(st, r.Host), strings.TrimSuffix(p, "/")+"/")
			}
			fmt.Fprintf(&b, "    - domain: %s\n      policy: %s\n", fqdn(st, r.Host), policy)
			if r.Group != "" {
				fmt.Fprintf(&b, "      subject: \"group:%s\"\n", r.Group)
			}
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
		fmt.Fprintf(&b, "      - client_id: %s\n        client_name: %s\n        client_secret: %q\n        redirect_uris:\n", o.ClientID, name, clientSecretHash(secrets, o.ClientID))
		for _, u := range o.RedirectURIs {
			fmt.Fprintf(&b, "          - %s\n", strings.ReplaceAll(u, "${DOMAIN}", st.Box.Domain))
		}
		scopes := o.Scopes
		if len(scopes) == 0 {
			scopes = []string{"openid", "profile", "email", "groups"}
		}
		b.WriteString("        scopes: [" + strings.Join(scopes, ", ") + "]\n")
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

func nftables(st *state.State) string {
	var ports []string
	if st.Box.Laib.Listen != "127.0.0.1" {
		ports = append(ports, fmt.Sprint(st.Box.Laib.Port))
	}
	for _, name := range st.ServiceNames() {
		for _, p := range st.Services[name].Contract.Ports {
			if !p.LAN {
				ports = append(ports, fmt.Sprint(p.Port))
			}
		}
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

// OperatorToken is the secret holding the operator's ops bearer.
const OperatorToken = "OPERATOR_TOKEN"

func operatorUnit(st *state.State, secrets map[string]string) string {
	model := cmp.Or(st.Box.Operator.Model, "gemma-4-e4b")
	reportAt := cmp.Or(st.Box.Operator.ReportAt, "07:00")
	tick := cmp.Or(st.Box.Operator.Tick, "10m")
	key := ""
	if st.Box.Model.KeySecret != "" {
		key = secretValue(secrets, st.Box.Model.KeySecret)
	}
	return "# Rendered by laib: the box's admin agent (ADR 0013).\n[Unit]\nDescription=Laib operator\nAfter=laib.service\nWants=laib.service\n\n[Service]\nExecStart=/var/opt/laib/laib-operator\nEnvironment=LAIB_MCP_URL=http://127.0.0.1:" + fmt.Sprint(st.Box.Laib.Port) + "/mcp\nEnvironment=LAIB_TOKEN=" + secretValue(secrets, OperatorToken) + "\nEnvironment=MODEL_URL=" + st.ModelURL() + "\nEnvironment=MODEL_KEY=" + key + "\nEnvironment=MODEL_NAME=" + model + "\nEnvironment=OPERATOR_HOME=/mnt/data/laib/operator\nEnvironment=OPERATOR_LISTEN=127.0.0.1:5901\nEnvironment=OPERATOR_TICK=" + tick + "\nEnvironment=OPERATOR_REPORT_AT=" + reportAt + "\nRestart=always\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n"
}
