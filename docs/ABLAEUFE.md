# Abläufe — what the owner and the family see Laib do

Every program of the household writes down its behaviour as numbered
scenarios („Abläufe“, owner 2026-11-12), in plain German, as the family or
the owner would tell it: what they see and do, not how the code does it.
Each scenario is proven by at least one test that names its id;
`scripts/ablaeufe.py` fails CI otherwise (`docs/TESTING.md`).

The format: a heading that starts with the id and one sentence, then two to
five short lines Wenn/Dann (also „Und“). The id is `<PREFIX>-<n>`, one
prefix per repo (laib: `L`), numbers never reused. A test names it in a
comment or its name: `// Ablauf L-12` (`# Ablauf L-3, L-4` for several,
`ablauf:L-12` in a test title).

```
### L-12 Ein Satz, was passiert
Wenn der Owner …
Dann sieht er …
```

A feature PR adds or changes its scenario and the test naming it. A
scenario no test proves yet is in `scripts/ablaeufe-baseline.txt`, which
only shrinks.

## Inbox und Freigaben

### L-1 Ein neuer Vorschlag meldet sich beim Owner
Wenn ein Agent oder die Box selbst etwas ändern will,
Dann bekommt der Owner im Chat eine Zeile „Freigabe #N: …“ mit dem Link auf die Karte.
Und antwortet der Bote nicht, kommt dieselbe Zeile per Mail.

### L-2 Jede Karte sagt in Klartext, was sich ändert
Wenn der Owner die Inbox öffnet,
Dann sagt jede Karte in einem Satz, was sich ändert, was es für die Familie heißt, ob man es zurücknehmen kann, wer es will und warum.
Und der Diff steht nur unter „Details“, nie roher Fehlertext.

### L-3 Jede Karte hat dieselbe Knopfreihe
Wenn eine Karte offen ist,
Dann steht links „Freigeben“, rechts „Ablehnen“.
Und bei einer riskanten Karte steht das Risiko über der Reihe und „Freigeben“ ist nicht hervorgehoben.

### L-4 Nach „Freigeben“ bleibt die Karte an ihrem Platz
Wenn der Owner eine Karte freigibt,
Dann bleibt sie, wo sie war, und zeigt ohne Knöpfe, was die Box gerade tut („sichert immich“, „startet immich“), dann „fertig“.
Und die Seite lädt sich dabei nie als Ganzes neu.

### L-5 Ablehnen fragt nach dem Warum, „nicht jetzt“ zählt gegen niemanden
Wenn der Owner „Ablehnen“ tippt,
Dann fragt die Karte „Warum nicht? (optional)“ mit „Ablehnen bestätigen“ und „Abbrechen“.
Und der Grund steht beim Vorschlag; „nicht jetzt“ gilt als „später“, nicht als Nein.

### L-6 „Alle freigeben“ ist eine Bestätigung, jede Karte läuft für sich
Wenn zwei oder mehr Karten offen sind und der Owner „Alle freigeben“ tippt,
Dann nennt eine Seite genau diese Karten und gibt nach einer Bestätigung jede einzeln frei.
Und was löscht oder was die Prüfung markiert hat, bleibt für die Einzelfreigabe liegen, mit Grund.

### L-7 Freigegeben wird nur in der Inbox
Wenn ein Agent oder der Chat etwas freigeben will, das nur ein Mensch darf,
Dann lehnt Laib ab, mit dem Grund.
Und ein Agent darf nie seinen eigenen Vorschlag freigeben.

### L-8 Eine Freigabe antwortet sofort, auch wenn die Box arbeitet
Wenn der Owner freigibt, während die Box noch etwas anwendet,
Dann antwortet die Karte sofort mit „wartet, bis die Box mit der Arbeit davor fertig ist“.
Und drei Freigaben in dieser Zeit werden zusammen in einem einzigen weiteren Lauf angewandt.

