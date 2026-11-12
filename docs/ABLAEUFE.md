# Abläufe — what the owner and the family see Laib do

Every program of the household writes down its behaviour as numbered
scenarios („Abläufe“, owner 2026-11-12), in plain German, as the family or
the owner would tell it. Each scenario is proven by at least one test that
names its id; `scripts/ablaeufe.py` fails CI otherwise (`docs/TESTING.md`).

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
repo (laib: `L`); a dropped id simply disappears and is never reused.

```
### L-12 Ein Satz, was versprochen ist
Wenn der Owner …
Dann sieht er …
```

## Abgleich: scenario and test

- **Both directions.** A scenario no test names is red, unless it is in
  `scripts/ablaeufe-baseline.txt`, which only shrinks; a test naming an id
  this file does not have is red.
- **Fingerprint.** A test names a scenario with the fingerprint of the text
  it was written against: `// Ablauf L-12 @3f9a` (`# Ablauf L-3 @ab12, L-4
  @cd34` for several, each with its own; `ablauf:L-12 @3f9a` in a test
  title). The fingerprint is the first four hex digits of the sha256 of the
  scenario's title and lines, whitespace collapsed. Change a scenario's
  words and every test naming it goes red, with file:line and the new
  fingerprint. `scripts/ablaeufe.py --stempeln L-12` prints the fingerprint
  and the markers naming it; it changes nothing, on purpose: whoever
  updates a marker has re-read that test against the new text first.
- **Tests that can fail.** That a test naming a scenario actually bites is
  the mutation ratchet's job (`scripts/ratchet.py mutation`,
  `docs/TESTING.md`), not this file's.
- **A feature PR** adds or changes its scenario and the test naming it.

## Inbox und Freigaben

### L-1 Der Owner erfährt von jedem Vorschlag und wie er ausging
Wenn ein Agent oder die Box selbst etwas ändern will,
Dann bekommt der Owner im Chat eine Zeile mit dem Link auf die Karte, und antwortet der Bote nicht, per Mail.
Und ist der Vorschlag durchgelaufen oder zurückgenommen, steht das an derselben Zeile.

### L-2 Jede Karte sagt in Klartext, was sich ändert
Wenn der Owner eine Karte öffnet,
Dann sagt sie in wenigen Sätzen, was sich ändert, was es der Familie bringt, ob man es zurücknehmen kann und wer es will.
Und eine riskante Karte nennt ihr Risiko, bevor er freigibt.

### L-5 Ein Nein erreicht den Vorschlagenden mit seinem Grund
Wenn der Owner einen Vorschlag ablehnt,
Dann kann er sagen, warum, und der Grund steht beim Vorschlag.
Und „nicht jetzt“ gilt als „später“, nicht als Nein.

### L-6 Alle auf einmal freigeben lässt Löschendes liegen
Wenn der Owner mehrere Karten auf einmal freigibt,
Dann sieht er vorher genau diese Karten und bestätigt sie einmal.
Und was löscht oder was die Prüfung markiert hat, bleibt mit Grund für die Einzelfreigabe liegen.

### L-7 Freigeben darf nur ein Mensch
Wenn ein Agent oder der Chat etwas freigeben will, das nur ein Mensch darf,
Dann lehnt Laib ab, mit dem Grund.
Und ein Agent gibt nie seinen eigenen Vorschlag frei.

### L-9 Ein roter Lauf nimmt alles zurück und sagt es bei jeder Karte
Wenn ein Lauf mit mehreren Freigaben nicht grün endet,
Dann nimmt Laib jede davon zurück.
Und jede Karte sagt, dass sie mit dem ganzen Lauf zurückgenommen ist.

### L-10 Ein missglücktes Update verliert keine Daten
Wenn das Update eines Dienstes mit Daten nicht richtig läuft,
Dann läuft das alte Programm wieder und die Karte sagt, dass die Daten auf dem Stand nach dem Update sind.
Und sie bietet an, die Sicherung zurückzuspielen oder nach vorne zu reparieren.

### L-11 Eine vorab freigegebene Karte läuft erst, wenn ihr Vorgänger da ist
Wenn eine Karte auf eine andere, einen Schlüssel oder eine Version wartet,
Dann kann der Owner sie vorab freigeben, und sie läuft erst danach, in ihrer Reihenfolge.

