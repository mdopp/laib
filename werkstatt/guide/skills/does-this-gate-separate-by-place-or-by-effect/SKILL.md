---
name: does-this-gate-separate-by-place-or-by-effect
description: "Does this gate separate by place or by effect?. You are defining, auditing or debugging a gate — a review requirement, a human approval, a permission scope, a release barrier. Also when a gate has never once fired and you assumed that meant nothing dangerous happened."
---

# A gate keyed on place decides differently for identical work

**The question to ask of every gate:**

> **Does this gate separate by *place* or by *effect*?** By place — a file, a
> directory, a repository, a naming convention — it reaches different verdicts for
> identical work and manufactures ceremony at arbitrary spots. **If you have found
> a gate keyed on place, you have probably found one that separates wrongly.**

## Three houses, the same confusion — each already had the right axis elsewhere

This is the part worth internalising: **the mistake was never ignorance, it was
failure to transfer.** Each house had the correct axis written down somewhere in
its own rules, and had not applied it to the gate in question.

- One house gated drafts by effect and releases by a property of the repository.
- **This repo**: the permission ladder separates by **reversibility** — `reboot`
  is a transient, recoverable restart, `destroy` covers irreversible state edits
  (`docs/SCOPE_AUDIT.md`, `packages/backend/src/lib/auth/apiScope.ts`). The
  release gate, meanwhile, separates by **directory path**
  (`PATH_MANDATED_PATHS` in `scripts/autoloop-seal.ts`). Proof the approximation
  does not hold: a template schema bump shipping an upgrade script — a **data
  migration on installed services** — passed the gate only because the file it
  touched happened to sit in a listed folder.
- A third house gated review on "touches the signing keystore, `local.properties`,
  CI secrets or the token contract". **Three of the four are places, and all three
  are structurally unfireable**: `.gitignore` carries every one of them, and CI
  secrets appear in no diff. Across a dozen units and seven releases the gate
  fired **zero times**. What slipped through was the unit that created the only
  path capable of unlatching a front door.

A gate that has never fired is not evidence of calm. Check whether it *can* fire.

## Two gates, two different effect axes — do not merge them

| Gate | Asks about | Trigger |
|---|---|---|
| **Review** | **Disclosure** | Does the change widen access, or make a secret reachable that was not? |
| **Human approval** | **Reversibility** | Anything that cannot be rolled back — a data migration, a consent-relevant change — goes to a person |

The operator's own framing, which rejected a repository-wide justification:

> *„wenn etwas nicht zurück rollbar ist, weil daten migriert werden"* — the gate
> is the irreversibility of **the individual change**, not the category of the house.

## Applying it here — what the transfer looked like in practice (#2700)

The release gate now decides on **both** axes. `scripts/autoloop-seal.ts` keeps
`PATH_MANDATED_PATHS` (place) for the cases it genuinely covers, and adds
`durableStateEffects` (effect) beside it; `gateDecision` owes a box-verify if
*either* fires. The effect trigger is a **closed, named list**, because a gate
that needs judgement cannot be a script:

| Effect | Signature in the diff | Why it is irreversible |
|---|---|---|
| `template-schema-migration` | a file at `templates/*/migrations/v<n>-to-v<m>.*`, or an added line touching `servicebay.schema-version` | runs against installed services' own data |
| `secret-store-write` | added lines naming the saved-secrets key material or the on-disk `enc:` envelope | rotate or re-shape it and stored secrets stop decrypting |
| `installed-manifest-write` | an added *assignment* into `config.installedTemplates` (a read does not count) | the record of what is installed at which schema version |

Two things worth copying to another house:

- **Reproduce the blindness first.** The proof is a test asserting the *place*
  gate returns nothing for the real change set, next to one asserting the effect
  gate trips on it. A test that is green before the fix proves nothing — take the
  new rule back out and watch it go red.
- **Degrade toward the old gate, never away from it.** The content-keyed rules
  need the diff body; when that read fails, fall back to the path rules rather
  than to "nothing owed".

Autonomy levels themselves are **not** portable between houses — see
`footgun-importing-a-working-agreement-from-another-repo`.

---
Carried from ServiceBay's assist catalog (`assists/checklist-does-this-gate-separate-by-place-or-by-effect.md` in mdopp/servicebay) when ServiceBay went off (laib #394). It is Laib's now: change it here.
