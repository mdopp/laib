# archiv

The message archive of ADR 0029, one instance per person: `laib add archiv
--person <login>`. The program, its contract and its limits are in
[mdopp/archiv](https://github.com/mdopp/archiv) (`apps/archiv/README.md`).
What the box adds: the one name `archiv.<domain>` (#986), the two folders,
FUSE for the vault, the proxy secret, the status file, and the archive's
own Radicale login `archiv-<person>` (laib#1009): the auth template seeds
it, templates/radicale lets it read that person's phone contacts and own
calendars and nothing else, and its password reaches the container as
`/run/secrets/radicale-password`.

No agent path by default (laib#1121, security plan item 12): an instance
gets `archiv.<domain>/p/<person>/` past Authelia, for an agent with the
instance's msgvault key, only when its `values.yml` sets `AGENT_PATH`.
Off, the person's login is the archive's only door, and `laib net` shows
no bypass on the name.

Proven on the Kraftwerk on 2026-11-02: gocryptfs mounts in rootless podman
on Fedora CoreOS with `/dev/fuse` and `SYS_ADMIN`; the first Signal backup
(41,437 messages) and the Gmail mailbox went in through it.

The inbox folder carries one file the archive writes itself:
`.briefkasten.pub`, the public key Homes hands to the phone, so the Homes app
encrypts SMS and calls before they leave it (ADR 0029 §9).
