# Laib ADRs

Status · Context · Decision · Consequences. One file per decision, next free
number: **0011**. Each file's first paragraph after the title says *in which
situation* it applies, so an agent can self-select.

| # | Decision |
|---|---|
| [0001](0001-agent-first-state-repo-one-verb.md) | Agent-first: a Git state repo is the truth, `plan`/`apply` is the one verb, no LLM code in the kernel |
| [0002](0002-quadlet-native-single-binary-go.md) | systemd runs the containers via Quadlet; Laib is one static Go binary on the host |
| [0003](0003-no-ssh-agent-laib-runs-on-the-host.md) | No SSH remote agent: Laib runs on the host and reads podman/journald directly; one box |
| [0004](0004-templates-are-contracts.md) | Templates are Quadlet files plus a `laib.yml` contract, not forms and generators |
| [0005](0005-test-pyramid-verify-in-apply.md) | Golden renderers, one booting integration job, verify inside apply; no gates until recurrence |
| [0006](0006-identities-hands-services-age-key.md) | Three kinds of identity: humans via SSO, four hand tokens, service secrets; one `age` key |
| [0007](0007-caddy-replaces-npm.md) | Caddy replaces Nginx Proxy Manager; the Caddyfile is rendered from state |
| [0008](0008-unmanaged-is-drift.md) | An unmanaged unit is drift: adopt or ignore, no bundle builder |
| [0009](0009-hermes-threshold.md) | Hermes is not adopted; the threshold under which it would be |
| [0010](0010-one-migration-no-compat.md) | One user, one migration, no compatibility layer |
