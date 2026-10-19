// Package state loads and validates the Laib state repo (docs/STATE.md).
package state

import (
	"fmt"
	"maps"
	"slices"
)

// Box is box.yml.
type Box struct {
	Domain   string `yaml:"domain"`
	Timezone string `yaml:"timezone"`
	DataDir  string `yaml:"dataDir"`
	LanIP    string `yaml:"lanIp"`
	Roles    Roles  `yaml:"roles"`
	GPU      GPU    `yaml:"gpu"`
	Backup   Backup `yaml:"backup"`
	DNS      DNS    `yaml:"dns"`
	TLS      string `yaml:"tls"` // "" = Let's Encrypt (HTTP-01, or DNS-01 with dns.provider); "internal" = Caddy's own CA (a trial box behind another box's port 80)
	Laib     Laib   `yaml:"laib"`
	// HostNetwork is the closed carve-out list (ServiceBay ADR 0007 carried
	// over, #16): only these services may declare hostNetwork: true.
	HostNetwork []string `yaml:"hostNetwork"`
	Notify      Notify   `yaml:"notify"`
	// Watchdog is the outside check (#57): serve pings url every five
	// minutes while readiness is not red; the outside turns silence into a
	// push. Contact is whom the family calls when the box is down.
	Watchdog   Watchdog   `yaml:"watchdog"`
	Updates    Updates    `yaml:"updates"`
	Registries []Registry `yaml:"registries"`
	// Model is where the LLM is when this box has no model role of its own
	// (a NUC without a GPU): a peer box's llama proxy or a cloud API. The
	// builtins MODEL_URL and MODEL_KEY resolve from here or from the role.
	Model Model `yaml:"model"`
	// Peers are the other boxes of the household (ADR 0018, issue #22).
	Peers map[string]Peer `yaml:"peers"`
	// Operator is the box's admin agent (ADR 0013): a user unit running
	// laib-operator with the ops token from secrets.age.
	Operator OperatorCfg `yaml:"operator"`
	// Intake is where wishes and escalations become tickets (#56): a GitHub
	// repo and the label a wish gets; the token is GITHUB_INTAKE_TOKEN in
	// secrets.age. Empty = wishes are kept under <home>/wishes.
	Intake Intake `yaml:"intake"`
	// Persona is the house's (ADR 0020): the wake word, the speakers' voice,
	// Haus's start page and the chat default. Joshua when unset.
	Persona string `yaml:"persona"`
	// ChatPersonas gives a Signal number (E.164) another persona for the
	// chat only; a wake word belongs to the room, not the person.
	ChatPersonas map[string]string `yaml:"chatPersonas"`
	// People are the household's people and their chat channels (ADR 0022
	// §3), preferred first: "signal:+49…", "telegram:<chat id>". A message
	// for a person goes out on these through the Bote, never to a free target.
	People map[string][]string `yaml:"people"`
}

// Intake: the ticket queue for wishes and escalations (#56).
type Intake struct {
	Repo  string `yaml:"repo"`  // owner/name
	Label string `yaml:"label"` // the label a wish gets; default wunsch
}

// OperatorCfg configures laib-operator.
type OperatorCfg struct {
	Enabled  bool   `yaml:"enabled"`
	Model    string `yaml:"model"`    // preset alias, default the household preset (qwen3.6-35b-a3b, #285)
	ReportAt string `yaml:"reportAt"` // "07:00"
	Tick     string `yaml:"tick"`     // "10m"
	// ContextWindow overrides the model's window in tokens (#285); 0 = ask
	// llama-server's /props
	ContextWindow int `yaml:"contextWindow"`
}

// Model is a remote OpenAI-compatible endpoint.
type Model struct {
	URL       string `yaml:"url"`       // e.g. http://192.168.1.20:11435/v1 or https://api.example/v1
	KeySecret string `yaml:"keySecret"` // name in secrets.age; empty for a loopback proxy
	Lease     string `yaml:"lease"`     // e.g. http://192.168.1.20:11435/api/model-lease (the proxy's port); empty = no lease
}

