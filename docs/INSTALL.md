# Laib — install on the box

Fedora CoreOS, user `core`, rootless Podman. Nothing here touches a running
service; after the install `laib plan` only *shows* what the box and the
state differ in.

```
curl -fsSL https://raw.githubusercontent.com/mdopp/laib/main/scripts/install-box.sh \
  | bash -s -- --domain <domain> --lan-ip <ip>
```

What it does, in order:

1. Downloads the release binary to `/var/opt/laib/laib` (the `/var/opt`
   prefix is writable on CoreOS; `/opt` is not).
2. Creates `/mnt/data/laib` owned by `core`.
3. Installs `dist/laib.service` as a **user** unit and enables linger so it
   runs without a login session.
4. Runs `laib init --domain …` once: the `age` key, the state repo with
   `box.yml` and `policy.yml`, the first journal line. **Print the key**
   (`/mnt/data/laib/age.key`) and keep it outside the box (ADR 0006).
5. Starts the daemon and prints `/ready`.

For a fresh box the same script runs from Ignition (`systemd.units` entry
that calls it once on first boot); for the migration it runs beside
ServiceBay.

## After the install (migration step 1, docs/MIGRATION.md)

```
laib plan                       # every ServiceBay unit shows as 'nicht verwaltet'
for u in $(laib plan --json | jq -r '.unmanaged[]'); do laib ignore "$u"; done
laib token mint ops             # for Solaris; paste into its ops profile
laib token mint claude --ttl 8h # for a Claude session
```

Then Caddy beside NPM (step 2). Nothing before that changes a service.

## Updating

Re-run the install script with `--version vX.Y.Z` or without for latest.
The unit restarts; state and journal are untouched.
