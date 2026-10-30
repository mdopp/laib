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

## Logins and groups (#231)

The family logs in with "Authelia" on Paperless' login page. Paperless
makes the account on the first login and, on every login, sets its groups
to those of Authelia's `groups` claim that Paperless has. post-deploy
(`paperless-mail.py setup`, through Paperless' API as `ADMIN_USER`) makes
the household's two groups from `templates/auth` on every apply, so a
login has permissions without anyone clicking them together:

| Group | Permissions |
|---|---|
| `admins` | everything of Paperless' own: documents and their metadata, users, groups, mail, workflows, custom fields, settings, history, statistics |
| `family` | view, add, change, delete documents, tags, correspondents, document types, storage paths, saved views, notes, share links and their own UI settings; see and dismiss tasks; see custom fields |

The family gets no mail permissions: the inbox comes from the values, and
a rule changed by hand is overwritten on the next apply. Nor workflows,
custom field definitions, users or groups: those change how Paperless files
for everyone, which is the admins' call. A group is made when missing and
given what it lacks; a permission added by hand stays. Only permissions
this Paperless has are given, so an older or newer one still applies.

Paperless sets a login's groups when it logs in: an account that logged in
before the groups existed gets them on its next login (log out, log in
again). A document belongs to who uploaded it through the web page and is
then only theirs (share it, or give it no owner); mail and the consume
folder file without an owner, for the whole family.

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

## The AI and the text recognition (#567)

Paperless' AI (3.0: suggestions for title, correspondent, type, tags and
dates; the document chat) runs on the household model through the model
role, as every other consumer's model does: `PAPERLESS_AI_LLM_ENDPOINT` is
`${MODEL_URL}` (the proxy at `host.containers.internal:11435/v1`), the key
`${MODEL_KEY}`. The embeddings for the chat's index go to the same proxy,
which hands `/v1/embeddings` to llama-embed (owner, 2026-10-24: through the
lease, no port opened to pods). While a window that stops llama-embed
stands (`full`), the proxy refuses them with 409 and Paperless' nightly
index (`10 2 * * *`) waits for the next night.

| Value | Default | What it does |
|---|---|---|
| `AI_ENABLED` | `"true"` | the AI on; `"false"` on a box without a model, else post-deploy stops: Paperless' client without an endpoint asks OpenAI's |
| `AI_MODEL` | `qwen3.6-35b-a3b` | the household model; another needs a lease Paperless does not take |
| `AI_LANGUAGE`, `AI_CONTEXT`, `AI_TIMEOUT` | `de`, `16384`, `300` | the suggestions' language, the tokens of a document read, the seconds Qwen gets |
| `AI_EMBED_BACKEND`, `AI_EMBED_MODEL` | `openai-like`, `embeddinggemma` | llama-embed through the proxy; `huggingface` and a sentence-transformers model run on Paperless' CPU instead |
| `AI_EMBED_CHUNK` | `"300"` | tokens per piece: llama-embed refuses a piece beyond its 512-token batch |
| `OCR_MODE` | `redo` | every page recognised again, a born-digital PDF's own text kept; `auto` kept a scanner's letter salad on 11 of 19 documents; `force` rasterises every PDF |
| `OCR_ROTATE_PAGES`, `OCR_DESKEW` | `"true"` | upside-down and crooked scans (Paperless skips deskew under `redo`) |

