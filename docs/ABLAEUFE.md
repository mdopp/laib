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

### L-9 Ein roter Lauf nimmt nur die rote Freigabe zurück
Wenn ein Lauf mit mehreren Freigaben nicht grün endet,
Dann wendet Laib jede Freigabe noch einmal allein an, in ihrer Reihenfolge, und behält die grünen.
Und nur die rote Karte sagt „zurückgenommen“, mit den Freigaben aus demselben Lauf, die angewendet sind; nur für sie kommen die Folgekarten.

### L-10 Ein roter Lauf mit Daten lässt die Daten stehen und bietet zwei Wege
Wenn ein Update eines Dienstes mit Daten nicht richtig läuft,
Dann läuft das alte Programm wieder und die Karte sagt, dass die Daten auf dem Stand nach dem Update sind.
Und sie bietet „Sicherung zurückspielen“ und „Nach vorne reparieren“, je ein Tipp.

### L-50 Ein roter Durchgang sagt auf seinen Karten, warum
Wenn ein Durchgang mit mehreren Freigaben rot endet und Laib ihn zurückgenommen hat,
Dann liest der Operator Journal und Log und schreibt die Ursache in einem Satz auf die Karten des Durchgangs und ihre Folgekarten: „Der Operator hat nachgesehen: …“.
Und was mit dem Fehler nichts zu tun hatte, schlägt er einzeln neu vor, so wie es war; freigeben muss es wieder ein Mensch.

### L-67 Wo sich mehrere Wege ausschließen, liegt eine Karte da, auf der man einen wählt
Wenn der Operator ein Problem sieht, bei dem sich zwei oder mehr Wege ausschließen,
Dann liegt eine Karte in der Inbox: was los ist, die Ursache oder „nicht gemessen“, jeder Weg mit dem, was daraus folgt, und welchen der Operator empfiehlt.
Und jeder Weg ist ein Tipp; „noch einmal gründlicher nachsehen“ und „als Box-Aufgabe weitergeben“ sind Wege wie die anderen, die Wege haben keine eigenen Karten daneben.
Und die Karte hat kein eigenes „Freigeben“, nur „Ablehnen“.

### L-68 Wer einen Weg wählt, schließt die anderen
Wenn jemand auf der Karte einen Weg antippt, in der Inbox, am Shell oder per Signal,
Dann sind die anderen Wege abgelehnt („anderer Weg gewählt“) und die Karte ist erledigt.
Und lehnt jemand die Karte ab, sind alle Wege abgelehnt.
Und „noch einmal gründlicher nachsehen“ schickt das Problem an den Operator zurück, „als Box-Aufgabe weitergeben“ legt eine Aufgabe für die Admins an; auf der Box ändert sich dabei nichts.

### L-51 Was nur der Owner tun kann, wird eine Box-Aufgabe
Wenn die Ursache eines roten Durchgangs etwas ist, das nur der Owner erledigen kann,
Dann steht dafür eine Box-Aufgabe bei den Admins, mit der Ursache als Grund.
Und der Operator gibt dabei nichts frei, umgeht keine Zustimmung und keine Sperre.

### L-11 Verkettete Freigaben laufen in ihrer Reihenfolge
Wenn eine Karte auf eine andere, einen Schlüssel oder eine Version wartet,
Dann kann der Owner sie vorab freigeben, und sie läuft erst, wenn das da ist, in ihrer Reihenfolge.
Und eine Karte, die auf einen Schlüssel wartet, verlinkt dessen Formular.

### L-48 Eine Vorbedingung lässt die Karte warten statt rot werden
Wenn beim Anwenden etwas fehlt, das nur der Owner tun kann,
Dann wird die Freigabe nicht rot, sondern legt eine Box-Aufgabe an und wartet darauf, schon freigegeben.
Und ist die Aufgabe erledigt, wendet Laib die Änderung von selbst an.

