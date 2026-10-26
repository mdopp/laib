"""The Musik-Eingang (#639): what the family drops into the share's
musik-eingang folder goes into the music library, tagged, with no further
step. It runs inside the beets container (beets-eingang.service, `podman
exec -i … python3 -` with this file on stdin), once a minute from
beets-eingang.timer, one run at a time (a lock).

Each entry at the inbox's top is one upload: an audio file, a folder (an
album, nested folders too) or a .zip. An upload is taken only once its
size and mtime held still between two runs at least MUSIK_STABIL_S apart
(15 s; the timer makes it about a minute), for a folder over its whole
tree, as scan's inbox does (scan/scan.py eingang_fertig). Names starting
with "." (an uploader's `._<name>.part`) or "~", or ending in .part, .tmp,
.crdownload or .partial are ignored; a folder that holds one is still
being filled.

An upload moves into .in-arbeit/ first (a rename on the share, so nobody
changes it under the import), a zip is unpacked there safely (no path
leaving the folder, no drive letter, no symlink, no password, at most
MUSIK_ZIP_MAX_DATEIEN files and MUSIK_ZIP_MAX_BYTES unpacked, the actual
bytes counted, not the header's), and `beet import -q` takes it with
quiet_fallback asis (an album beets cannot match goes in with its own
tags instead of being skipped), move, and duplicate_action skip: what the
library holds already is never replaced. What beets moved is gone from
the inbox; whatever is left (not music, a duplicate, an error, a refused
archive) moves to _nicht-importiert/<zeit>/ in its folder's shape, with
GRUND.txt saying why in German. Nothing the import did not take is
deleted; only the emptied folders and a zip whose content was imported or
kept unpacked are.

After the run, one message per upload that imported or kept something:
bote-notify's payload {an, text} plus "at" (epoch), one JSON line each,
appended to musik-eingang/meldungen.jsonl (staged and renamed). It is for
the uploader Haus named in the sidecar `._<name>.von` (an LLDAP user,
which box.yml people names), else for NOTIFY_PERSON. beets is not on the
host network and bote-notify listens on the host's loopback only, so the
container never sends: musik-meldungen.sh does, on the host, as the unit's
ExecStartPost. MUSIK_MELDEN=false switches it off. Lines the sender left
(bote-notify away) are one line in einzelheiten; the import never waits.

The status, ${DATA_DIR}/beets/config/laib-status.json (laib status beets,
Haus): {"at", "detail", "einzelheiten", "findings", "wartet",
"letzte": [{"at", "eingang", "von", "titel", "alben", "namen", "ordner",
"nicht_importiert": [{"datei", "grund"}]}]}, the newest upload first.
The Bestand's line and findings (#727) come from musik-bestand.py's
bestand/stand.json: this script is the status file's one writer.
Stdlib only: the image (linuxserver/beets) has python3 and beet, no
unzip, no 7z, no unrar.
"""
import datetime, fcntl, json, os, re, shlex, shutil, stat, subprocess, sys, time, zipfile

E = os.environ
EINGANG = E.get("MUSIK_EINGANG", "/eingang")
DATA = E.get("BEETS_DATA", "/config")
BEET = shlex.split(E.get("BEET", "beet"))
STABIL_S = float(E.get("MUSIK_STABIL_S", "15"))
ZIP_MAX_BYTES = int(E.get("MUSIK_ZIP_MAX_BYTES", str(20 << 30)))   # a discography in FLAC fits, a disk does not
ZIP_MAX_DATEIEN = int(E.get("MUSIK_ZIP_MAX_DATEIEN", "5000"))
MELDEN = E.get("MUSIK_MELDEN", "true").strip().lower() not in ("false", "0", "no", "nein", "off", "aus")
NOTIFY_PERSON = E.get("NOTIFY_PERSON", "")

