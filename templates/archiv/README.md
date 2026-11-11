# archiv

The message archive of ADR 0029, one instance per person: `laib add archiv
--person <login>`. The program, its contract and its limits are in
[mdopp/archiv](https://github.com/mdopp/archiv) (`apps/archiv/README.md`).
What the box adds: the one name `archiv.<domain>` (#986), the two folders,
FUSE for the vault, the proxy secret, the status file, and the archive's
own Radicale login `archiv-<person>` (laib#1009): the auth template seeds
it, templates/radicale lets it read that person's phone contacts and own
calendars and nothing else, and its password reaches the container as
`/run/secrets/radicale-password`. For Umfeld (archiv#27, ADR 0029
amendment 2026-11-10) that login also reads the household's address book
`homes/haushalt-kontakte`, and the archive reaches Immich at `IMMICH_URL`
with the person's own API key, which they enter on the archive's page into
the open vault; the template carries none. `IMMICH_PUBLIC_URL` is where the
page opens a photo. A tap on an answer is written server to server to
Homes' own port `ARCHIV_HOMES_INTERN` (never through Caddy), signed with
`ARCHIV_HOMES_SECRET`.

**Mail (archiv#34).** The person enters their own mailboxes on the
archive's page, as many as they have; the template carries none. The
household's document mailbox (the one Paperless reads, a catch-all for
the domain) reaches a person's archive only where the owner gives it:
`DOKUMENTE_MAIL_HOST`, `DOKUMENTE_MAIL_USER` (as Paperless'
`PAPERLESS_MAIL_*`; `DOKUMENTE_MAIL_DOMAIN` defaults to `${DOMAIN}`) in
that instance's `values.yml`, and its password with `laib secret set
DOKUMENTE_MAIL_PASSWORD_<PERSON>` (on stdin), which reaches the container
as the file `/run/secrets/dokumente-mail-password`, never the environment.
The archive then reads only mail addressed to `<person>+doc@<domain>` (or
`+doc+…`), read-only (`EXAMINE`, `BODY.PEEK`), in that one folder, and
never deletes there. Why this way and its price: laib has no secret a
per-person template can share with another template (an instance's
secrets end in `_<PERSON>`), so the password is set per person, which also
makes it the owner's decision for each person; the login reads the whole
mailbox, so the archive's address check, not the provider, keeps another
person's document mail out. A read-only login at the provider, where one
exists, is the narrower key; the narrowest would be a fetcher on
Paperless' side that hands each archive only its person's mail (not
built). Deleting old mail at the provider is the person's own opt-in on
their own mailboxes, in the archive (apps/archiv/README.md); it needs
nothing from the box.

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
