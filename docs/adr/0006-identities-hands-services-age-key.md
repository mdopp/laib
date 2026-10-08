# ADR 0006 — Three kinds of identity: humans via SSO, four hand tokens, service secrets; one `age` key

Applies when you add a token type, a password, a delegation, a request flow, a
pairing code, or a credential self-heal.

- **Status:** Accepted, 2026-10-08

## Context

ServiceBay had about twelve credential kinds: scoped API tokens on a five-rung
ladder, delegated child tokens, a bootstrap token, one-shot token requests,
pairing codes, a delegated admin, an internal token, session cookies, a
dashboard password, and the admin passwords of NPM, AdGuard and LLDAP with
rekey logic to heal them.

## Decision

1. **Humans** have one identity: Authelia + LLDAP. The box page and the
   Werkstatt sit behind it. Laib has no password of its own.
2. **Hands** are four tokens: `resident`, `ops`, `pi`, `claude`. Each has a
   tier ceiling in `policy.yml`; `claude` has a TTL. Minted by
   `laib token mint`. No delegation chains, no request flow: asking for more
   is making a proposal, and the approval is the elevation.
3. **Services** keep their secrets (26 today) in `secrets.age`, generated on
   first apply and never regenerated. Every rendered config carries the value
   from that file, so a wiped service is a restore, not a rekey.
4. **External** credentials (Claude OAuth, GitHub PAT, DNS API) live in the
   same file marked `external: true` and are never generated.
5. The `age` identity is the one secret kept outside the box, on paper.

## Consequences

- `destroy`, `reboot` and `exec` always need a human; a token cannot approve
  its own request. This is carried over from ServiceBay ADR 0009/0013 and
  stays.
- Backups include `secrets.age`; losing the paper key loses the secrets.