AUDIO = (".mp3", ".flac", ".m4a", ".aac", ".ogg", ".oga", ".opus", ".wav")
ARCHIV = (".rar", ".7z", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".zst")
UNFERTIG = (".part", ".tmp", ".crdownload", ".partial", "~")
NI, ARBEIT = "_nicht-importiert", ".in-arbeit"
ZUSTAND = os.path.join(DATA, "musik-eingang")
STATUS = os.path.join(DATA, "laib-status.json")
MELDUNGEN = os.path.join(ZUSTAND, "meldungen.jsonl")   # musik-meldungen.sh sends them from the host

KEINE_MUSIK = "keine Musikdatei"
DUPLIKAT = ("schon in der Musiksammlung (Duplikat): nicht noch einmal übernommen. Ist diese Fassung besser, "
            "im beets-Werkzeug von Hand ersetzen")
ARCHIV_GRUND = "kann ich nicht auspacken (nur ZIP): bitte als ZIP oder ausgepackt in musik-eingang legen"
ZIP_IM_ORDNER = "ZIP-Datei in einem Ordner: bitte die ZIP-Datei direkt in musik-eingang legen"
UNTERBROCHEN = "Ein früherer Durchlauf wurde unterbrochen: bitte zurück in musik-eingang legen"
FEHLER = "Import fehlgeschlagen (Einzelheiten: laib status beets); zurück in musik-eingang legen versucht es noch einmal"
NICHT_GENOMMEN = "beets hat die Datei nicht übernommen (Einzelheiten: laib status beets)"


class Abgelehnt(Exception):
    pass


def jetzt_utc():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def ignoriert(name):
    return name.startswith((".", "~")) or name.lower().endswith(UNFERTIG)


def signatur(pfad):
    """the upload's size and mtime, over the whole tree for a folder; None while a part is still staged"""
    st = os.lstat(pfad)
    if not stat.S_ISDIR(st.st_mode):
        return None if st.st_size == 0 else [[".", st.st_size, st.st_mtime_ns]]
    sig = []
    for wurzel, ordner, dateien in os.walk(pfad):
        ordner.sort()
        for name in sorted(dateien):
            if name.lower().endswith(UNFERTIG):
                return None
            p = os.path.join(wurzel, name)
            s = os.lstat(p)
            sig.append([os.path.relpath(p, pfad), s.st_size, s.st_mtime_ns])
        sig += [[os.path.relpath(os.path.join(wurzel, o), pfad) + "/", 0, 0] for o in ordner]
    return sig


def lies(pfad, leer):
    try:
        with open(pfad) as f:
            return json.load(f)
    except (OSError, ValueError):
        return leer


def schreib(pfad, daten):
    with open(pfad + ".tmp", "w") as f:
        json.dump(daten, f, ensure_ascii=False, indent=1)
    os.replace(pfad + ".tmp", pfad)


