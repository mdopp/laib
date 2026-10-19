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

## Amendment 2026-10-19 (#380): how a long-running container holds a TTL hand

`claude` has a TTL because it belongs to a session, not to a service. The
Werkstatt is a long-running container whose Claude sessions come and go
(#383), and it held `LAIB_CLAUDE_TOKEN` as a service secret in
`secrets.age`: after 8 h every session lost Laib and the owner minted a new
one by hand. A week-long token at rest was the workaround.

Decision: a hand may name one other hand in `session:` in `policy.yml`.
Its only power, through the verb `session`, is to get a token of that hand
for one session. The token carries that hand's TTL (a session hand must
have one, may not be `human`, and has no `session:` itself, so there is no
chain), is journaled, and is listed with the session's name. `laib init`
ships `werkstatt: {ceiling: read, verbs: [session], session: claude}`. The
Werkstatt's `LAIB_WERKSTATT_TOKEN` is minted on `laib add` (`mint:
werkstatt`). It is the one long-lived token there, and it can neither read
the box nor change it. Each Claude session asks for its own `claude` token
when it connects and keeps it in the container's tmpfs.

This is the one exception to "no delegation chains, no request flow". It
is one level deep, the target and its TTL are fixed in `policy.yml` (a
human's file), and asking for a session is not asking for more: the
session gets exactly the `claude` hand. Revoking the `werkstatt` token
ends new sessions. Revoking one session token ends that session.