### L-9 Ein roter Lauf nimmt alles zurück und sagt es bei jeder Karte
Wenn ein Lauf mit mehreren Freigaben nicht grün endet,
Dann nimmt Laib jede davon zurück und jede Karte sagt „zurückgenommen, mit dem ganzen Lauf #4, #5, #6“.

### L-10 Ein roter Lauf mit Daten lässt die Daten stehen und bietet zwei Wege
Wenn ein Update eines Dienstes mit Daten nicht richtig läuft,
Dann läuft das alte Programm wieder und die Karte sagt, dass die Daten auf dem Stand nach dem Update sind.
Und sie bietet „Sicherung zurückspielen“ und „Nach vorne reparieren“, je ein Tipp.

### L-11 Verkettete Freigaben laufen in ihrer Reihenfolge
Wenn eine Karte auf eine andere, einen Schlüssel oder eine Version wartet,
Dann kann der Owner sie vorab freigeben, und sie läuft erst, wenn das da ist, in ihrer Reihenfolge.
Und eine Karte, die auf einen Schlüssel wartet, verlinkt dessen Formular.

### L-12 „Zurücknehmen“ gibt es nur, wo es etwas rückgängig macht
Wenn eine Freigabe fertig ist,
Dann bietet die Karte „Zurücknehmen“ nur, wenn es etwas zurückbringt.
Und ein neu installierter Dienst wird dabei entfernt, seine Daten bleiben.

### L-13 Ein überholter Vorschlag wird neu berechnet oder sagt, warum nicht
Wenn sich seit dem Vorschlag etwas geändert hat,
Dann wendet Laib ihn nur an, wenn er genau das tut, was der Owner gesehen hat.
Sonst heißt die Karte „veraltet“ mit dem Grund, und ein späterer Vorschlag zum Gleichen ersetzt den älteren.

### L-14 Der Owner erfährt im Chat, wie es ausging
Wenn ein freigegebener Vorschlag fertig gelaufen oder zurückgenommen ist,
Dann wird die Chat-Zeile, die ihn angekündigt hat, entsprechend markiert.

### L-15 Ein neues Programm für einen Dienst mit Daten wartet auf einen Menschen
Wenn ein neueres Image für einen Dienst mit Daten vorgeschlagen wird,
Dann muss ein Mensch es freigeben.
Und vor dem Update sichert Laib die Daten des Dienstes.

## Schlüssel

### L-16 Schlüssel hinterlegen, ohne Konsole
Wenn der Owner auf der Box-Seite „Schlüssel hinterlegen“ öffnet,
Dann sieht er je Dienst die Schlüssel mit ihrem deutschen Namen, „fehlt“ oder „hinterlegt“, nie den Wert.
Und nach dem Speichern sagt die Seite „<Schlüssel> hinterlegt, <Dienst> neu gestartet“.

### L-17 Ein Link führt direkt zu einem Schlüssel und wieder zurück
Wenn eine App oder ein Befund auf `/schluessel?name=…&zurueck=…` verlinkt,
Dann öffnet sich genau dieses Formular mit Erklärung, woher man den Schlüssel bekommt.
Und „Zurück“ gibt es nur auf eine Adresse der eigenen Domain.

### L-18 Homes zeigt, auf welche Schlüssel die Box wartet
Wenn etwas auf der Box einen fehlenden Schlüssel braucht,
Dann steht er in Homes' Liste mit seinem Namen und Link, nie mit Wert.
Und die Aufgabe dazu schließt sich, sobald der Schlüssel gesetzt ist.

## Aufgaben

### L-19 Jede sieht nur ihre Aufgaben
Wenn Anna in Homes ihre Einrichtung öffnet,
Dann sieht sie die Aufgaben für alle und für sich, nie die einer anderen Person.
Und die Admins sehen die Admin-Aufgaben auf der Box-Seite.

### L-20 Eine Aufgabe schließt sich selbst
Wenn eine Aufgabe sagt, woran man merkt, dass sie erledigt ist, und Laib das messen kann,
Dann hakt Laib sie ab, sobald der Befund weg ist.
Und kann Laib es nicht messen, sagt die Aufgabe, dass ein Mensch sie abhakt.