def entpacke(zip_pfad, ziel):
    """the zip into ziel, or Abgelehnt with the family's reason and nothing unpacked"""
    try:
        z = zipfile.ZipFile(zip_pfad)
    except (zipfile.BadZipFile, OSError) as err:
        raise Abgelehnt("die ZIP-Datei ist beschädigt oder unvollständig (%s)" % err)
    with z:
        glieder = []
        for i in z.infolist():
            name = i.filename.replace("\\", "/")
            teile = [t for t in name.split("/") if t not in ("", ".")]
            # a leading "/" only roots a name: it unpacks inside like any other (teile drops it), and a bare
            # "/" directory entry, which some shops' downloads carry, is nothing (2026-10-24); a drive is refused
            if len(name) > 1 and name[1] == ":":
                raise Abgelehnt("unsicherer Pfad in der ZIP-Datei (Laufwerk): %s" % i.filename)
            if ".." in teile:
                raise Abgelehnt("unsicherer Pfad in der ZIP-Datei (zeigt nach außen): %s" % i.filename)
            if stat.S_ISLNK(i.external_attr >> 16):
                raise Abgelehnt("unsicherer Eintrag in der ZIP-Datei (ein Verweis): %s" % i.filename)
            if i.flag_bits & 1:
                raise Abgelehnt("die ZIP-Datei ist mit einem Passwort geschützt")
            if teile and teile[0] != "__MACOSX" and not i.is_dir():   # macOS' resource forks, no music
                glieder.append((i, teile))
        if len(glieder) > ZIP_MAX_DATEIEN:
            raise Abgelehnt("zu viele Dateien in der ZIP-Datei (%d, höchstens %d)" % (len(glieder), ZIP_MAX_DATEIEN))
        summe = sum(i.file_size for i, _ in glieder)
        if summe > ZIP_MAX_BYTES:
            raise Abgelehnt("die ZIP-Datei ist ausgepackt zu groß (%.1f GB, höchstens %.1f GB)" % (summe / 1e9, ZIP_MAX_BYTES / 1e9))
        if shutil.disk_usage(os.path.dirname(ziel)).free < summe + (64 << 20):
            raise Abgelehnt("nicht genug freier Platz auf der Box zum Auspacken (%.1f GB nötig)" % (summe / 1e9))
        os.makedirs(ziel)
        geschrieben = 0
        try:
            for i, teile in glieder:
                p = os.path.join(ziel, *teile)
                if os.path.commonpath([os.path.realpath(os.path.dirname(p)) + "/", os.path.realpath(ziel)]) != os.path.realpath(ziel):
                    raise Abgelehnt("unsicherer Pfad in der ZIP-Datei: %s" % i.filename)
                os.makedirs(os.path.dirname(p), exist_ok=True)
                with z.open(i) as quelle, open(p, "wb") as aus:
                    while block := quelle.read(1 << 20):
                        geschrieben += len(block)
                        if geschrieben > summe or geschrieben > ZIP_MAX_BYTES:   # a header that lies about its size
                            raise Abgelehnt("die ZIP-Datei packt mehr aus, als sie angibt")
                        aus.write(block)
        except Abgelehnt:
            shutil.rmtree(ziel)
            raise
        except (zipfile.BadZipFile, OSError, EOFError, RuntimeError) as err:
            shutil.rmtree(ziel)
            raise Abgelehnt("die ZIP-Datei ist beschädigt oder unvollständig (%s)" % err)


def overlay():
    """beets' settings for the inbox, over the user's config.yaml (beet -c): written every run, laib's file"""
    p = os.path.join(ZUSTAND, "beets.yaml")
    with open(p, "w") as f:
        f.write("# written by musik-eingang.py on every run (templates/beets, #639); edits are overwritten\n"
                "import:\n  quiet: yes\n  quiet_fallback: asis\n  move: yes\n  copy: no\n  write: yes\n"
                "  resume: no\n  incremental: no\n  duplicate_action: skip\n"
                "  log: %s\n" % os.path.join(ZUSTAND, "import.log"))
    return p


def importiere(pfad, einzeln, konfig):
    """beet import -q; (exit code, the output's tail, the import log's lines)"""
    log = os.path.join(ZUSTAND, "import.log")
    open(log, "w").close()
    cmd = BEET + ["-c", konfig, "import", "-q"] + (["-s"] if einzeln else []) + [pfad]
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, stdin=subprocess.DEVNULL)
        rc, aus = p.returncode, (p.stdout + p.stderr)
    except OSError as err:
        rc, aus = 127, str(err)
    with open(log) as f:
        zeilen = [z.rstrip("\n").split(" ", 1) for z in f if " " in z]
    return rc, "\n".join(aus.strip().splitlines()[-15:]), zeilen


def namen(einzeln, seit, konfig):
    """what beets added since seit, by its own names: the titles of a singleton, else the albums ("" untagged)"""
    ab = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(seit))
    cmd = BEET + ["-c", konfig, "ls"] + (["-f", "$title", "singleton:true"] if einzeln else ["-a", "-f", "$album"]) + ["added:%s.." % ab]
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=300)
    except (OSError, subprocess.SubprocessError):
        return []
    return [z.strip() for z in p.stdout.splitlines()] if p.returncode == 0 else []


def absender(name):
    """the uploader Haus named in ._<name>.von, read and removed; "" without one"""
    p = os.path.join(EINGANG, "._%s.von" % name)
    try:
        with open(p) as f:
            wer = f.readline().strip().lower()
        os.remove(p)
    except (OSError, UnicodeDecodeError):
        return ""
    return wer if re.fullmatch(r"[a-z0-9][a-z0-9._@-]{0,63}", wer) else ""