Paperless takes a field of its own settings (Einstellungen → Konfiguration)
over the environment, so post-deploy clears the AI and OCR fields these
values set: a field changed there by hand is reset on the next apply, the
others stay. It also makes the contract fields once (custom fields, found
by name, never changed): Anbieter, Vertragsnummer, Vertragsbeginn,
Vertragsende, Kündigungsfrist, Nächster Kündigungstermin, Betrag,
Zahlungsintervall, and for invoices (#818) Rechnungsnummer and Fällig am
(Betrag is the contracts' field). The family fills them, and so does
Dokumente (below).

## Dokumente (ADR 0028): contracts and invoices

Reading contracts and invoices, the notice reminders and the rest of what
was `paperless-vertraege.timer` (#709, #721, #737, #818, #820) is
Dokumente's now (`mdopp/dokumente`, run by the scan template, see its
README). Paperless' side, made by post-deploy (`paperless-mail.py setup`)
on every apply, each step changing only what differs:

- **The user `dokumente`**, no administrator, no staff, in no group, with
  exactly these rights: view, add and change documents (add: a „nur für
  mich“ stack is uploaded through the API and handed to its person), view and add notes and
  tags, view correspondents, document types, custom fields and users (so a
  household person is never taken for a sender), and its own UI settings
  (`view_uisettings`: Paperless guards `/api/ui_settings/` with it, where a
  client reads which permissions it has; without it Paperless answers 403
  and a „nur für mich“ stack never starts). Its password is the box's
  `PAPERLESS_DOKUMENTE_PASSWORD`; a different one is set back.
- **Its token**, Paperless' own (`/api/token/`), handed back as
  `PAPERLESS_TOKEN`, the scan service's secret, only when it differs.
- **The workflow „Dokument hinzugefügt“**: on every document added (consume
  folder, API, mail, web page) it gives `dokumente` view and change on it,
  then posts to `http://host.containers.internal:8720/hook` with the header
  `X-Dokumente-Token` (`DOKUMENTE_WEBHOOK_TOKEN`, generated here). Found by
  name; one that differs is made again.
- **Documents a person owns** (an upload through the web page is only
  theirs): `dokumente` gets view and change on them, merged, so the owner's
  and everyone else's stay.
- **The record moves once**: `laib-vertraege.json` from Paperless' data
  folder to `${DATA_DIR}/dokumente` (backed up), while Dokumente has none; the old file stays
  for a rollback.

The contract and invoice fields (custom fields) are still made here, above,
and so is **`Stapel`** (text, #909): Dokumente writes into it, on every
document it files, the id of the scanned stack the document came from (the
stack's start time, for example `2026-10-29T17-07-46`). Haus lists a stack's
documents by it straight from Paperless, so a document renamed, split or
merged there stays under its stack and a deleted one is gone.

Not here (follow-up issues): questions to the documents through the Bote
or Haus; upstream, a Paperless setting for names its AI must not suggest.

A document recognised before keeps its text: to recognise it again, select
it in Paperless and reprocess it from the document actions (the values'
mode applies); Dokumente does that by itself for letter salad.

## Gesundheit (laib#893): medical records

Gesundheit (`mdopp/gesundheit`, its own template) reads lab sheets, reports
and breath tests into a timeline per person. It is Paperless' second client
and gets exactly Dokumente's shape from this template's post-deploy
(`paperless-mail.py kunden`):

- **The user `gesundheit`**, no administrator, with Dokumente's rights
  (`DOKUMENTE_RECHTE`: documents, notes, tags, the names it reads), its
  password `PAPERLESS_GESUNDHEIT_PASSWORD` generated here; its token minted
  from `/api/token/` and handed back as `PAPERLESS_GESUNDHEIT_TOKEN` (the
  gesundheit template declares it `external, optional`, so the two template
  updates apply in either order).
- **The one workflow** „Dokument hinzugefügt“ gives both users view and
  change on each new document and carries one webhook action per client:
  Dokumente's to 8720 with `X-Dokumente-Token`, Gesundheit's to
  `host.containers.internal:18529/hook` with `X-Gesundheit-Token`
  (`GESUNDHEIT_WEBHOOK_TOKEN`, generated by the gesundheit template; without
  it only Dokumente's action, Gesundheit reads hourly).
- **The document types** of `PAPERLESS_BEFUND_ARTEN` (Befund, Arztbrief,
  Labor, Röntgen, Atemtest, Lungenfunktion) are made when missing, found by
  name, never renamed; they belong to nobody (`owner: null`), because a type
  the admin owns is invisible to `dokumente` and `gesundheit` (one owned
  from before is handed to everybody once). Paperless' AI suggests them,
  Gesundheit reads documents of these types (or tags of the same names).
- Without `PAPERLESS_GESUNDHEIT_PASSWORD` (the gesundheit template not
  installed) nothing of this is made and post-deploy says so; Dokumente goes
  on alone.
