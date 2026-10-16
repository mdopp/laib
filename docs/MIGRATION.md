# Laib — the one migration from ServiceBay

Only one box runs ServiceBay. So: no compatibility layer, no dual control
plane for months, no feature flags. One weekend, one order, one script that
reads ServiceBay's config once.

## Running side by side

Both run at once: ServiceBay in its container on 5888, Laib as a user
service on 5900; separate state, separate nftables tables
(`servicebay_lanblock` / `laib`), separate tokens. ServiceBay's `<name>.kube`
files sit in the Quadlet root, Laib's units in `<name>/` folders. Three
things cannot be shared:

1. **A service.** Quadlet names units after the file, not the folder, and
   ServiceBay's `auth.kube` runs the same Authelia and LLDAP on the same
   data as Laib's `auth/` units, whatever those are called. `laib plan`
   reports `KONFLIKT` for every file in the Quadlet root that would run a
   service of the state beside Laib: the same name or unit name, a host port
   the service publishes, or one of its backed-up data paths (a `.kube` is
   read with its `Yaml=`; the `backup: false` shared tree is shared on
   purpose). While one is reported, every `laib apply` refuses, so
   ServiceBay's unit is stopped and its files moved out **before** the
   service goes into the state (step 4).
2. **Ports 80/443.** Caddy starts on alternative ports beside NPM (step 2).
3. **Whole-file configs.** Authelia, AdGuard rewrites and the SSH port 2222
   (claude-dev vs Werkstatt) move as a whole, never half.

## Principle

A service belongs to exactly one control plane. It is ServiceBay's while its
file is in the Quadlet root and Laib's state does not have it; it is Laib's
from the moment ServiceBay's unit is stopped, its file moved out and
`laib add <name>` puts it in the state. There is no marker and no in-between:
the `KONFLIKT` check above keeps the two apart (`migratedFrom` was dropped,
#227; ADR 0010: the kernel knows nothing about ServiceBay). A Quadlet unit
cannot be disabled while its file is there, so "disable" always means the
file moves out.

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
3. **auth** (Authelia + LLDAP), by the a–c of step 4: ServiceBay's
   `auth.kube` out first, then `laib add auth`. OIDC clients are rendered
   from every `services/*/laib.yml`, which at this point is the full list,
   so no client is dropped (the #1724 failure class cannot occur).
4. **adguard**, then **llama** as the `model` role (ADR 0011): the template
   moves from solarisbay into Laib with the lease sidecar; Its consumers
   (foundry-chronicle now, the Werkstatt's pi from step 5) speak Laib's lease contract (ADR 0011,
   amendment 2026-10-14): a preset instead of a window name, a holder, the
   model key. Then **vaultwarden, radicale, mosquitto, file-share, beets,
   immich, media, home-assistant** in that order: small and stateless first,
   the ones with migrations last. Each one, in this order (the service is
   down from a to c; that is the price of never having two writers on one
   database):
   a. Stop ServiceBay's unit and move its files out of the Quadlet root:
      ```sh
      systemctl --user stop <name>.service
      mkdir -p ~/servicebay-units
      mv ~/.config/containers/systemd/<name>.kube ~/.config/containers/systemd/<name>.yml ~/servicebay-units/   # or <name>.container
      systemctl --user daemon-reload
      ```
   b. Carry its secrets over (#228) and move its data into Laib's layout
      (#229).
   c. `laib add <name>` (it applies). `laib plan` shows no `KONFLIKT`, the
      apply ends green, the app shows the old data.
5. **Werkstatt** replaces `claude-dev` + `pi-web`: one template, same
   `/workspace` volume, so checkouts and sessions survive.
6. **Solaris does not move** (it retires, ADR 0015). Its units stay
   ignored (`laib ignore`) while Haus's bridge still needs them (haus#10);
   they go off in Phase 5 (#160).
7. Disable `servicebay.service`. Keep its data dir for 30 days, then delete.
   ServiceBay's assist catalog: there is no import tool yet; what happens to
   it is decided in #160.

## What does not move

- The 28 JSON stores, the twin, the approval and audit stores. The journal
  starts empty on day one; the old audit log stays in the ServiceBay data dir
  for 30 days.
- ServiceBay API tokens. All agents get new hand tokens.
- The TUI, the portal, the companion app's `/napi` (Haus owns it now; until haus#10 the rest goes through Haus's Solaris bridge).

## Rollback during the weekend

Every `laib apply` snapshots first. Handing a service back to ServiceBay is
`laib remove <name>`, then its files back from `~/servicebay-units/` into the
Quadlet root, `systemctl --user daemon-reload`, `systemctl --user start
<name>.service`; NPM comes back the same way until step 7. After step 7,
rollback is a restore of the ServiceBay data dir, which is why it stays 30
days.