def dateien(wurzel):
    return [os.path.join(w, n) for w, _, ns in os.walk(wurzel) for n in ns]


def frei(ordner, name):
    stamm, n = name, 2
    while os.path.lexists(os.path.join(ordner, name)):
        name, n = "%s (%d)" % (stamm, n), n + 1
    return name


class Lauf:
    def __init__(self):
        self.zeit = datetime.datetime.now().strftime("%Y-%m-%d_%H-%M-%S")
        self.ni = os.path.join(EINGANG, NI, self.zeit)
        self.uploads = []      # this run's entries for letzte
        self.einzelheiten = []
        self.gelb = False
        self.konfig = None

    def beiseite(self, arbeit, gruende):
        """what is left under arbeit into _nicht-importiert/<zeit>/, in its folder's shape, with GRUND.txt"""
        rest = sorted(dateien(arbeit))
        if not rest:
            return []
        os.makedirs(self.ni, exist_ok=True)
        oben = {}
        aus = []
        for p in rest:
            rel = os.path.relpath(p, arbeit)
            top, _, unter = rel.partition(os.sep)
            if top not in oben:
                oben[top] = frei(self.ni, top)
            ziel = os.path.join(self.ni, oben[top], unter) if unter else os.path.join(self.ni, oben[top])
            os.makedirs(os.path.dirname(ziel), exist_ok=True)
            os.rename(p, ziel)
            aus.append({"datei": os.path.relpath(ziel, self.ni), "grund": gruende(p)})
        with open(os.path.join(self.ni, "GRUND.txt"), "a") as f:
            if f.tell() == 0:
                f.write("Musik-Eingang, %s\nDiese Dateien hat beets nicht in die Musiksammlung übernommen. "
                        "Berichtigt wieder in musik-eingang legen, dann versuche ich es noch einmal.\n\n" % self.zeit.replace("_", " "))
            f.writelines("%s: %s\n" % (x["datei"], x["grund"]) for x in aus)
        return aus

    def aufraeumen(self, arbeit):
        """the emptied folders; never a file"""
        for w, ordner, ns in os.walk(arbeit, topdown=False):
            if not ns:
                try:
                    os.rmdir(w)
                except OSError:
                    pass

    def eintrag(self, name, titel=0, alben=0, nicht=(), von="", namen=()):
        e = {"at": jetzt_utc(), "eingang": name, "von": von, "titel": titel, "alben": alben, "namen": list(namen),
             "ordner": NI + "/" + self.zeit if nicht else "", "nicht_importiert": list(nicht)}
        self.uploads.append(e)

    def unterbrochen(self):
        """a run that died mid-import left work in .in-arbeit/: shown, never lost"""
        basis = os.path.join(EINGANG, ARBEIT)
        for alt in sorted(os.listdir(basis)) if os.path.isdir(basis) else []:
            arbeit = os.path.join(basis, alt)
            nicht = self.beiseite(arbeit, lambda p: UNTERBROCHEN)
            self.aufraeumen(arbeit)
            if nicht:
                self.gelb = True
                self.eintrag("(unterbrochener Durchlauf)", nicht=nicht)

    def upload(self, name, nr):
        arbeit = os.path.join(EINGANG, ARBEIT, "%s-%d" % (self.zeit, nr))
        os.makedirs(arbeit)
        pfad = os.path.join(arbeit, name)
        try:
            os.rename(os.path.join(EINGANG, name), pfad)
        except OSError:
            os.rmdir(arbeit)
            return
        von = absender(name)
        gruende = {}
        if os.path.isfile(pfad) and name.lower().endswith(".zip"):
            ziel = os.path.join(arbeit, frei(arbeit, name[:-4] or "zip"))
            try:
                entpacke(pfad, ziel)
                os.remove(pfad)          # its content is unpacked: imported or kept below
                pfad = ziel
            except Abgelehnt as err:
                gruende[pfad] = str(err)
        if pfad not in gruende:
            self.beet(pfad, gruende)
        titel, alben, genannt = gruende.pop("", (0, 0, []))
        nicht = self.beiseite(arbeit, lambda p: gruende.get(p) or (KEINE_MUSIK if not p.lower().endswith(AUDIO) else NICHT_GENOMMEN))
        self.aufraeumen(arbeit)
        if any(x["grund"] in (FEHLER, NICHT_GENOMMEN) for x in nicht):
            self.gelb = True
        self.eintrag(name, titel, alben, nicht, von, genannt)

    def beet(self, pfad, gruende):
        """beet import over one upload; gruende gets each file's reason, and "" (titles, albums)"""
        alle = dateien(pfad) if os.path.isdir(pfad) else [pfad]
        for p in alle:
            low = p.lower()
            if low.endswith(".zip"):
                gruende[p] = ZIP_IM_ORDNER
            elif low.endswith(ARCHIV):
                gruende[p] = ARCHIV_GRUND
        musik = [p for p in alle if p.lower().endswith(AUDIO)]
        if not musik:
            return
        if self.konfig is None:
            self.konfig = overlay()
        einzeln = not os.path.isdir(pfad)
        seit = time.time()
        rc, aus, log = importiere(pfad, einzeln, self.konfig)
        genommen = [p for p in musik if not os.path.lexists(p)]
        gruende[""] = (len(genommen), 0 if einzeln else len({os.path.dirname(p) for p in genommen}),
                       namen(einzeln, seit, self.konfig) if genommen else [])
        for p in musik:
            if p in genommen:
                continue
            art = next((a for a, w in log if p == w or any(p.startswith(x.rstrip("/") + "/") or p == x for x in w.split("; "))), "")
            if art.startswith("duplicate"):
                gruende[p] = DUPLIKAT
            elif art == "skip":
                gruende[p] = "beets hat sie übersprungen"
            else:
                gruende[p] = FEHLER if rc else NICHT_GENOMMEN
        if rc or any(gruende.get(p) in (FEHLER, NICHT_GENOMMEN) for p in musik):
            self.einzelheiten.append("beet import %s: Exit %d\n%s" % (os.path.basename(pfad), rc, aus))


    def melden(self):
        """one message per upload that imported or kept something, for the host's sender; staged and renamed"""
        zeilen = []
        for u in self.uploads:
            text, an = mitteilung(u), u["von"] or NOTIFY_PERSON
            if text and an:
                zeilen.append(json.dumps({"an": an, "text": text, "at": int(time.time())}, ensure_ascii=False) + "\n")
        if not MELDEN or not zeilen:
            return
        try:
            with open(MELDUNGEN) as f:
                alt = f.read()
        except OSError:
            alt = ""
        with open(MELDUNGEN + ".tmp", "w") as f:
            f.write(alt + ("\n" if alt and not alt.endswith("\n") else "") + "".join(zeilen))
        os.replace(MELDUNGEN + ".tmp", MELDUNGEN)