### L-12 Zurücknehmen löscht nie Daten
Wenn der Owner eine Freigabe zurücknimmt,
Dann wird das nur angeboten, wo es etwas zurückbringt.
Und ein neu installierter Dienst wird entfernt, seine Daten bleiben.

### L-13 Angewandt wird nur, was der Owner gesehen hat
Wenn sich seit dem Vorschlag etwas geändert hat,
Dann wendet Laib ihn nur an, wenn er genau das tut, was auf der Karte stand.
Und sonst heißt die Karte veraltet, mit dem Grund.

### L-15 Vor dem Update eines Dienstes mit Daten fragt Laib und sichert
Wenn ein neueres Programm für einen Dienst mit Daten vorgeschlagen wird,
Dann muss ein Mensch es freigeben.
Und vor dem Update sichert Laib die Daten des Dienstes.

## Schlüssel

### L-16 Einen Schlüssel hinterlegen, ohne dass sein Wert je angezeigt wird
Wenn der Owner auf der Box-Seite einen Schlüssel hinterlegt,
Dann sieht er je Dienst, welcher fehlt und welcher hinterlegt ist, nie den Wert.
Und nach dem Speichern sagt die Seite, welcher Dienst neu gestartet ist.

### L-17 Ein Link zu einem Schlüssel führt nur auf die eigene Domain zurück
Wenn eine App oder ein Befund auf das Formular eines Schlüssels verlinkt,
Dann öffnet sich genau dieses Formular mit Erklärung, woher man den Schlüssel bekommt.
Und zurück geht es nur auf eine Adresse der eigenen Domain.

### L-18 Homes erfährt, auf welche Schlüssel die Box wartet, nie ihren Wert
Wenn etwas auf der Box einen fehlenden Schlüssel braucht,
Dann steht er in Homes' Liste mit seinem Namen und Link, nie mit Wert.

## Aufgaben

### L-19 Jede sieht nur ihre Aufgaben
Wenn Anna in Homes ihre Einrichtung öffnet,
Dann sieht sie die Aufgaben für alle und für sich, nie die einer anderen Person.
Und die Admin-Aufgaben stehen nur bei den Admins.

### L-20 Eine erledigte Aufgabe schließt sich selbst
Wenn Laib messen kann, dass eine Aufgabe erledigt ist,
Dann hakt Laib sie ab, sobald es so ist.
Und kann Laib es nicht messen, sagt die Aufgabe, dass ein Mensch sie abhakt.

### L-21 Ein Wunsch an den Boten geht nicht verloren
Wenn ein Familienmitglied dem Boten einen Wunsch an die Box sagt,
Dann steht er als Aufgabe bei den Admins, mit der Person, die gefragt hat.

## Updates

### L-23 Updates melden sich nur, wenn es etwas zu entscheiden gibt
Wenn die wöchentliche Prüfung neue Programme findet,
Dann bekommt der Owner die Liste nur, wenn sie anders ist als letzte Woche.
Und ein Update, das nur Neuigkeiten oder Namen ändert, übernimmt Laib ohne Karte.

### L-24 Sofort aktualisieren geht nie mitten in einer Sicherung
Wenn der Owner die Updates jetzt will,
Dann startet der nächtliche Lauf sofort und sagt, wo sein Ergebnis erscheint.
Und läuft gerade eine Sicherung oder ein Lauf, lehnt Laib mit dem Grund ab.

### L-26 Für ein System-Update startet die Box nur nach der Sicherung neu
Wenn ein Update des Betriebssystems bereitliegt,
Dann startet die Box nur zwei bis vier Stunden nach der Sicherung neu, nie während einer.
Und nur, wenn alles grün ist.

## Sicherung

### L-27 Eine fehlgeschlagene oder alte Sicherung meldet sich
Wenn die nächtliche Sicherung fehlschlägt oder zu alt ist,
Dann ist sie rot und der Owner bekommt Bescheid.