### L-21 Ein Wunsch an den Boten wird eine Aufgabe für die Admins
Wenn ein Familienmitglied dem Boten einen Wunsch an die Box sagt,
Dann steht er als Box-Aufgabe bei den Admins, mit der Person, die gefragt hat.

### L-22 „Gemeinsam mit Homes“ führt durch eine Aufgabe
Wenn jemand bei einer Aufgabe „Gemeinsam mit Homes“ tippt,
Dann beginnt der Bote mit „Aufgabe #N mit dir erledigen“ und führt Schritt für Schritt.

## Updates

### L-23 Die Liste der Updates kommt nur, wenn sie sich geändert hat
Wenn die wöchentliche Prüfung neue Images findet,
Dann bekommt der Owner die Liste, aber nur, wenn sie anders ist als letzte Woche.

### L-24 „Jetzt aktualisieren“ startet den nächtlichen Lauf
Wenn der Owner die Updates jetzt will,
Dann startet der nächtliche Lauf sofort und sagt, wo sein Ergebnis erscheint.
Und läuft gerade eine Sicherung oder ein Lauf, lehnt Laib mit dem Grund ab.

### L-25 Eine Update-Karte sagt, was das Update bringt
Wenn ein Template ein Update hat,
Dann sagt die Karte in wenigen Zeilen, was es dem Haushalt bringt, das Technische unter „Details“.
Und ein Update, das nur Neuigkeiten oder Namen ändert, übernimmt Laib ohne Karte.

### L-26 Die Box startet für ein System-Update nur im Fenster nach der Sicherung neu
Wenn ein Update des Betriebssystems bereitliegt,
Dann startet die Box nur zwei bis vier Stunden nach der Sicherung neu, nie während einer, und nur wenn alles grün ist.
Und `laib diag` sagt, dass ein Update wartet.

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

### L-30 Zurückspielen startet den Dienst wieder
Wenn der Owner eine Sicherung eines Dienstes zurückspielt,
Dann hält Laib den Dienst dafür an und startet ihn danach wieder.

### L-31 Das Notfallblatt hat alles, was man ohne Box braucht
Wenn der Owner das Notfallblatt druckt,
Dann stehen darauf der Schlüssel und das Sicherungs-Passwort, für Menschen lesbar.

## Einblick (ADR 0032)

### L-32 In einen versiegelten Dienst schaut niemand hinein
Wenn ein Agent oder ein Mensch in das Archiv hineinschauen will,
Dann lehnt Laib ab, für jede Hand.

### L-33 Ein Einblick braucht die Freigabe eines Menschen und gilt eine Stunde
Wenn ein Agent in einen Dienst mit privaten Daten schauen will,
Dann lehnt Laib ab und legt eine Einblick-Karte in die Inbox.
Und nach der Freigabe darf er eine Stunde lang schauen; das Journal hält nur die Größe der Ausgabe fest.

## Status und Befunde

### L-34 Die Status-Seite zeigt Probleme zuerst
Wenn der Owner die Status-Seite öffnet,
Dann stehen die roten Dienste oben, mit Filter Alle · Rot · Gelb · Grün und ihren Zahlen.

### L-35 Jede Oberfläche sagt dasselbe über die Box
Wenn ein Dienst rot ist oder die Sicherung fehlt,
Dann sagen die Status-Seite, Homes und `laib status` dasselbe.

### L-36 Die Status-Seite auf der Adresse im Heimnetz gibt es nur zu Hause
Wenn jemand die Box über ihre LAN-Adresse aufruft,
Dann sieht er zu Hause die Status-Seite.
Und von außen sieht er nur „nur zu Hause“.

### L-37 Ein Dienst, der zehn Minuten rot ist, meldet sich
Wenn ein Dienst zehn Minuten rot ist,
Dann bekommt der Owner eine Nachricht: seit wann, wer dran ist, mit Link auf den Status.
Und wird er wieder grün, kommt „wieder grün“.

