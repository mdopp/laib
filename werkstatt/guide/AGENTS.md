# The Werkstatt — how to work here

You are in the Werkstatt, the dev container on a Laib box (one home box run
by `laib`). The household's owner speaks German; write code, commits and
docs in English, and anything a family member reads in German.

## Rules that outrank everything else

1. **Laib changes the box, you propose.** You never apply your own change.
   A new service, a public route, `auth: none`, a box.yml or policy change
   is the owner's decision in the Inbox (`laib propose …`, then wait).
2. **Commands do the mechanics.** Scaffold with `laib-app new`, lint with
   `laib template lint`, check a route with `laib-app check`. Do not redo
   their steps by hand, and do not skip one because it looks obvious.
3. **Done is measured.** An app is done when `laib-app check
   <name>.<domain>` exits 0. Exit 8 says what is missing (often the owner's
   Strato record); exit 9 means it could not be measured: say so in those
   words, never "done".
4. **Ask once, with options.** When the owner must act (Strato, approve,
   make a package public), say exactly what to do in one German sentence
   per step, then wait. Do not guess around a missing decision.
5. **No secrets in a repo.** A template declares a secret; the box
   generates or stores it. Never print or commit a token.
6. **Three tries, then stop.** Three failed attempts at the same goal:
   stop, write down what you saw, `laib escalate` or ask.

## Tools here

git, gh, go, node/npm, python3 (+ venv, pip), gcc/make, jq, curl,
ripgrep, dig, openssl, vi, nano, tmux, podlet; `laib` (Laib's MCP as a
command; `laib` alone lists what your token may call), `laib-app`, Claude
Code and pi (pi talks to the box's own model; while somebody else holds
the GPU, the model proxy answers who and until when).
No podman: images are built by the repo's CI and published to ghcr.

## What to read for a task

- **A small program for the household, under its own `<name>.<domain>`:**
  `/opt/werkstatt/guide/apps.md`, the whole path from the wish to the
  checked route.
- **A Laib template** (an upstream app, or changing one):
  `/opt/werkstatt/guide/templates.md`.
- **Laib itself** (kernel, built-in templates): the laib repo's
  `CLAUDE.md`, then `docs/DESIGN.md`, `docs/STATE.md`, `docs/adr/`.
- **What the box runs now:** `laib status`, `laib context`, `laib template list`.
- **How to check, measure and ask:** the skills in
  `/opt/werkstatt/guide/skills/` (pi and Claude Code load them by their
  description): a probe that cannot fail, a deployment you have looked at,
  a measurement with its limits, a gate by place or effect, how to put a
  decision to the owner.
- **What ServiceBay did** (the old system; the comparison every
  replacement owes, laib `CLAUDE.md`): its templates and its assist
  catalog in the checkout `/workspace/servicebay` (`templates/`,
  `/workspace/servicebay/assists`). Read them there, as a repo: nothing
  delivers them any more (no checkout: `gh repo clone mdopp/servicebay
  /workspace/servicebay`).

This guide is `werkstatt/guide/` in github.com/mdopp/laib; it reaches the
Werkstatt with the image. A gap or a wrong line here is an issue there.
