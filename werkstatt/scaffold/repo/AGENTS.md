# Working in this repo

Read the Werkstatt guide before you change anything here: `/opt/werkstatt/guide/AGENTS.md`,
then `/opt/werkstatt/guide/apps.md` (it is also Claude Code's managed CLAUDE.md in the Werkstatt).
Outside the Werkstatt, the same guide is `werkstatt/guide/` in github.com/mdopp/laib.

- A new app: `laib-app new <name>`; never copy an app folder by hand.
- Before a push: the app's tests and `laib template lint templates/<name>`.
- Conventional Commits, scope = the app: `feat(<name>): …`.
- Actions by commit, base images by digest (`uses: x/y@<sha> # v1.2.3`, `FROM img:tag@sha256:…`):
  `scripts/pins.sh` fails CI otherwise; Dependabot moves them weekly (laib ADR 0034).
- No secrets in the tree: a template declares them, the box generates or stores them.
