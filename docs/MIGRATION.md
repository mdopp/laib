# Laib — the one migration from ServiceBay

Only one box runs ServiceBay. So: no compatibility layer, no dual control
plane for months, no feature flags. One weekend, one order, one script that
reads ServiceBay's config once.

## Principle

A service belongs to exactly one control plane, recorded in
`services/<name>/laib.yml` (`migratedFrom: servicebay` until the end). While a
service is still ServiceBay's, Laib shows it as `nicht verwaltet` and does not
touch it. While a service is Laib's, ServiceBay's unit for it is disabled.

## Order

1. **Laib binary + state repo**, nothing applied. `laib init --domain …`,
   then `scripts/migrate-servicebay.py --config /mnt/data/servicebay/config.json
   --templates <servicebay>/templates --out /mnt/data/laib/state` writes a draft
   `services/<name>/` (laib.yml with routes from `reverseProxy.hosts`, secrets
   and ports from `variables.json`, values.yml from `installedVariables`) for
   every installed template and prints what it could not map. The drafts are
   reviewed by hand; the Quadlet files are written from each template's Pod
   YAML by the agent in the Werkstatt (ADR 0004: `.container` is the primary
   form). Secrets: `laib secret set` for the external ones (DNS token, SMTP,
   Fritz!Box, GitHub, Claude OAuth); everything else is re-generated on first
   apply and the post-deploy hook re-seeds the app. Nothing runs yet.
2. **Caddy beside NPM** on a different port, `laib apply caddy`. Routes are
   verified against `127.0.0.1` before DNS moves. Then AdGuard rewrites
   switch, NPM's unit is disabled. SSO forward-auth now goes through Caddy;
   Authelia's config is still ServiceBay's at this point and stays compatible
   because the rules are Authelia's, not the proxy's (ServiceBay ADR 0005, 0006).
3. **auth** (Authelia + LLDAP): `laib apply auth`. OIDC clients are rendered
   from every `services/*/laib.yml`, which at this point is the full list,
   so no client is dropped (the #1724 failure class cannot occur).
4. **adguard**, then **vaultwarden, radicale, mosquitto, file-share, beets,
   immich, media, home-assistant** in that order: small and stateless first,
   the ones with migrations last. Each one: `laib plan <name>`, read the diff,
   `laib apply <name>`, verify green, disable ServiceBay's unit.
5. **Werkstatt** replaces `claude-dev` + `pi-web`: one template, same
   `/workspace` volume, so checkouts and sessions survive.
6. **Solaris**: `llama` and `solaris` templates move last; the `ops` profile
   gets its token from `laib token mint ops`.
7. Disable `servicebay.service`. Keep its data dir for 30 days, then delete.
   ServiceBay's assist catalog is imported into `docs/assists/` of this repo
   by `laib migrate assists` (text only, secrets scanned).

## What does not move

- The 28 JSON stores, the twin, the approval and audit stores. The journal
  starts empty on day one; the old audit log stays in the ServiceBay data dir
  for 30 days.
- ServiceBay API tokens. All agents get new hand tokens.
- The TUI, the portal, the companion app's `/napi` (Solaris owns the app, ServiceBay ADR 0011).

## Rollback during the weekend

Every `laib apply` snapshots first. Re-enabling a ServiceBay unit is
`systemctl --user enable --now <unit>`; NPM comes back the same way until step
7. After step 7, rollback is a restore of the ServiceBay data dir, which is
why it stays 30 days.
