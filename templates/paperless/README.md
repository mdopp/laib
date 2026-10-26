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
Zahlungsintervall. The family fills them, and so does the timer below.

## Contract data and notice reminders (#709)

`paperless-vertraege.timer` runs `paperless-vertraege.py` hourly inside the
Paperless container, as the paperless user, with Paperless' own admin and AI
settings (the household model through the proxy, marked
`X-Laib-Background: paperless`, so it never counts as a person asking).

- **Which documents.** A contract is a document whose document type or one
  of whose tags is named in `VERTRAG_ARTEN` (default `Vertrag, Versicherung,
  Bescheid`; the household's own names, compared without case).
- **Reading.** A contract with an empty contract field is read once per
  text, at most ten a run: the model answers JSON, per field a value and
  the words of the text it stands on. A value is taken only when those
  words are in the text (a name or number also in those words) and it has
  its field's shape: a date, an amount (`EUR84.50`), a period
  (`3 Monate`), one of monatlich, vierteljährlich, halbjährlich, jährlich,
  einmalig. Otherwise the field stays empty, the document gets the tag
  `prüfen` (made once, no automatic matching, no owner) and its note says
  why. **Only empty fields are filled**: a value someone set is never
  changed; a note on the document names what was filled. While the model
  does not answer (a window holds the card), the run stops reading and the
  next one asks again. A document recognised again (a new text) is read
  again, still only for its empty fields.
- **Reminding.** For a contract with *Nächster Kündigungstermin* and a
  *Kündigungsfrist* it can read (a number and Tage, Wochen, Monate or
  Jahre: "drei Monate zum Ende der Laufzeit" is 3 months), the notice must
  be in by the notice date minus the period. `VERTRAG_VORLAUF_TAGE` (28)
  before that, a reminder in Haus's Haushalt (`erinnerung_anlegen`: a task
  with an alarm in the family calendar, on every phone and on Haus'
  Kalender page), e.g. „Kündigung prüfen: Stromvertrag (Stadtwerke …).
  Kündigen bis 31.12.2026 (Kündigungstermin 31.03.2027).
  https://paperless.…/documents/12/details“. It is made as soon as both
  fields are there, once per document and notice date; one already due is
  due today; one whose notice date minus the period has passed is not
  made. A period it cannot read is a line in the unit's journal. Without
  Haus (`HAUS_HAUSHALT_TOKEN`) no reminder is made and none is recorded,
  so the first run after `laib add haus` makes them.
- **The record.** `laib-vertraege.json` in Paperless' data folder (backed
  up): per document what was filled and not taken, per reminder its day.
  `journalctl --user -u paperless-vertraege` has one line per document and
  reminder.

Not here (the follow-up issue): the Bote telling a reminder over Signal,
renewing a notice date after it passed, questions to the documents through
the Bote or Haus, keeping the household's own people out of the AI's
correspondent suggestions (Paperless 3.0 has no setting for it), and a check
for unusable text.

A document recognised before keeps its text: to recognise it again, select
it in Paperless and reprocess it from the document actions (the values'
mode applies).