// Peer is another box running Laib.
type Peer struct {
	LanIP string `yaml:"lanIp"`
	Laib  string `yaml:"laib"` // its /ready and /routes, e.g. http://192.168.1.20:5900
}

// Registry is a Git repo whose templates/<name>/ folders are installable (#14).
type Registry struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	Ref  string `yaml:"ref"` // branch or tag; default main
}

// Updates is the window for podman auto-update (#13). Empty days keep
// podman's daily timer; laib diag --journal runs after every update either way.
type Updates struct {
	Days []string `yaml:"days"` // Sat, Sun …
	Time string   `yaml:"time"` // "03:00"
}

// Notify is the webhook Laib posts red results, proposals and readiness
// changes to (#15). Empty means polling only.
type Notify struct {
	URL  string `yaml:"url"`
	Mail string `yaml:"mail"` // the owner's address for laib's mails (#393); empty: the sso service's OPERATOR_EMAIL
}

// Watchdog: the outside check and the family's contact (#57).
type Watchdog struct {
	URL     string `yaml:"url"`     // a healthchecks-style ping URL outside the box
	Contact string `yaml:"contact"` // "Martin, 0170 …" — printed on the family card
}

// Laib is the box page and MCP listener itself (ADR 0012): where it listens,
// and the host Caddy publishes it under for admins.
type Laib struct {
	Host   string `yaml:"host"`   // default "laib" → laib.<domain>
	Port   int    `yaml:"port"`   // default 5900
	Listen string `yaml:"listen"` // default 127.0.0.1; "0.0.0.0" lets pods reach /mcp, the nftables drop keeps the LAN out
	Group  string `yaml:"group"`  // Authelia group for the pages, default "admins"
	// HooksImage is the container template hooks run in (#36): Python, bcrypt,
	// curl, openssl. Default ghcr.io/mdopp/laib-hooks:latest.
	HooksImage string `yaml:"hooksImage"`
}

// Roles names the service that plays each platform role.
type Roles struct {
	Proxy    string `yaml:"proxy"`
	SSO      string `yaml:"sso"`
	Identity string `yaml:"identity"`
	DNS      string `yaml:"dns"`
	Model    string `yaml:"model"` // the llama-server template (ADR 0011)
}

// GPU declares the box GPU and who holds it.
type GPU struct {
	Present bool   `yaml:"present"`
	Owner   string `yaml:"owner"`
}

// Backup is the restic target.
type Backup struct {
	Repo     string `yaml:"repo"`
	Schedule string `yaml:"schedule"`
}

// DNS is the ACME DNS-01 provider for Caddy (ADR 0007).
type DNS struct {
	Provider  string `yaml:"provider"`  // e.g. "cloudflare"; empty = HTTP-01
	SecretRef string `yaml:"secretRef"` // name in secrets.age
}

// Policy is policy.yml.
type Policy struct {
	Tiers     []string            `yaml:"tiers"`
	Hands     map[string]Hand     `yaml:"hands"`
	Approvals map[string]Approval `yaml:"approvals"`
	// Autonomy is the dial (docs/DESIGN.md "Repair"): per proposal class
	// "auto" (laib approves, applies, rolls back on red) or "proposal"
	// (a human decides). converge is "on" or "off" (the ten-minute loop).
	// Classes: values, template-update, new-service, patch, remove.
	Autonomy map[string]string `yaml:"autonomy"`
	// Envelope: snapshot "required" (default) or "optional" — whether a
	// class-2 action may go ahead without a pre-repair snapshot (#35).
	Envelope Envelope `yaml:"envelope"`
	// Skills switches the Operator's skills (ADR 0023): "paused" rests one,
	// "active" (or absent) runs it.
	Skills map[string]string `yaml:"skills"`
}

// Envelope tunes the operator's envelope.
type Envelope struct {
	Snapshot string `yaml:"snapshot"`
}