### L-28 Jede Sicherung hat eine Kopie außer Haus
Wenn die nächtliche Sicherung durch ist,
Dann kopiert Laib sie auf die NAS.
Und die Box-Seite zählt zwei Orte nur, solange diese Kopie frisch ist.

### L-29 Der monatliche Test spielt eine Sicherung wirklich zurück
Wenn der Wiederherstellungstest läuft,
Dann vergleicht er das Zurückgespielte mit der Sicherung, nicht mit der laufenden Datei.
Und jede Art, wie eine Sicherung kaputt sein kann, macht ihn rot.

### L-30 Nach dem Zurückspielen läuft der Dienst wieder
Wenn der Owner eine Sicherung eines Dienstes zurückspielt,
Dann hält Laib den Dienst dafür an und startet ihn danach wieder.

### L-31 Das Notfallblatt hat alles, was man ohne Box braucht
Wenn der Owner das Notfallblatt druckt,
Dann stehen darauf der Schlüssel und das Sicherungs-Passwort, für Menschen lesbar.

## Einblick (ADR 0032)

### L-32 In einen versiegelten Dienst schaut niemand hinein
Wenn ein Agent oder ein Mensch in das Archiv hineinschauen will,
Dann lehnt Laib ab, für jede Hand.

### L-33 Ein Einblick in private Daten braucht einen Menschen und gilt eine Stunde
Wenn ein Agent in einen Dienst mit privaten Daten schauen will,
Dann lehnt Laib ab und legt eine Einblick-Karte in die Inbox.
Und nach der Freigabe darf er eine Stunde lang schauen; das Journal hält nur die Größe der Ausgabe fest.

## Status und Befunde

### L-35 Jede Oberfläche sagt dasselbe über die Box
Wenn ein Dienst rot ist oder die Sicherung fehlt,
Dann sagen die Status-Seite, Homes und `laib status` dasselbe.

### L-36 Die Status-Seite im Heimnetz gibt es nur zu Hause
Wenn jemand die Box über ihre Adresse im Heimnetz aufruft,
Dann sieht er zu Hause die Status-Seite.
Und von außen sieht er nichts davon.

### L-37 Ein echter Ausfall meldet sich, ein geplanter Neustart nicht
Wenn ein Dienst zehn Minuten rot ist,
Dann bekommt der Owner eine Nachricht mit seit wann und Link, und wird er wieder grün, auch das.
Und startet Laib einen Dienst geplant neu, ist er währenddessen nicht rot und niemand wird geweckt.

### L-39 Eine Reparatur läuft erst nach der Freigabe eines Menschen
Wenn `laib diag` etwas findet, das sich reparieren lässt,
Dann liegt eine Reparatur-Karte in der Inbox, die in Worten sagt, was sie tut.
Und sie läuft erst, wenn ein Mensch freigibt; dieselbe Reparatur liegt nur einmal da.

### L-40 Was Menschen lesen, ist ein Satz, kein Fehlertext
Wenn eine Mail, eine Karte oder die Status-Seite einen Fehler zeigt,
Dann steht dort ein deutscher Satz, der rohe Text nur in den Details.

## Familie und Admin

### L-41 Die Box-Seite ist nur für Admins
Wenn jemand die Box-Seite nicht über die Anmeldung der Box aufruft,
Dann zeigt sie nichts.

### L-42 Die Apps der Familie gibt es nur zu Hause
Wenn jemand von außen eine App der Familie aufruft,
Dann kommt er nicht hin; nur www, die Anmeldung, Kontakt und Fotos stehen im Internet.
Und wo ein Dienst von außen erreichbar ist, fragt er dort den zweiten Faktor.

### L-43 Einen Admin lädt nur ein Mensch ein
Wenn ein Admin in Homes jemanden einlädt,
Dann bekommt Homes den Einladungslink zurück, auch ohne Mail.
Und einen Admin einladen darf nur ein Mensch, kein Programm.

### L-44 Jeder Tipp auf der Box-Seite endet sichtbar
Wenn jemand auf der Box-Seite etwas tippt, auch während die Box gerade arbeitet,
Dann antwortet sie sofort, dort wo er getippt hat: fertig, rot mit Grund und nächstem Schritt, oder wartend mit Grenze.
Und die Seite lädt sich dabei nie als Ganzes neu.