### L-49 Erweiterungen installieren bestätigt den Warnhinweis des Marktplatzes
Wenn der Owner eine Karte „Erweiterungen installieren“ für Home Assistant freigibt,
Dann sagt die Karte vorher, dass diese Freigabe sein Einverständnis zum Warnhinweis des Marktplatzes ist.
Und Laib bestätigt den Hinweis in Home Assistant für seinen eigenen Benutzer, nur wenn er neu ist, bevor es installiert.
Und lässt Home Assistant das nicht zu, wartet die Karte auf eine Aufgabe für den Owner, statt rot zu werden.

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
Wenn ein neueres Image für einen Dienst mit Daten vorgeschlagen wird und es keine Routine ist (L-70),
Dann muss ein Mensch es freigeben.
Und vor dem Update sichert Laib die Daten des Dienstes, auch bei einer Routine.

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

### L-55 Neue Vorlagen einer Laib-Version kommen als eine Karte
Wenn eine neue Laib-Version neue Vorlagen für mehrere Dienste mitbringt,
Dann sieht der Owner eine Karte „Neue Vorlagen aus Laib 2.31.0 für …“, darauf je Dienst in einem Satz, was er dem Haushalt bringt.
Und ein Tipp auf „Freigeben“ gibt alle frei; läuft ein Dienst danach nicht, nimmt Laib nur ihn zurück, die anderen bleiben.
Und eine noch offene Karte einer früheren Version geht in der neuen auf.

### L-56 Eine Vorlage, die nur Kommentare ändert, bringt keine Karte
Wenn sich an einer Vorlage nur Kommentare oder Leerzeilen ändern,
Dann gibt es keine Karte und keinen Neustart, nur eine Zeile im Journal.

### L-26 Die Box startet für ein System-Update nur im Fenster nach der Sicherung neu
Wenn ein Update des Betriebssystems bereitliegt,
Dann startet die Box nur zwei bis vier Stunden nach der Sicherung neu, nie während einer, und nur wenn alles grün ist.
Und `laib diag` sagt, dass ein Update wartet.

### L-53 Laib nimmt nur ein Update, das aus seinem eigenen Release stammt
Wenn nachts ein neues Laib-Release da ist,
Dann installiert die Box es nur, wenn seine Prüfsummen die Unterschrift von Laibs Release auf GitHub tragen.
Und fehlt die Unterschrift oder stammt sie von jemand anderem, bleibt die Box auf ihrer Version, und `laib diag` sagt auf Deutsch, warum.
Und ein Programm mit Versionsnummer kommt in genau der Fassung, die geprüft wurde; „Images festhalten“ hält auch diese fest.

### L-70 Ein normales Update spielt sich nachts selbst ein
Wenn nachts ein neues Image für einen laufenden Dienst da ist und das Modell und die Regeln es normal finden,
Dann spielt Laib es selbst ein: vorher die Sicherung, wo Daten migrieren können, danach die Prüfung, bei Rot zurück.
Und es kommt keine Karte, das Journal sagt „als Routine“.

### L-71 Ein Update, bei dem man aufpassen muss, kommt als Karte mit dem Grund zuerst
Wenn das Modell oder eine Regel „aufpassen“ sagt (große Version, Daten, die nicht zurückgehen, Wegfall, Bruch),
Dann kommt die Karte „Neue Versionen: …“, und der Grund steht vor der Zusammenfassung.
Und sagt der Änderungstext selbst „normal“, bleibt die Regel trotzdem.

### L-72 Ein fremdes Update wartet drei Tage, ein eigenes und ein Sicherheitsupdate nicht
Wenn ein neues Image eines fremden Projekts erscheint,
Dann wartet es drei Tage (oder so viele, wie die Vorlage sagt), und Laib zählt solange die Meldungen dazu auf GitHub.
Und häufen sich die Meldungen, kommt es als Karte mit den Links; ein Sicherheitsupdate und die eigenen Images warten nicht.

### L-73 Der Owner schaltet das Selbst-Einspielen aus
Wenn der Owner `laib updates --auto aus` sagt,
Dann kommt dafür eine Karte, und nach seiner Freigabe kommt jedes neue Image wieder als Karte.

