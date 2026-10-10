# Laib — install on the box

Fedora CoreOS, user `core`, rootless Podman. Nothing here touches a running
service; after the install `laib plan` only *shows* what the box and the
state differ in.

```
curl -fsSL https://raw.githubusercontent.com/mdopp/laib/main/scripts/install-box.sh \
  | bash -s -- --domain <domain> --lan-ip <ip> \
       --model-url http://<gpu-box>:11435/v1 --model-lease http://<gpu-box>:8787/api/model-lease
```

A box with its own GPU passes `--gpu` instead and later adds the `llama`
template as its model role. A box without one (the NUC) points
`--model-url` at a peer's llama proxy or at a cloud API (`https://…`,
then `laib secret set MODEL_API_KEY` before the first apply). Operator,
Bote and Frage read `${MODEL_URL}`, `${MODEL_KEY}` and `${MODEL_LEASE}`.

What it does, in order:

1. Downloads the three release binaries to `/var/opt/laib/`: `laib`,
   `laib-operator` (the admin agent) and `laib-lease` (the model role's
   sidecar). The `/var/opt` prefix is writable on CoreOS; `/opt` is not.
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
