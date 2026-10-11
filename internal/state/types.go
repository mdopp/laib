// Package state loads and validates the Laib state repo (docs/STATE.md).
package state

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
	HostNetwork []string   `yaml:"hostNetwork"`
	Notify      Notify     `yaml:"notify"`
	Updates     Updates    `yaml:"updates"`
	Registries  []Registry `yaml:"registries"`
	// Model is where the LLM is when this box has no model role of its own
	// (a NUC without a GPU): a peer box's llama proxy or a cloud API. The
	// builtins MODEL_URL and MODEL_KEY resolve from here or from the role.
	Model Model `yaml:"model"`
	// Peers are the other boxes of the household (ADR 0018, issue #22).
	Peers map[string]Peer `yaml:"peers"`
	// Operator is the box's admin agent (ADR 0013): a user unit running
	// laib-operator with the ops token from secrets.age.
	Operator OperatorCfg `yaml:"operator"`
}

// OperatorCfg configures laib-operator.
type OperatorCfg struct {
	Enabled  bool   `yaml:"enabled"`
	Model    string `yaml:"model"`    // preset alias, default gemma-4-e4b
	ReportAt string `yaml:"reportAt"` // "07:00"
	Tick     string `yaml:"tick"`     // "10m"
}

// Model is a remote OpenAI-compatible endpoint.
type Model struct {
	URL       string `yaml:"url"`       // e.g. http://192.168.1.20:11435/v1 or https://api.example/v1
	KeySecret string `yaml:"keySecret"` // name in secrets.age; empty for a loopback proxy
	Lease     string `yaml:"lease"`     // e.g. http://192.168.1.20:8787/api/model-lease; empty = no lease
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
	URL string `yaml:"url"`
}

// Laib is the box page and MCP listener itself (ADR 0012): where it listens,
// and the host Caddy publishes it under for admins.
type Laib struct {
	Host   string `yaml:"host"`   // default "laib" → laib.<domain>
	Port   int    `yaml:"port"`   // default 5900
	Listen string `yaml:"listen"` // default 127.0.0.1; "0.0.0.0" lets pods reach /mcp, the nftables drop keeps the LAN out
	Group  string `yaml:"group"`  // Authelia group for the pages, default "admins"
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
}

// Envelope tunes the operator's envelope.
type Envelope struct {
	Snapshot string `yaml:"snapshot"`
}

// Hand is one agent identity.
type Hand struct {
	Ceiling string `yaml:"ceiling"`
	TTL     string `yaml:"ttl"`
}

// Approval says who may approve a tier.
type Approval struct {
	By       string `yaml:"by"`
	Snapshot string `yaml:"snapshot"`
}

// Contract is services/<name>/laib.yml.
type Contract struct {
	Name         string      `yaml:"name"`
	Template     TemplateRef `yaml:"template"`
	Routes       []Route     `yaml:"routes"`
	Secrets      []Secret    `yaml:"secrets"`
	OIDC         *OIDC       `yaml:"oidc"`
	Data         []DataPath  `yaml:"data"`
	Health       Health      `yaml:"health"`
	Ports        []Port      `yaml:"ports"`
	GPU          bool        `yaml:"gpu"`
	HostNetwork  bool        `yaml:"hostNetwork"`
	After        []string    `yaml:"after"`    // services restarted and verified before this one (#12)
	Provides     []string    `yaml:"provides"` // purposes this service serves: photos, media, dns … (one per purpose by default)
	MigratedFrom string      `yaml:"migratedFrom"`
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
	ClientID     string   `yaml:"clientId"`
	RedirectURIs []string `yaml:"redirectUris"`
	Scopes       []string `yaml:"scopes"`
}

// DataPath is a path to back up (or explicitly not).
type DataPath struct {
	Path   string `yaml:"path"`
	Backup *bool  `yaml:"backup"`
}

// Health is what apply verifies.
type Health struct {
	HTTP string `yaml:"http"`
}

// Port is a published host port; LAN=false renders an nftables drop.
type Port struct {
	Port int  `yaml:"port"`
	LAN  bool `yaml:"lan"`
}

// Value is one entry of values.yml: a literal or a secret reference.
type Value struct {
	Literal string
	Secret  string // set when the value was `!secret NAME`
}

// Service is one services/<name>/ folder.
type Service struct {
	Name     string
	Dir      string
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
	Ignore   []string
	// Warnings are findings that do not stop a load but belong in a plan
	// and in a proposal's text: today, two services serving one purpose.
	Warnings []string
}