### L-74 Am Morgen steht im Bericht, was sich eingespielt hat, mit dem Weg zurück
Wenn sich nachts ein Update selbst eingespielt hat,
Dann steht im Morgenbericht je Dienst eine Zeile „Heute Nacht eingespielt: immich v2.4.0 → v2.5.0 · Zurücknehmen: Inbox, Karte #31.“
Und die Karte steht einen Tag lang an ihrem Platz in der Inbox mit „Zurücknehmen“; was Laib bei Rot zurückgenommen hat, steht auch da.

### L-75 Was Rechte ändert, spielt sich nie selbst ein
Wenn neben einem Image auch ein öffentlicher Pfad, ein Schlüssel, ein Skript auf der Box, ein Port, box.yml, policy.yml oder eine Vorlage geändert wird,
Dann bleibt es eine Karte für den Owner und sagt „nicht von selbst eingespielt: …“.

### L-76 `laib updates` zeigt, was sich einspielt, was wartet und was angehalten ist
Wenn der Owner `laib updates` aufruft,
Dann steht bei jedem neuen Image, ob es sich einspielt oder bis wann es wartet,
Und darunter, was sich in sieben Tagen eingespielt hat und wie es ausging, und was für ihn angehalten ist, mit dem Grund.

### L-57 Der Kern liest keine Antworten aus dem Internet; ein Update wird vor dem Einspielen selbst geprüft
Wenn nachts nach neuen Versionen gesucht wird,
Dann fragt ein eigener Helfer ohne Schlüssel die Register und Änderungsnotizen, nicht der Kern, und die Karte „Neue Versionen“ sieht aus wie immer.
Und bevor ein freigegebenes Update eingespielt wird, prüft der Kern selbst die Prüfsumme und bei Laibs eigenen Programmen die Unterschrift.
Und stimmt eins davon nicht, läuft nichts davon, die Box bleibt auf ihrer Version und die Karte sagt, warum.

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

### L-52 Ein Helfer mit Fehler macht den Dienst gelb, nicht rot
Wenn ein Helfer eines Dienstes fehlschlägt, ein Timer oder der Auftrag, den er startet, der Dienst selbst aber läuft,
Dann ist der Dienst gelb: „Home Assistant läuft; ein Helfer meldet einen Fehler: …“ mit dem Helfer und seiner letzten Logzeile.
Und es kommt keine Nachricht „seit 10 Minuten rot“; rot machen ihn nur sein Hauptdienst und seine Prüfung.

### L-38 Ein geplanter Neustart ist kein Alarm
Wenn Laib einen Dienst gerade anwendet oder angekündigt neu startet,
Dann ist er währenddessen nicht rot und niemand wird geweckt.

### L-39 Ein Befund bringt seine Reparatur mit, die erst nach Freigabe läuft
Wenn `laib diag` etwas findet, das sich reparieren lässt,
Dann liegt eine Reparatur-Karte in der Inbox, die in Worten sagt, was sie tut.
Und sie läuft erst, wenn ein Mensch freigibt; dieselbe Reparatur liegt nur einmal da.

### L-58 Wünsche zu Protokollzeilen kommen einmal pro Woche als eine Karte
Wenn die tägliche Protokoll-Durchsicht Fehlermuster findet,
Dann legt der Operator dafür nicht täglich je eine Karte an, sondern sammelt sie über die Woche, ein Fund je Muster.
Und in der folgenden Woche liegt eine Karte „Protokoll-Funde der Woche“ in der Inbox, je Fund eine Zeile; ein „Freigeben“ gilt für alle Zeilen der Karte.

### L-40 Was Menschen lesen, ist ein Satz, kein Fehlertext
Wenn eine Mail, eine Karte oder die Status-Seite einen Fehler zeigt,
Dann steht dort ein deutscher Satz, der rohe Text nur unter „Details“.

## Familie und Admin

### L-41 Die Box-Seite ist nur für Admins
Wenn jemand die Box-Seite nicht über Caddy und Authelia aufruft,
Dann antwortet sie „nur über die Box-Seite“ und zeigt nichts.
Und sie behält, was Freigeben, Schlüssel und Reparatur brauchen, und den Morgenbericht.