// Hand is one agent identity.
type Hand struct {
	Ceiling string   `yaml:"ceiling"`
	TTL     string   `yaml:"ttl"`
	Verbs   []string `yaml:"verbs"` // when set, the only verbs this hand may use (the approver: approve, reject, inbox, status, note)
	Human   bool     `yaml:"human"` // its verdicts count as a human's: the relay behind the admin's verified Signal number (#48)
}

// Approval says who may approve a tier.
type Approval struct {
	By       string `yaml:"by"`
	Snapshot string `yaml:"snapshot"`
}

// Contract is services/<name>/laib.yml.
type Contract struct {
	Name        string      `yaml:"name"`
	Template    TemplateRef `yaml:"template"`
	Routes      []Route     `yaml:"routes"`
	Secrets     []Secret    `yaml:"secrets"`
	OIDC        *OIDC       `yaml:"oidc"`
	Data        []DataPath  `yaml:"data"`
	Health      Health      `yaml:"health"`
	Status      *StatusFile `yaml:"status"`
	Ports       []Port      `yaml:"ports"`
	GPU         bool        `yaml:"gpu"`
	HostNetwork bool        `yaml:"hostNetwork"`
	After       []string    `yaml:"after"`    // services restarted and verified before this one (#12)
	Needs       []string    `yaml:"needs"`    // box facilities a container needs: "mcp" = laib.listen 0.0.0.0 (ADR 0012 §4, #46)
	Provides    []string    `yaml:"provides"` // purposes this service serves: photos, media, dns … (one per purpose by default)
	// When: a unit file exists only while one of the named values is set (#184).
	When map[string][]string `yaml:"when"`
	// Requires: a value that is set needs these set too (#184); a literal is
	// checked at load, a !secret at plan, which knows secrets.
	Requires map[string][]string `yaml:"requires"`
	// Channels: the people platforms this service delivers on, each on while
	// its value is set (ADR 0022 §3, #184).
	Channels map[string]string `yaml:"channels"`
}

// TemplateRef says where a service's files came from.
type TemplateRef struct {
	Registry string `yaml:"registry"`
	Name     string `yaml:"name"`
	Version  int    `yaml:"version"`
}

// Route is one published hostname.
type Route struct {
	Host     string   `yaml:"host"`
	Port     int      `yaml:"port"`
	Exposure string   `yaml:"exposure"` // internal | public
	Auth     string   `yaml:"auth"`     // none | one_factor | two_factor
	Group    string   `yaml:"group"`
	Caddy    string   `yaml:"caddy"`
	Bypass   []string `yaml:"bypass"` // path prefixes that skip Authelia on this host: a token-only API (/napi/) next to pages
	// ProxySecret names a secret Caddy sends as the header `X-<Name>-Proxy`
	// on the authenticated handler only, so the service trusts Remote-User
	// solely behind the proxy (ADR 0012 §3; Laib's own page does the same).
	ProxySecret string `yaml:"proxySecret"`
	ProxyHeader string `yaml:"proxyHeader"` // the header's name, default X-<Service>-Proxy
	// RedirectFrom: names Caddy sends to this route's host, no service behind
	// them (#360: the old entry points dopp.cloud and www. to haus.); "@" is
	// the domain itself.
	RedirectFrom []string `yaml:"redirectFrom"`
}

// Secret is a declared secret; non-external ones are generated once.
type Secret struct {
	Name     string `yaml:"name"`
	External bool   `yaml:"external"`
	Optional bool   `yaml:"optional"` // external and unset is fine: the unit gets an empty value (Caddy's DNS token without dns.provider)
	Mint     string `yaml:"mint"`     // a Laib token for this hand (policy.yml hands, not human), minted on first add — no manual token mint + secret set
}

// OIDC is the service's Authelia client.
type OIDC struct {
	ClientID                string   `yaml:"clientId"`
	RedirectURIs            []string `yaml:"redirectUris"`
	Scopes                  []string `yaml:"scopes"`
	TokenEndpointAuthMethod string   `yaml:"tokenEndpointAuthMethod"` // how the app sends its secret: client_secret_basic (default) or client_secret_post (#251)
}

