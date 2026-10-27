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
(Betrag is the contracts' field). The family fills them, and so does the
timer below.

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
- **Invoices** (#818). An invoice is a document whose type or one of whose
  tags is named in `RECHNUNG_ARTEN` (default `Rechnung`), or whose title
  starts with the scan service's art „Rechnung“ (`2026-05-04 Absender -
  Rechnung Fassade`, also `Rechnung 4711`; not „Mahnung zur Rechnung“, not
  „Rechnungskorrektur“). Scanned and mailed ones alike: a mail's invoice
  counts by its type or tag, or a subject that starts with „Rechnung“. It
  is read like a contract, in the same run and the same ten, for three
  fields: **Betrag** (the sum the invoice asks for, `EUR1234.56`),
  **Rechnungsnummer** (a number OCR split across lines, „Rech.-Nr.: 2026/“
  „0042“, is joined, never guessed together) and **Fällig am**. The amount
  must be one its words name („1.234,56 €“, „EUR 87,00“), in euro and above
  zero: words of a credit note or refund (Gutschrift, Guthaben, erstatten),
  a minus sign or another currency leave it empty with `prüfen` and a note
  saying why, never silently. A due date must be a day its words name
  („zahlbar bis 18.05.2026“, „fällig am 30.06.2026“). A payment term
  („Zahlungsziel 14 Tage“) counts from the invoice's date, when its words
  are in the text too, and the note says so (Rechnungsdatum 02.03.2026 und
  „Zahlungsziel 14 Tage“); a term counted from a day the text does not name
  (receipt, delivery) or without an invoice date leaves Fällig am empty and
  the document `prüfen`. **Verwendungszweck** (#820) is the reference the
  invoice asks to give with the payment („Bitte als Verwendungszweck
  angeben: …“, „Kassenzeichen“, „Zahlungsreferenz“, a direct debit's
  „Mandatsreferenz“ too): its words must name one, and never the household's
  own line (the recipient block, „Ihr Zeichen“, „Ihre Referenz“). An invoice
  that names none leaves it empty, without `prüfen` and never with the
  invoice number in its place. An invoice read before #820 is asked once
  more for it (its filled fields stay). A document both contract and invoice is asked for
  both, its Betrag as the invoice's. Without the two invoice fields (before
  `laib apply paperless` made them) contracts are read and invoices wait.
- **The family address book** (#820). With Haus, the same question per
  contract and invoice also asks for the sender: firm, contact person,
  phone, e-mail, IBAN and postal address (letterhead, return-address line or
  footer), plus the recipient block. A value is kept only when its words
  are in the text and it has its shape; the firm is the sender, never the
  household: a value in the recipient block, a household person (as above,
  #737), a login's e-mail, or a number, mail or IBAN the text calls the
  recipient's („Ihre Rufnummer“, „von Ihrem Konto … abgebucht“, Lastschrift,
  Mandat) is not taken, nor a street with a household person's name just
  before it (OCR runs the window envelope's return-address line into the
  recipient block). An address goes only whole (street, postcode, town; the
  country when the text names it). Then Haus' Haushalt tool
  `kontakt_ergaenzen` gets the firm with what was found and the document's
  Paperless link as `quelle`; Haus adds only what the card lacks. Once per
  document, also when its text is read again; a document read before #820 is
  asked once more, for its sender only (its fields stay as they are). When
  Haus answers that the IBAN differs from the one in the address book, the
  document gets `prüfen` and a note with Haus' reason (a changed bank account
  is the fake-invoice trick). A Haus without the tool (before haus#164, or
  without its address book) or away keeps the contact in the record for the
  next run, never asking the model again; a tool error is recorded, not
  retried. Without Haus nothing is asked, sent or recorded.
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
- **"zum Monats-/Quartals-/Jahresende"** (#721). A period "zum Monatsende",
  "zum Quartalsende", "zum Ende eines Kalendervierteljahres", "zum
  Halbjahresende" or "zum Jahresende" keeps its rule in the field ("3 Monate
  zum Quartalsende"). With the notice date at such an end, a period of months
  reaches back to a month's end: 1 Monat zum Monatsende before 28.02. is
  31.01., not 28.01. A notice date that is no such end (15.02. with "zum
  Quartalsende") is counted back by the day, never moved later, and the
  document gets `prüfen` and a note, once.
- **Renewing** (#721). A notice date that has passed moves on by the renewal
  the contract's own text names ("verlängert sich jeweils um 12 Monate",
  "um ein weiteres Jahr"), term by term until it lies ahead, a month's end
  kept (29.02.2024 + 1 Jahr is 28.02.2025); the note quotes the words and
  says to empty the field once the contract is cancelled. The fields
  Vertragsbeginn, Vertragsende and Zahlungsintervall are no evidence of the
  renewal (a two-year contract often renews by one year, a yearly one is
  paid monthly), so a text that names no renewal, or two ("um 12 oder um 24
  Monate"), changes nothing: the tag `prüfen` and a note, once per date.
- **The message on the reminder day** (#721). On the reminder's day, and
  until the notice must be in, the owner (box.yml `notify.person`) gets one
  message per document and notice date through the Bote, e.g. on Signal:
  „Laib: Kündigung prüfen: Stromvertrag“, an empty line, then „Stromvertrag
  (Stadtwerke …). Kündigen bis 31.12.2026 (Kündigungstermin 31.03.2027).
  https://paperless.…/documents/12/details“. The script prints it as
  `meldung {json}`; `paperless-vertraege.sh`, the unit's host part, writes
  it into laib-notify's `eingang/` (`/mnt/data/laib/notify/eingang`, #703),
  which tells it through the Bote first, else by mail, once. Paperless needs
  no token for it, and the container never reaches bote-notify, which
  listens on the host's loopback only. Without Haus the message still comes.
- **Letter salad** (#721). A document whose text is letter salad (fewer
  than half its tokens word-shaped, or short tokens with almost none of the
  common German or English words; a text under 30 words is not judged, an
  invoice of numbers is not salad) is recognised again through Paperless'
  own reprocess (the values' `OCR_MODE`), once, at most three a run, with a
  note. Still salad a day after: the tag `prüfen` and a note to scan the
  original again. A contract of salad is not read until its text reads.
  Every run looks at each document's text once (its hash is kept).
- **The household's own people** (#737). Paperless 3.0's AI names the
  people a letter mentions as its correspondent (its prompt is fixed, no
  setting keeps names out), so a letter *to* a family member got that
  member as its sender. The household is Paperless' own users: their first
  and last name, which allauth takes from Authelia's `name` claim (LLDAP's
  display name, split at the first space) on the first login, and the
  login itself as a whole name; plus `HAUSHALT_NAMEN` (comma-separated
  „Vorname Nachname“) for people without a login. box.yml `people` is not
  a source: it carries a key and chat channels, no names. A correspondent
  is a household person's when its name is the full name in either order
  („Muster, Anna“, „Anna Muster“), „Familie“ and the surname, or a first
  name or a login as its whole name; case, umlauts (ü = ue), accents,
  punctuation, salutations and titles (Herrn Dr.) do not count, and
  „Anna Muster Immobilien GmbH“ is no person. On such a document the run
  clears the correspondent and leaves a note. **Never touched:** a document
  with the tag `korrespondent-ok` (made with the first clearing, no
  automatic matching, no owner; the note says to set it when the sender
  was right), and every correspondent a document had when the pass first
  ran (the record keeps them), so a hand-filed archive stays. The
  correspondent itself is never deleted: Paperless marks none as made by
  its AI. One the run left without documents is in the record, and
  `paperless-check` shows it in `laib diag` (yellow) for the owner to
  delete in Paperless.
- **The record.** `laib-vertraege.json` in Paperless' data folder (backed
  up): per document the fields asked, what was filled and not taken (a
  document read before #818 counts as asked for the contract fields, so a
  contract that is also an invoice is asked once more), per document its
  sender's answer from Haus or the contact waiting for it (#820), per reminder its day,
  the messages told, the dates marked, each text's verdict.
  `journalctl --user -u paperless-vertraege` has one line per document and
  reminder.

Not here (follow-up issues): questions to the documents through the Bote
or Haus; upstream, a Paperless setting for names its AI must not suggest.

A document recognised before keeps its text: to recognise it again, select
it in Paperless and reprocess it from the document actions (the values'
mode applies); the timer does that by itself for letter salad.
