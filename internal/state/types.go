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
	Laib     Laib   `yaml:"laib"`
	// HostNetwork is the closed carve-out list (ServiceBay ADR 0007 carried
	// over, #16): only these services may declare hostNetwork: true.
	HostNetwork []string   `yaml:"hostNetwork"`
	Notify      Notify     `yaml:"notify"`
	Updates     Updates    `yaml:"updates"`
	Registries  []Registry `yaml:"registries"`
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
	After        []string    `yaml:"after"` // services restarted and verified before this one (#12)
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
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Exposure string `yaml:"exposure"` // internal | public
	Auth     string `yaml:"auth"`     // none | one_factor | two_factor
	Group    string `yaml:"group"`
	Caddy    string `yaml:"caddy"`
}

// Secret is a declared secret; non-external ones are generated once.
type Secret struct {
	Name     string `yaml:"name"`
	External bool   `yaml:"external"`
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
}

// State is the whole repo, loaded.
type State struct {
	Dir      string
	Box      Box
	Policy   Policy
	Services map[string]*Service
	Ignore   []string
}
