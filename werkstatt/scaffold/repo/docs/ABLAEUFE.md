# Abläufe — what the family sees these apps do

Every program of the household writes down its behaviour as numbered
scenarios („Abläufe“, owner 2026-11-12), in plain German, as the family or
the owner would tell it: what they see and do, not how the code does it.
Each scenario is proven by at least one test that names its id;
`scripts/ablaeufe.py` fails CI otherwise.

The format: a heading that starts with the id and one sentence, then two to
five short lines Wenn/Dann (also „Und“). The id is `<PREFIX>-<n>`, one
prefix per repo, numbers never reused. A test names it in a comment or its
name: `// Ablauf A-1` (`# Ablauf A-1, A-2` for several, `ablauf:A-1` in a
test title).

```
### A-1 Ein Satz, was passiert
Wenn jemand …
Dann sieht er …
```

A feature PR adds or changes its scenario and the test naming it. A
scenario no test proves yet is in `scripts/ablaeufe-baseline.txt`, which
only shrinks (laib's `docs/ABLAEUFE.md` is the example).

## Die Apps
