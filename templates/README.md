# Built-in templates

A template is a folder: Quadlet files with `${VAR}` placeholders, a `laib.yml`
contract and a `values.example.yml` (ADR 0004). Install one by copying the
folder to `services/<name>/` in the state repo, renaming
`values.example.yml` to `values.yml`, filling it, and running `laib plan`.

`laib template lint templates/<name>` validates a folder exactly like the
state loader would.

| Template | Role | Notes |
|---|---|---|
| adguard | dns | AdGuard Home on the host network; the hook writes the first config with the admin user and syncs laib's rewrites via the API |
| caddy | proxy | host network (the one carve-out), reads `render/caddy/Caddyfile` |
| auth | sso, identity | Authelia + LLDAP in one pod; the hook writes configuration.yml, the OIDC key and seeds LLDAP |
| whoami | — | the smallest complete example: route, SSO, OIDC, secret, LAN-blocked port |
| werkstatt | — | v0 of the one dev container: Claude Code, pi, repos, sshd for the mobile app; Laib as MCP for every session |
| bote | — | Hermes as stock with signal-cli: the agent you talk to; config rendered by the hook (ADR 0016) |
| frage | — | "was ist …": SearXNG + model summary; /ask and an OpenAI-compatible endpoint for Assist and the Bote (ADR 0015) |
