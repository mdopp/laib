# Abläufe — what the family sees these apps do

Every program of the household writes down its behaviour as numbered
scenarios („Abläufe“, owner 2026-11-12), in plain German, as the family or
the owner would tell it. Each scenario is proven by at least one test that
names its id; `scripts/ablaeufe.py` fails CI otherwise (laib's
`docs/ABLAEUFE.md` is the example).

## So wenig wie nötig (owner, 2026-11-12)

1. A scenario only if it is a promise to a person or to another program:
   what someone sees, gets, or is protected from. No internals, no exact
   button texts.
2. Kinds: privacy and data loss always; convenience only if it was broken
   before.
3. At most three lines (Wenn/Dann/Und), one promise per scenario.

## Format

A heading that starts with the id and one sentence, then the lines, each
starting with Wenn, Dann or Und. The id is `<PREFIX>-<n>`, one prefix per
repo (here: `A`); a dropped id simply disappears and is never reused.

```
### A-12 Ein Satz, was versprochen ist
Wenn der Owner …
Dann sieht er …
```

## Abgleich: scenario and test

- **Both directions.** A scenario no test names is red, unless it is in
  `scripts/ablaeufe-baseline.txt`, which only shrinks; a test naming an id
  this file does not have is red.
- **Fingerprint.** A test names a scenario with the fingerprint of the text
  it was written against: `// Ablauf A-12 @3f9a` (`# Ablauf A-3 @ab12, A-4
  @cd34` for several, each with its own; `ablauf:A-12 @3f9a` in a test
  title). The fingerprint is the first four hex digits of the sha256 of the
  scenario's title and lines, whitespace collapsed. Change a scenario's
  words and every test naming it goes red, with file:line and the new
  fingerprint. `scripts/ablaeufe.py --stempeln A-12` prints the fingerprint
  and the markers naming it; it changes nothing, on purpose: whoever
  updates a marker has re-read that test against the new text first.
- **Tests that can fail.** That a test naming a scenario actually bites is
  the mutation ratchet's job (`scripts/ratchet.py mutation`, for the
  packages in `scripts/ratchet/mutation.pkgs`), not this file's.
- **A feature PR** adds or changes its scenario and the test naming it.

## Die Apps