def mehrzahl(n, eins, viele):
    return "%d %s" % (n, eins if n == 1 else viele)


def aufzaehlung(namen, gesamt):
    """at most three names, then „und N weitere“ up to gesamt"""
    return ", ".join(namen[:3]) + (" und %d weitere" % (gesamt - len(namen[:3])) if gesamt > len(namen[:3]) else "")


def mitteilung(u):
    """the family's message for one upload, or None when it neither imported nor kept anything"""
    t, nicht = u["titel"], u["nicht_importiert"]
    if not t and not nicht:
        return None
    if t:
        einzeln = not u["alben"]
        n = t if einzeln else (len(u["namen"]) or u["alben"])     # beets' albums: CD1 and CD2 are one
        genannt = [x for x in u["namen"] if x] or [os.path.splitext(u["eingang"])[0] if einzeln else u["eingang"]]
        kopf = ("%s %s" % (mehrzahl(t, "Titel", "Titel"), "ist" if t == 1 else "sind") if einzeln else
                "%s (%s) %s" % (mehrzahl(n, "Album", "Alben"), mehrzahl(t, "Titel", "Titel"), "ist" if n == 1 else "sind"))
        kopf += " in der Musik: " + aufzaehlung(genannt, n)
    else:
        kopf = "Nichts Neues in der Musik"
    if nicht:
        gruende = ["%s – %s" % (os.path.basename(x["datei"]), re.split(r" \(|: ", x["grund"], maxsplit=1)[0]) for x in nicht]
        kopf += " · %s nicht übernommen: %s (%s in musik-eingang/%s)" % (
            mehrzahl(len(nicht), "Datei", "Dateien"), aufzaehlung(gruende, len(nicht)), "liegt" if len(nicht) == 1 else "liegen", NI)
    return "🎵 " + kopf