### L-59 Laib steht ohne Homes
Wenn auf der Box kein Homes läuft,
Dann zeigt Laibs Status-Seite alles für den Betrieb: Bereitschaft, Befunde, Auslastung, den Morgenbericht und die Aufgaben der Admins.
Und jede Aufgabe lässt sich dort abhaken oder zurückstellen; eine Seite von Homes oder der Bote ist nur ein zusätzlicher Weg.
Und Links aus Nachrichten und wartenden Karten führen auf Laibs Seite.

### L-42 Die Apps der Familie gibt es nur zu Hause
Wenn jemand von außen eine App der Familie aufruft,
Dann kommt er nicht hin; nur www, die Anmeldung, Kontakt und Fotos stehen im Internet.
Und wo ein Dienst von außen erreichbar ist, fragt er dort den zweiten Faktor.

### L-66 Ein Gast-Link geht auch im WLAN zu Hause
Wenn ein Gast im WLAN zu Hause einen Link für Gäste öffnet (Kontakt austauschen, geteilte Fotos),
Dann findet sein Telefon die öffentliche Adresse der Box, wie unterwegs, und Chrome blockiert den Link nicht.
Und stimmt das nicht, nennt laib diag den Namen und was AdGuard und das Internet antworten.

### L-43 Homes lädt eine Bewohnerin ein
Wenn ein Admin in Homes jemanden einlädt,
Dann bekommt Homes den Einladungslink zurück, auch ohne Mail.
Und einen Admin einladen darf nur ein Mensch.

### L-44 Jeder Tipp antwortet sofort
Wenn jemand auf der Box-Seite einen Knopf tippt,
Dann endet es sichtbar, wo es begann: fertig, rot mit Grund und nächstem Schritt, oder wartend mit Grenze.

### L-54 Homes findet die Zeitleiste von selbst
Wenn die Zeitleiste (Reitti) auf der Box eingerichtet ist,
Dann schaltet Homes den Standortverlauf ein, ohne dass jemand eine Adresse einträgt.
Und wird sie entfernt, ist der Standortverlauf wieder aus; eine von Hand eingetragene Adresse gilt weiter.

## Haus

### L-45 Musik per Sprache läuft auf dem Standard-Lautsprecher des Raums
Wenn in einem Raum ein Standard-Lautsprecher gewählt ist und jemand dort „Spiele Einslive“ oder „Spiele Musik von Queen“ sagt,
Dann läuft die Musik auf diesem Lautsprecher, und die Antwort kommt weiter vom Sprachgerät.
Und ohne gewählten Standard spielt sie wie bisher auf dem Lautsprecher im Raum.

### L-60 Die Lautstärke per Sprache trifft den Lautsprecher, der im Raum gerade spielt
Wenn im Wohnzimmer Musik läuft und jemand dort „Lautstärke auf 25 Prozent“, „lauter“ oder „mach es leiser“ sagt,
Dann ändert sich die Lautstärke des Lautsprechers, der in diesem Raum gerade spielt, und das Sprachgerät antwortet „Lautstärke 25 Prozent.“
Und spielt dort nichts, gilt es für den Standard-Lautsprecher des Raums; gibt es keinen, sagt es „Ich finde hier keinen Lautsprecher.“
Und das Sprachgerät selbst wird dabei nicht leiser oder lauter.

### L-61 Ein Podcast spielt per Sprache mit seinem Namen
Wenn jemand „Spiele den Podcast Lage der Nation“ oder „Spiele die aktuelle Lage der Nation“ sagt,
Dann spielt der Podcast auf dem Lautsprecher des Raums, auf dem auch Radio und Musik laufen, und das Sprachgerät antwortet „Ich spiele den Podcast Lage der Nation.“
Und „Spiele Einslive“ und „Spiele Musik von Queen“ spielen weiter das Radio und die Musik.

