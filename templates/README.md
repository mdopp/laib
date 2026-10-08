# Built-in templates

A template is a folder: Quadlet files with `${VAR}` placeholders, a `laib.yml`
contract and a `values.example.yml` (ADR 0004). Install one by copying the
folder to `services/<name>/` in the state repo, renaming
`values.example.yml` to `values.yml`, filling it, and running `laib plan`.

`laib template lint templates/<name>` validates a folder exactly like the
state loader would.

| Template | Role | Notes |
|---|---|---|
| caddy | proxy | host network (the one carve-out), reads `render/caddy/Caddyfile` |
| authelia | sso | reads `render/authelia/*.yml` next to its own configuration |
| whoami | — | the smallest complete example: route, SSO, OIDC, secret, LAN-blocked port |