def zeile(lauf):
    if not lauf:
        return "Musik-Eingang: bereit, noch nichts importiert"
    t, a, n = lauf["titel"], lauf["alben"], lauf["nicht"]
    teil = ("zuletzt %s%s importiert" % (mehrzahl(t, "Titel", "Titel"), " (%s)" % mehrzahl(a, "Album", "Alben") if a else "")
            if t else "zuletzt nichts importiert")
    return "Musik-Eingang: " + teil + (", %s nicht importiert" % mehrzahl(n, "Datei", "Dateien") if n else "")


def zahl(n):
    return "{:,}".format(n).replace(",", ".")


def bestand_zeile(b):
    """the Bestand's line (#727), from musik-bestand.py's state; "" when there is nothing to sort"""
    if not b or not b.get("gesamt"):
        return ""
    return "Bestand: %s%s von %s einsortiert, %s unklar" % ("fertig, " if b.get("fertig") else "", zahl(b["einsortiert"]),
                                                         zahl(b["gesamt"]), zahl(b["unklar"]))


def bestand_befunde(b):
    """the songs to look through in music/_unklar, the spoken word waiting for the owner's yes (#727)"""
    aus = []
    if b.get("unklar"):
        aus.append({"severity": "info", "detail": "Bestand: %s nicht sicher erkannt" % mehrzahl(b["unklar"], "Lied", "Lieder"),
                    "fix": "sie liegen in music/_unklar/<Interpret>/, LIESMICH.txt sagt warum; richtig benannt wieder in "
                           "musik-eingang legen, dann sortiert beets sie ein", "einzelheiten": b.get("einzelheiten", "")})
    g = b.get("gesprochen") or {}
    if g.get("vorschlag"):
        liste = "; ".join(g["vorschlag"])
        aus.append({"severity": "info", "detail": "Gesprochenes für Audiobookshelf, wartet auf dein Ja: %s" % liste,
                    "fix": 'Stimmt die Liste, setze in den Werten von beets BESTAND_GESPROCHEN: "%s" (was keine Musik ist; '
                           'Musik daraus weglassen). Erst dann zieht der nächste Lauf diese Ordner aus music nach audiobooks; '
                           'bis dahin bleiben sie, wie sie sind' % liste, "einzelheiten": ""})
    for x in g.get("probleme") or []:
        aus.append({"severity": "gelb", "detail": "Gesprochenes: %s nicht ganz verschoben" % x["name"], "fix": x["grund"],
                    "einzelheiten": ""})
    return aus