// DataPath is a path to back up (or explicitly not).
type DataPath struct {
	Path   string `yaml:"path"`
	Backup *bool  `yaml:"backup"`
	// Quiesce: the service is stopped while this path is snapshotted (a
	// database copied live is not a backup, #40): stop, snapshot, start.
	Quiesce bool `yaml:"quiesce"`
}

// Health is what apply verifies.
// StatusFile is what a service says about itself beyond up and down (#309):
// a JSON file it keeps, {"at": RFC3339, "detail": "…", "findings":
// [{"severity", "detail", "fix"}]}. laib reads it fresh on every status,
// /ready and diag, with its age. Check names a oneshot of the service that
// refreshes it: laib starts it, without waiting, when someone looks at diag
// or the Box page and the file is older than Stale (default 5m). A service
// whose own state changes writes the file itself and needs no check.
type StatusFile struct {
	File  string `yaml:"file"`  // under ${DATA_DIR}
	Check string `yaml:"check"` // a .service of the template without [Install]
	Stale string `yaml:"stale"` // a duration, default 5m
}

type Health struct {
	HTTP string `yaml:"http"`
	// Expect names the codes a probe answers when it is not 2xx/3xx (an
	// API that challenges with 401); set, only these are green (#288).
	Expect []int `yaml:"expect"`
	// None says why there is no probe (MQTT speaks no HTTP …): the unit
	// state is the check. Lint wants one of the two (#46).
	None string `yaml:"none"`
}

// Answers says whether a probe's status code means the service is up:
// 2xx/3xx, or exactly the declared codes. 404 is a wrong path, not a live app.
func (h Health) Answers(code int) bool {
	if len(h.Expect) == 0 {
		return code >= 200 && code < 400
	}
	for _, c := range h.Expect {
		if c == code {
			return true
		}
	}
	return false
}

// Port is a published host port; LAN=false renders an nftables drop.
type Port struct {
	Port int      `yaml:"port"`
	LAN  bool     `yaml:"lan"`
	When []string `yaml:"when"` // the port exists only while one of these values is set (#190), like a unit under Contract.When
}

// Value is one entry of values.yml: a literal or a secret reference.
type Value struct {
	Literal string
	Secret  string // set when the value was `!secret NAME`
}

// Set: the value is a non-empty literal. ~, null and blanks load as empty;
// `false` is text and counts; a !secret never does, plan does not know it.
func (svc *Service) Set(name string) bool { return svc.Values[name].Literal != "" }

// Unmet lists what `requires` leaves open: with secrets nil the literal
// values (load), with secrets the !secret ones (plan, render).
func (svc *Service) Unmet(secrets map[string]string) []string {
	var out []string
	for _, v := range slices.Sorted(maps.Keys(svc.Contract.Requires)) {
		if !svc.Set(v) {
			continue
		}
		for _, w := range svc.Contract.Requires[v] {
			switch ref := svc.Values[w].Secret; {
			case secrets == nil && ref == "" && !svc.Set(w):
				out = append(out, fmt.Sprintf("%s is set, so %s must be too", v, w))
			case secrets != nil && ref != "" && secrets[ref] == "":
				out = append(out, fmt.Sprintf("%s is set, so the secret %s must be too (laib secret set %s)", v, ref, ref))
			}
		}
	}
	return out
}

// Service is one services/<name>/ folder.
type Service struct {
	Name     string
	Dir      string
	off      []string // units `when` turned off, and why (#184)
	Contract Contract
	Values   map[string]Value
	Units    map[string]string // file name -> template content with ${VAR}
	Assets   map[string]string // file name -> content copied as is (presets.json …)
}

// State is the whole repo, loaded.
type State struct {
	Dir      string
	Box      Box
	Policy   Policy
	Services map[string]*Service
	Personas map[string]*Persona // the house persona and every chat persona, by name
	Ignore   []string
	// Warnings are findings that do not stop a load but belong in a plan
	// and in a proposal's text: today, two services serving one purpose.
	Warnings []string
	// Off says which units `when` turned off and why; plan prints it (#184).
	Off []string
}
