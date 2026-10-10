# bote — the agent you talk to (ADR 0016)

Hermes **as stock** (`nousresearch/hermes-agent`), configured only through
the post-deploy hook, plus signal-cli's REST API in the same pod. Nothing
is forked. If this template ever needs more than configuration, that is
the signal to build a lean Bote instead (ADR 0016 §2).

## Once, by hand

1. Link the Bote's phone number: `laib exec bote --container systemd-bote-signal -- signal-cli link -n Bote`
   and scan the QR code in Signal → Linked Devices on the phone that owns
   that number (a second SIM or a spare phone; the Bote should not share
   the admin's own number).
2. Tokens: `laib token mint bote` → `laib secret set BOTE_LAIB_TOKEN`;
   `laib token mint human --note "signal +49…"` → `laib secret set BOTE_HUMAN_TOKEN`.
   The human token is used only through the `laib-admin` MCP entry, which
   Hermes marks `untrusted`: every write asks, and the SOUL says it may be
   used only on the admin's own message.
3. Pairing: the first message from an unknown number yields a pairing code;
   approve with `laib exec bote -- hermes pairing approve signal <CODE>` or
   list the number in `SIGNAL_ALLOWED_USERS`.

## What is verified and what is not

Verified from the Hermes docs (2026-10): the image and its `/opt/data`
volume, the Signal env keys (`SIGNAL_HTTP_URL`, `SIGNAL_ACCOUNT`,
`SIGNAL_ALLOWED_USERS`, `SIGNAL_HOME_CHANNEL`), `mcp_servers` with `url`,
`headers` and `trust`, `${VAR}` references resolved from `.env`,
`model.base_url`/`api_mode` and the `providers:` dict, `gateway run`,
`non_interactive_hard_stop_enabled`. Not yet verified on a box: the exact
`providers.<name>.api` key for an OpenAI-compatible endpoint and the
`model_aliases` shape — run `hermes model` once inside the container and
compare with what it writes. Issue: the Bote on the NUC (#23, phase 2).