### L-62 Solange das Sprachgerät zuhört, sind die Lautsprecher im Raum stumm
Wenn jemand das Sprachgerät weckt, während im selben Raum etwas spielt,
Dann werden alle Lautsprecher dieses Raums stumm, die gerade spielen.
Und sobald es nicht mehr zuhört, spätestens nach 20 Sekunden, sind genau diese wieder laut; was vorher schon stumm war, bleibt stumm.
Und ein neues Sprachgerät macht das nach der nächsten Aktualisierung von Home Assistant auch.

### L-47 Wer den Lautsprecher aus Versehen weckt, hört nur einen kurzen Ton statt einer langen Antwort
Wenn der Lautsprecher auf ein Gespräch am Tisch oder den Fernseher hin aufwacht und nichts hört, das an ihn gerichtet ist,
Dann sagt er nichts, sondern spielt nur einen kurzen, leisen Ton „düü-doow“.
Und eine echte Frage, Bitte oder ein Befehl bekommt die Antwort wie bisher, auch wenn die Prüfung gerade nicht antwortet.
Und auf der Box-Seite zählt dieses Aufwachen als „ohne Befehl“; was gesagt wurde, wird nirgends gespeichert.

### L-63 Der Owner richtet Automationen in der Werkstatt mit Sprache ein
Wenn der Owner in der Werkstatt sagt „Wenn ich mein Telefon nach 23 Uhr auf den Stand lege, soll das Bürolicht ausgehen“,
Dann zeigt ihm der Agent die Automation in Worten, und der Owner bekommt einen vierstelligen Code auf Signal; geändert wird Home Assistant erst, wenn er den Code in der Werkstatt nennt. Der Agent sieht den Code nie.
Und lässt sich der Code nicht schicken, wird nichts vorgemerkt, und der Agent sagt, warum.
Und Lesen (welche Automationen, welche Tags) braucht keinen Code; Schreiben, Löschen, Wiederherstellen und Schalten immer.
Und die Werkstatt hat dafür keinen eigenen Schlüssel zu Home Assistant: sie geht über Laib zum Automaten des Boten, und das Journal nennt jede bestätigte Änderung.

### L-64 Ein Tag mit der Adresse der Box öffnet die Homes-App
Wenn jemand mit der Homes-App einen Tag berührt, auf dem `https://www.<domain>/tag/…` steht,
Dann öffnet Android direkt die App, ohne Browser und ohne Anmeldung, weil die Box Android ohne Anmeldung bestätigt, dass die Adresse zur App gehört.
Und ohne App führt der Tag in den Browser, auf die Seite des Tags, erst nach der Anmeldung.
Und ohne Anmeldung gibt die Box dafür nur diese eine Datei heraus, sonst nichts von Homes.

### L-65 Der Scanner ist ein Gerät in Home Assistant
Wenn auf der Box ein Scanner eingerichtet ist und Mosquitto läuft,
Dann zeigt Home Assistant von selbst ein Gerät „Scanner“ mit dem Knopf „Scannen – Haushalt“ und seinem Zustand (bereit, scannt, wartet, fertig, fehler).
Und ein Druck auf den Knopf startet den Stapel wie „Für den Haushalt“ in Homes; „Für mich“ gibt es in Home Assistant nicht, weil dort jeder jeden Knopf drücken kann: das geht in Homes und mit dem Tag am Scanner.
Und verschwindet der Scanner aus der Box, verschwindet das Gerät auch aus Home Assistant; ein Neustart lässt das Gerät, wie es ist.
Und Home Assistant sieht nie, wem ein Stapel gehört oder was darin steht.

## Drucken

### L-46 Jedes Gerät im Heimnetz findet den Drucker ohne App; von außen ist er nicht erreichbar
Wenn im Heimnetz oder am USB der Box ein Drucker ist,
Dann findet jedes Handy und jeder Rechner zu Hause ihn im eigenen Druckmenü und druckt ohne Hersteller-App, auch Android.
Und ein Drucker, der das schon selbst kann, erscheint nicht doppelt; einer, der es nicht kann, heißt „… (über die Box)“.
Und von außerhalb des Heimnetzes ist der Druckserver nicht erreichbar, und seine Verwaltung ist nur auf der Box selbst.
