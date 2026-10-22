# paperless — the household's documents

Paperless-ngx with OCR, Postgres and Redis in one pod. The family logs in
through Authelia's OIDC; the local admin stays as break-glass. Ways a
document gets in, each ending in Paperless with its text recognised and,
through Haus' library, findable in Haus and by the Bote (haus#13):

| Way | Where it lands | Title, tags |
|---|---|---|
| E-mail to the inbox address (`PAPERLESS_MAIL_FILTER_TO`) | Paperless fetches it itself | the subject; the attachments only |
| The Bote, a photo or PDF with "ablegen" or "archivieren" | `paperless-consume/Bote/<sender>/` | the words besides "ablegen", else sender and time; tags Bote and the sender |
| Haus, "Dokument ablegen" (page and app) | `paperless-consume/` | the file's name |
| The PC: File Browser or the network drive | `paperless-consume/`, any subfolder | the file's name; each folder on the way is a tag |

## The consume folder from a PC

`paperless-consume` is a folder of the file share's data tree
(`${DATA_DIR}/file-share/data/paperless-consume`, the value `CONSUME`), and
both of the share's doors show that whole tree:

- **File Browser**, `https://files.<domain>` with the family login: the
  folder `paperless-consume` at the top. Drag files in.
- **Network drive** (Samba, LAN only; each person's password on Haus'
  Netzlaufwerk page): `\\<box>\data\paperless-consume` on Windows,
  `smb://<box>/data/paperless-consume` on a Mac or phone.

A file dropped there is filed within a minute and then removed from the
folder; a file that stays was refused (Paperless' log says why, often a
duplicate). Folders are consumed too (`PAPERLESS_CONSUMER_RECURSIVE`) and
their names become tags (`PAPERLESS_CONSUMER_SUBDIRS_AS_TAGS`, #521): drop
`Steuer/2026/` and its files get the tags Steuer and 2026. The empty folders
stay behind.

## The mail inbox (#521)

Paperless fetches one IMAP mailbox every ten minutes and files the
attachments of mail addressed to `PAPERLESS_MAIL_FILTER_TO`, with the
subject as the title and no owner (the whole family sees them); then it
flags the mail, and a flagged mail is never fetched again. A mailbox whose
address is an alias of another (Strato: `dokumente@` an alias of
`webmaster@`) is the usual case: the login is the real mailbox, the filter
the alias, and nothing else in that mailbox is touched.

post-deploy makes the account and the rule through Paperless' API on every
apply, from the values (`paperless-mail.py setup`), and takes over an
account and rule made by hand for the same mailbox and address. With
`PAPERLESS_MAIL_HOST` empty it changes nothing in Paperless and does not
ask its API at all. It logs in as `ADMIN_USER`; after a move from
ServiceBay the database keeps ServiceBay's administrator (Paperless makes
`ADMIN_USER` only in an empty one), so a refused login is a red finding in
diag with the fix, not a red apply: Paperless runs, only the inbox waits.

Once, as the owner, on the box:

```sh
laib secret set PAPERLESS_MAIL_PASSWORD        # the mailbox's password, typed on stdin
# services/paperless/values.yml:
#   PAPERLESS_MAIL_HOST: imap.strato.de
#   PAPERLESS_MAIL_USER: webmaster@example.org
#   PAPERLESS_MAIL_FILTER_TO: dokumente@example.org
laib plan && laib apply paperless
```

`laib plan` refuses a host without user or password (`requires:`).
Each apply ends by asking Paperless to fetch now, in its own worker.
`laib diag` shows the inbox's state from `paperless-check.service`
(Paperless' own connection test with the stored password, hourly and when
someone looks): red when Paperless cannot log in to the mailbox, or the
account or rule is missing; yellow, with the file and Paperless' reason,
for every document Paperless could not file and still lists as a failed
task (from a mail, the consume folder or an upload alike). What it does
not see: a failure someone dismissed, and a filter address that no mail is
sent to.

**Never fetch by hand as root.** `podman exec systemd-paperless python3
manage.py mail_fetcher` runs the fetch as root: the attachment lands in a
`0700` folder of root's under `/tmp/paperless`, the worker (uid 1000)
cannot read it, and the document fails with `Permission denied` (seen
2026-10-22). To fetch now: Paperless → Einstellungen → E-Mail → the
account's "Verarbeiten", or `laib apply paperless`; on the box, the image's
own wrapper `podman exec systemd-paperless mail_fetcher`, which drops to the
paperless user. `paperless-check.service` itself execs `--user paperless`.

**A mail whose attachment failed** is not fetched again by itself:
Paperless records it as processed (failed) and does not flag it. To file
it: Einstellungen → E-Mail, at the rule open the processed mails, delete
the failed entry; the next fetch takes the mail again. Then dismiss the
failed task under Dateiaufgaben, which turns diag's yellow off.