### L-38 Ein geplanter Neustart ist kein Alarm
Wenn Laib einen Dienst gerade anwendet oder angekündigt neu startet,
Dann ist er währenddessen nicht rot und niemand wird geweckt.

### L-39 Ein Befund bringt seine Reparatur mit, die erst nach Freigabe läuft
Wenn `laib diag` etwas findet, das sich reparieren lässt,
Dann liegt eine Reparatur-Karte in der Inbox, die in Worten sagt, was sie tut.
Und sie läuft erst, wenn ein Mensch freigibt; dieselbe Reparatur liegt nur einmal da.

### L-40 Was Menschen lesen, ist ein Satz, kein Fehlertext
Wenn eine Mail, eine Karte oder die Status-Seite einen Fehler zeigt,
Dann steht dort ein deutscher Satz, der rohe Text nur unter „Details“.

## Familie und Admin

### L-41 Die Box-Seite ist nur für Admins
Wenn jemand die Box-Seite nicht über Caddy und Authelia aufruft,
Dann antwortet sie „nur über die Box-Seite“ und zeigt nichts.
Und sie behält nur, was Freigeben, Schlüssel und Reparatur brauchen.

### L-42 Die Apps der Familie gibt es nur zu Hause
Wenn jemand von außen eine App der Familie aufruft,
Dann kommt er nicht hin; nur www, die Anmeldung, Kontakt und Fotos stehen im Internet.
Und wo ein Dienst von außen erreichbar ist, fragt er dort den zweiten Faktor.

### L-43 Homes lädt eine Bewohnerin ein
Wenn ein Admin in Homes jemanden einlädt,
Dann bekommt Homes den Einladungslink zurück, auch ohne Mail.
Und einen Admin einladen darf nur ein Mensch.

### L-44 Jeder Tipp antwortet sofort
Wenn jemand auf der Box-Seite einen Knopf tippt,
Dann endet es sichtbar, wo es begann: fertig, rot mit Grund und nächstem Schritt, oder wartend mit Grenze.

## Haus

### L-45 Musik per Sprache läuft auf dem Standard-Lautsprecher des Raums
Wenn in einem Raum ein Standard-Lautsprecher gewählt ist und jemand dort „Spiele Einslive“ oder „Spiele Musik von Queen“ sagt,
Dann läuft die Musik auf diesem Lautsprecher, und die Antwort kommt weiter vom Sprachgerät.
Und ohne gewählten Standard spielt sie wie bisher auf dem Lautsprecher im Raum.

### L-47 Wer den Lautsprecher aus Versehen weckt, hört nur einen kurzen Ton statt einer langen Antwort
Wenn der Lautsprecher auf ein Gespräch am Tisch oder den Fernseher hin aufwacht und nichts hört, das an ihn gerichtet ist,
Dann sagt er nichts, sondern spielt nur einen kurzen, leisen Ton „düü-doow“.
Und eine echte Frage, Bitte oder ein Befehl bekommt die Antwort wie bisher, auch wenn die Prüfung gerade nicht antwortet.
Und auf der Box-Seite zählt dieses Aufwachen als „ohne Befehl“; was gesagt wurde, wird nirgends gespeichert.

## Drucken

### L-46 Jedes Gerät im Heimnetz findet den Drucker ohne App; von außen ist er nicht erreichbar
Wenn im Heimnetz oder am USB der Box ein Drucker ist,
Dann findet jedes Handy und jeder Rechner zu Hause ihn im eigenen Druckmenü und druckt ohne Hersteller-App, auch Android.
Und ein Drucker, der das schon selbst kann, erscheint nicht doppelt; einer, der es nicht kann, heißt „… (über die Box)“.
Und von außerhalb des Heimnetzes ist der Druckserver nicht erreichbar, und seine Verwaltung ist nur auf der Box selbst.