def status(alt, lauf, wartet, hinweis=""):
    if lauf is None:
        lauf, letzte = alt.get("lauf"), alt.get("letzte", [])
    else:
        letzte = (list(reversed(lauf.pop("uploads"))) + alt.get("letzte", []))[:20]
    detail = zeile(lauf)
    if wartet:
        detail += " · %s noch kopiert" % ("1 Eingang wird" if wartet == 1 else "%d Eingänge werden" % wartet)
    findings = []
    if lauf and lauf["nicht"] and os.path.isdir(os.path.join(EINGANG, lauf["ordner"])):
        findings.append({"severity": "gelb" if lauf["gelb"] else "info",
                         "detail": "Musik-Eingang: %s nicht importiert" % mehrzahl(lauf["nicht"], "Datei", "Dateien"),
                         "fix": "sie liegen in musik-eingang/%s/, der Grund steht in GRUND.txt; berichtigt wieder in musik-eingang legen" % lauf["ordner"],
                         "einzelheiten": lauf["einzelheiten"]})
    bestand = lies(os.path.join(DATA, "bestand", "stand.json"), {})
    if bestand_zeile(bestand):
        detail += " · " + bestand_zeile(bestand)
    findings += bestand_befunde(bestand)
    schreib(STATUS, {"at": jetzt_utc(), "detail": detail, "einzelheiten": "\n\n".join(x for x in (lauf["einzelheiten"] if lauf else "", hinweis) if x),
                     "findings": findings, "eingang": "musik-eingang", "wartet": wartet, "lauf": lauf, "letzte": letzte})


def main():
    os.makedirs(ZUSTAND, exist_ok=True)
    sperre = open(os.path.join(ZUSTAND, "lauf.lock"), "w")
    try:
        fcntl.flock(sperre, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        print("Musik-Eingang läuft schon: dieser Durchlauf lässt ihn in Ruhe")
        return
    gesehen_pfad = os.path.join(ZUSTAND, "gesehen.json")
    gesehen, neu = lies(gesehen_pfad, {}), {}
    jetzt = time.time()
    lauf = Lauf()
    lauf.unterbrochen()
    bereit, wartet = [], 0
    for name in sorted(os.listdir(EINGANG)):
        if name.startswith("._") and name.endswith(".von") and not os.path.lexists(os.path.join(EINGANG, name[2:-4])):
            try:   # a sidecar whose upload never came
                if jetzt - os.lstat(os.path.join(EINGANG, name)).st_mtime > 86400:
                    os.remove(os.path.join(EINGANG, name))
            except OSError:
                pass
        if name == NI or ignoriert(name):
            continue
        try:
            sig = signatur(os.path.join(EINGANG, name))
        except OSError:
            continue
        alt = gesehen.get(name)
        if sig is not None and alt and alt["sig"] == sig and jetzt - alt["seit"] >= STABIL_S:
            bereit.append(name)
            continue
        neu[name] = alt if sig is not None and alt and alt["sig"] == sig else {"sig": sig, "seit": jetzt}
        wartet += 1
    for nr, name in enumerate(bereit, 1):
        lauf.upload(name, nr)
        print("Musik-Eingang:", name, "→", lauf.uploads[-1]["titel"], "Titel,", len(lauf.uploads[-1]["nicht_importiert"]), "nicht importiert")
    try:
        os.rmdir(os.path.join(EINGANG, ARBEIT))   # empty after a run that finished
    except OSError:
        pass
    schreib(gesehen_pfad, neu)
    try:
        with open(MELDUNGEN) as f:   # what the host's sender could not deliver last time
            offen = sum(1 for z in f if z.strip())
    except OSError:
        offen = 0
    hinweis = ""
    if offen:
        hinweis = ("%s nicht zugestellt: bote-notify war nicht erreichbar oder lehnte ab; der nächste Lauf "
                   "versucht es wieder, nach einem Tag verworfen (journalctl --user -u beets-eingang)"
                   % ("1 Mitteilung" if offen == 1 else "%d Mitteilungen" % offen))
    try:
        lauf.melden()
    except Exception as err:   # a message never fails the import
        lauf.einzelheiten.append("Mitteilung nicht geschrieben: %s" % err)
    ergebnis = None
    if lauf.uploads:
        ergebnis = {"at": jetzt_utc(), "titel": sum(u["titel"] for u in lauf.uploads), "alben": sum(u["alben"] for u in lauf.uploads),
                    "nicht": sum(len(u["nicht_importiert"]) for u in lauf.uploads), "ordner": NI + "/" + lauf.zeit,
                    "gelb": lauf.gelb, "einzelheiten": "\n\n".join(lauf.einzelheiten), "uploads": lauf.uploads}
    status(lies(STATUS, {}), ergebnis, wartet, hinweis)


if __name__ == "__main__":
    main()
