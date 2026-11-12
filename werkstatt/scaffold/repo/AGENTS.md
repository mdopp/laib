# Working in this repo

Read the Werkstatt guide before you change anything here: `/opt/werkstatt/guide/AGENTS.md`,
then `/opt/werkstatt/guide/apps.md` (it is also Claude Code's managed CLAUDE.md in the Werkstatt).
Outside the Werkstatt, the same guide is `werkstatt/guide/` in github.com/mdopp/laib.

- A new app: `laib-app new <name>`; never copy an app folder by hand.
- Before a push: the app's tests and `laib template lint templates/<name>`.
- Conventional Commits, scope = the app: `feat(<name>): …`.
- Actions by commit, base images by digest (`uses: x/y@<sha> # v1.2.3`, `FROM img:tag@sha256:…`):
  `scripts/pins.sh` fails CI otherwise; Dependabot moves them weekly (laib ADR 0034).
- The gates (laib#1170): `scripts/loc.sh` holds production lines under the one cap in
  `scripts/loc.txt` (raising it: the reason there, the owner's yes); `scripts/ratchet.py`
  lets dead code, lint findings, duplicates and complexity only go down against
  `scripts/ratchet/` (a fixed finding: `--update`; a new one that must stay: `--update --accept`,
  the reason in the PR, the owner's yes). Both run in CI; run them before a PR.
- Every behaviour has its Ablauf: `docs/ABLAEUFE.md` holds the apps' scenarios in plain German
  (`<PREFIX>-<n>`, Wenn/Dann); a feature PR adds or changes its scenario and the test naming it
  (`// Ablauf A-3`). `scripts/ablaeufe.py` fails CI on a scenario without a test, unless it is in
  `scripts/ablaeufe-baseline.txt`, which only shrinks.
- `ratchet.py`, `test_ratchet.py`, `ablaeufe.py`, `test_ablaeufe.py`, `pins.sh`, `copies.sh` and `loc.sh` are laib's
  `werkstatt/scaffold/repo/scripts/`: `scripts/copies.sh` fails CI when a copy differs.
  Change them in laib, then copy them here; never edit them only here.
- No secrets in the tree: a template declares them, the box generates or stores them.
