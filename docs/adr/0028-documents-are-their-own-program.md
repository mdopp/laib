# ADR 0028 — Documents are their own program: Dokumente, beside Haus and the Bote

Applies when you change how paper and files become documents in Paperless
(scanning a stack, splitting it, reading contracts and invoices, reminding
of notice dates), or when you wonder whether such logic belongs in a laib
template.

- **Status:** Proposed, 2026-10-27
- **Deciders:** the owner (mdopp), on „Warum ist das eigentlich in Laib?
  Gehört das nicht eher in ein Document Service? Außerhalb von Laib?“ and
  the answer below: „Ja und dann auch gleich bauen“.

## Context

ADR 0026 took the household verbs out of the kernel, but the document
logic itself still lives in the laib repo as templates: `scan/scan.py`
(1,540 lines: a stack from the scanner or the inbox folder, turned, read,
split by the household model, filed) and
`templates/paperless/paperless-vertraege.py` (917 lines: contract and
invoice fields read by the model, notice reminders). Both grow fast
(#568, #571, #589, #709, #721, #801, #814, #818, #829, #831), each change
waits for a laib train, and a laib update restarts what they depend on in
the middle of their work. On 2026-10-27 an update restarted the model while
a 36-page stack was being split, and the stack went to Paperless unsorted.

Haus is the precedent: a program in its own repo with its own tests and
image, which laib only runs (a template) and reaches over MCP and HTTP.

## Decision

1. **Dokumente** is a program in its own repo (`mdopp/dokumente`), with its
   own CI, tests and image (`ghcr.io/mdopp/dokumente`). It owns everything
   between paper and a finished document in Paperless:
   - the scanner and the inbox folder, splitting a stack, separator sheets,
     blank pages (from `scan/`);
   - reading contracts and invoices into Paperless' fields, notice and due
     reminders (from `paperless-vertraege.py`), through Paperless' REST API
     with its own token, no longer inside the Paperless container. The
     token is a template secret the box generates or stores, for the
     narrowest Paperless user that can read documents and set their tags,
     notes and custom fields, never the admin;
   - running when a document arrives (a Paperless workflow webhook on
     „Dokument hinzugefügt“, debounced per stack), with an hourly run as a
     catch-all. The webhook reaches Dokumente only on the pod- or
     host-internal address, never through caddy;
   - the household model through the lease's proxy as background work
     (`X-Laib-Background`), as both parts do today. What they do now carries
     over unchanged, with its tests: the face-up turn, separator sheets,
     letterhead and reference recognition, filling only empty fields, the
     household's exclusions;
   - doing again on its own what failed for a passing reason, a model or
     Paperless away (#831), and saying so instead of asking a person;
   - one status file, read by `laib status`, Haus and the Bote, that says
     what it does and did: the stack, the evaluation's last run.
2. **Laib keeps what touches the box:** the template that runs Dokumente
   (the USB scanner passed in, the model through the lease, secrets, the
   folders, laib-notify's eingang for messages), the verbs behind the verb
   door (`laib scan …` keeps its name and shape, ADR 0026), and the dial and
   rollbacks of its image. A laib update waits while Dokumente reports work
   in progress, or Dokumente survives a restart of the model by retrying.
3. **Haus and the Bote are its two faces**, as for the rest of the house:
   Haus shows and starts (Scannen, Letzte Stapel, Verträge), the Bote talks
   about it. Neither holds document logic.
4. **Paperless stays the store** (ADR 0017). `paperless-mail.py` stays with
   Paperless' template: it configures Paperless' own mail fetching.

Dokumente is a container in the sense of ADR 0027: it touches nothing on
the host, holds its own token and folders, and never the age key or the
state repo.

## Consequences

- The move copies `scan/` and the contract files with their history into
  the new repo once the changes in flight have landed (#830, #831, #833);
  #833's stopgap trigger (watching the thumbnails folder on a timer) is not
  carried, the webhook replaces it; from then on
  `scan/` in laib is frozen and later removed.
- The scan template points at the new image; `paperless-vertraege.*` units
  leave the Paperless template, replaced by the Dokumente service.
- A feature about documents costs a PR in `mdopp/dokumente` and, only if it
  needs the box (a device, a folder, a secret), one in laib.
