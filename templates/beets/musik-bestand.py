"""The Bestand (#727): the music an earlier run left unsorted is sorted once,
in the background. It runs inside the beets container (beets-bestand.service,
`podman exec -i … python3 -` with this file on stdin), from
beets-bestand.timer three times an hour (:00, :20, :40), one run at a
time (a lock), niced and on idle I/O.

What it sorts: every song in a folder named like „Unbekanntes Album“,
„Unknown“, „Album inconnu“ or „Untitled“ below an artist, everything under
„Unbekannter Künstler“/„Unknown Artist“, the loose songs at the music
folder's top (an unfinished download script's `__.N.mp3`) and the songs in
its leftover folders (a top folder starting with `_`, `simplemp3s`). The
spoken word the owner is asked about (BESTAND_GESPROCHEN_VORSCHLAG) and
what he confirmed (BESTAND_GESPROCHEN) are never sorted as music.

Each song is one `beet import -q -s` (a singleton: the songs of an unknown
album are rarely one album) through laib's overlay, bestand/beets.yaml:
`move: yes`, `quiet_fallback: skip`, `duplicate_action: skip`, MusicBrainz
at one request per 3 s, AcoustID through the user's chroma plugin, and
`--set laib_bestand=<id>`, so beets' library says where the song went. A
sure match is tagged and moved by beets after the user's `paths`; its
lyrics (.lrc/.txt of the same name) follow it under the new name. What
beets skipped (not sure, already in the library) moves to
music/_unklar/<Interpret>/… with its lyrics and a line in
_unklar/LIESMICH.txt; a song beets failed on is tried in three runs, then
it goes there too. When a folder holds no song left to do, the rest of it
(covers, .nfo, logs) follows to _unklar, a leftover folder to
_unklar/_reste/, and the emptied folders go. Nothing is deleted.
_unklar/.ignore keeps it out of Jellyfin's library.

Resume is the tree itself: what is still in an unknown folder is still to
do, beets' library counts what went in (laib_bestand), _unklar what did
not. A run takes at most BESTAND_JE_LAUF songs or BESTAND_MINUTEN minutes
and stops after three errors in a row (MusicBrainz away).

Spoken word (#727): BESTAND_GESPROCHEN names top folders of the music the
owner confirmed; each run moves them into the audiobooks folder
(Audiobookshelf's „Hörbücher & Hörspiele“), merged into a folder of that
name, never over a file that is there. The proposal and what moved are in
the state, bestand/stand.json, which musik-eingang.py folds into
laib status beets. Stdlib only."""
import ctypes, datetime, errno, fcntl, json, os, platform, re, shlex, shutil, subprocess, time

E = os.environ
MUSIK = E.get("BESTAND_MUSIK", "/music")
HOER = E.get("BESTAND_HOERBUECHER", "/audiobooks")
DATA = E.get("BEETS_DATA", "/config")
BEET = shlex.split(E.get("BEET", "beet"))
AN = E.get("MUSIK_BESTAND", "true").strip().lower() not in ("false", "0", "no", "nein", "off", "aus")
JE_LAUF = int(E.get("BESTAND_JE_LAUF", "30") or "30")
MINUTEN = float(E.get("BESTAND_MINUTEN", "25") or "25")
VERSUCHE = 3

ZUSTAND = os.path.join(DATA, "bestand")
STAND = os.path.join(ZUSTAND, "stand.json")
LOG = os.path.join(ZUSTAND, "import.log")
UNKLAR = "_unklar"
RESTE = "_reste"
OHNE = "Unbekannter Künstler"
AUDIO = (".mp3", ".flac", ".m4a", ".aac", ".ogg", ".oga", ".opus", ".wav", ".wma")
TEXTE = (".lrc", ".txt")
BLEIBT_OBEN = (".jpg", ".jpeg", ".png", ".webp", ".nfo")   # the library's own pictures and Jellyfin's files
ALBUM = re.compile(r"^(unbekanntes album|unknown album|unknown|album inconnu|untitled|unbenannt)( \(.*\)| - .*)?$", re.I)   # not „Unknown Pleasures“
KUENSTLER = re.compile(r"^(unbekannter künstler|unknown artist|unknown|artiste inconnu|various unknown)$", re.I)
RESTE_ORDNER = ("simplemp3s",)   # the download script's folder (#727); a top folder starting with _ is one too

NICHT_SICHER = "nicht sicher erkannt"
DOPPELT = "schon in der Musiksammlung (doppelt)"
KAPUTT = "beets konnte die Datei nicht lesen oder MusicBrainz nicht erreichen (%d Versuche)" % VERSUCHE


def jetzt_utc():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


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


def namen(wert):
    return [n.strip() for n in (wert or "").split(";") if n.strip()]


def leise():
    """nice 19 and idle I/O for this run and everything it starts (beet, fpcalc): days are fine, a slow box is not"""
    try:
        os.nice(19)
    except OSError:
        pass
    nr = {"x86_64": 251, "aarch64": 30}.get(platform.machine())
    if nr:
        try:   # ioprio_set(IOPRIO_WHO_PROCESS, self, IOPRIO_CLASS_IDLE)
            ctypes.CDLL(None, use_errno=True).syscall(nr, 1, 0, 3 << 13)
        except (OSError, AttributeError):
            pass


def frei(pfad):
    """pfad, or „name (2).ext“ beside it when that is taken"""
    if not os.path.lexists(pfad):
        return pfad
    stamm, ext = os.path.splitext(pfad)
    n = 2
    while os.path.lexists("%s (%d)%s" % (stamm, n, ext)):
        n += 1
    return "%s (%d)%s" % (stamm, n, ext)


def verschiebe(quelle, ziel):
    """a file to ziel (whose folder is made); across mounts copied whole, checked, then the source removed"""
    os.makedirs(os.path.dirname(ziel), exist_ok=True)
    try:
        os.rename(quelle, ziel)
        return
    except OSError as err:
        if err.errno != errno.EXDEV:
            raise
    teil = ziel + ".laib-teil"
    shutil.copy2(quelle, teil)
    if os.path.getsize(teil) != os.path.getsize(quelle):
        os.remove(teil)
        raise OSError("Kopie unvollständig: %s" % quelle)
    os.rename(teil, ziel)
    os.remove(quelle)


def leere_weg(ordner, bis):
    """the emptied folders in ordner, and above it up to (and with) bis; never a file"""
    for w, _, ns in os.walk(ordner, topdown=False):
        if not ns:
            try:
                os.rmdir(w)
            except OSError:
                pass
    oben = os.path.dirname(ordner)
    while oben == bis or oben.startswith(bis + "/"):
        try:
            os.rmdir(oben)
        except OSError:
            return
        oben = os.path.dirname(oben)


def texte(lied):
    """the lyrics beside a song: same name, .lrc or .txt"""
    stamm = os.path.splitext(lied)[0]
    return [stamm + e for e in TEXTE if os.path.isfile(stamm + e)]


class Bestand:
    def __init__(self, ausgenommen):
        self.ausgenommen = {n.casefold() for n in ausgenommen}  # „DIE SENDUNG MIT DER MAUS“ is „Die Sendung mit der Maus“
        self.behaelter = []   # (folder, target under _unklar, a leftover folder)
        self.lieder = []      # (song, its target under _unklar)

    def finde(self):
        """the containers to sort and their songs, fresh from the tree"""
        for name in sorted(os.listdir(MUSIK)):
            p = os.path.join(MUSIK, name)
            if name.startswith(".") or name == UNKLAR or name.casefold() in self.ausgenommen or os.path.islink(p):
                continue
            if os.path.isfile(p):
                if name.lower().endswith(AUDIO):
                    self.lieder.append((p, os.path.join(OHNE, name)))
                continue
            if name.startswith("_") or name in RESTE_ORDNER:
                self.ordner(p, os.path.join(RESTE, name), True)
            elif KUENSTLER.match(name):
                self.ordner(p, name, False)
            else:
                for w, ds, _ in os.walk(p):
                    ds.sort()
                    for d in list(ds):
                        if ALBUM.match(d):
                            q = os.path.join(w, d)
                            self.ordner(q, os.path.relpath(q, MUSIK), False)
                            ds.remove(d)

    def ordner(self, p, ziel, rest):
        self.behaelter.append((p, ziel, rest))
        for w, ds, ns in os.walk(p):
            ds.sort()
            for n in sorted(ns):
                if n.lower().endswith(AUDIO):
                    q = os.path.join(w, n)
                    self.lieder.append((q, os.path.join(ziel if not rest else os.path.relpath(p, MUSIK), os.path.relpath(q, p))))


class Lauf:
    def __init__(self, alt):
        self.alt = alt
        self.versuche = dict(alt.get("versuche", {}))
        self.einzelheiten = []
        self.genommen = self.einsortiert = self.unklar = 0
        self.id = "b%d" % time.time()

    def overlay(self):
        p = os.path.join(ZUSTAND, "beets.yaml")
        with open(p, "w") as f:
            f.write("# written by musik-bestand.py on every run (templates/beets, #727); edits are overwritten\n"
                    "directory: %s\n"
                    "import:\n  quiet: yes\n  quiet_fallback: skip\n  move: yes\n  copy: no\n  write: yes\n"
                    "  resume: no\n  incremental: no\n  duplicate_action: skip\n  log: %s\n"
                    "musicbrainz:\n  ratelimit: 1\n  ratelimit_interval: 3.0\n" % (MUSIK, LOG))
        return p

    def beet(self, konfig, *args, timeout=1800):
        try:
            p = subprocess.run(BEET + ["-c", konfig] + list(args), capture_output=True, text=True,
                               stdin=subprocess.DEVNULL, timeout=timeout)
            return p.returncode, p.stdout, p.stderr
        except (OSError, subprocess.SubprocessError) as err:
            return 127, "", str(err)

    def zaehle(self, konfig):
        rc, aus, _ = self.beet(konfig, "ls", "-f", "$id", "laib_bestand::.", timeout=600)
        return len([z for z in aus.splitlines() if z.strip()]) if rc == 0 else self.alt.get("einsortiert", 0)

    def unklar_ablegen(self, lied, ziel, grund):
        z = frei(os.path.join(MUSIK, UNKLAR, ziel))
        for t in texte(lied):
            verschiebe(t, frei(os.path.splitext(z)[0] + os.path.splitext(t)[1]))
        verschiebe(lied, z)
        self.liesmich([(os.path.relpath(z, os.path.join(MUSIK, UNKLAR)), grund)])
        self.unklar += 1

    def liesmich(self, zeilen):
        u = os.path.join(MUSIK, UNKLAR)
        os.makedirs(u, exist_ok=True)
        if not os.path.exists(os.path.join(u, ".ignore")):
            open(os.path.join(u, ".ignore"), "w").close()   # Jellyfin leaves a folder with .ignore out
        with open(os.path.join(u, "LIESMICH.txt"), "a") as f:
            if f.tell() == 0:
                f.write("Musik, die beets beim Einsortieren nicht sicher erkannt hat (#727). Nichts davon ist gelöscht.\n"
                        "Richtig benannt (Interpret - Titel) oder getaggt wieder in musik-eingang legen, "
                        "dann sortiert beets sie ein; oder hier lassen.\n\n")
            f.writelines("%s: %s\n" % z for z in zeilen)

    def lied(self, konfig, lied, ziel, nr):
        """one song through beet import; True when beets failed on it"""
        texte_vorher = texte(lied)
        open(LOG, "w").close()
        kennung = "%s-%d" % (self.id, nr)
        rc, aus, err = self.beet(konfig, "import", "-q", "-s", "--set", "laib_bestand=" + kennung, lied)
        rel = os.path.relpath(lied, MUSIK)
        self.genommen += 1
        if not os.path.lexists(lied):   # beets moved it: a sure match
            self.einsortiert += 1
            self.versuche.pop(rel, None)
            _, wo, _ = self.beet(konfig, "ls", "-p", "laib_bestand::^%s$" % re.escape(kennung), timeout=600)
            neu = next((z.strip() for z in wo.splitlines() if z.strip()), "")
            for t in texte_vorher:
                if neu and os.path.isdir(os.path.dirname(neu)):
                    verschiebe(t, frei(os.path.splitext(neu)[0] + os.path.splitext(t)[1]))
                else:
                    verschiebe(t, frei(os.path.join(MUSIK, UNKLAR, os.path.dirname(ziel), os.path.basename(t))))
            return False
        with open(LOG) as f:
            arten = [z.split(" ", 1)[0] for z in f if " " in z]
        if rc == 0 and arten:
            self.versuche.pop(rel, None)
            self.unklar_ablegen(lied, ziel, DOPPELT if arten[0].startswith("duplicate") else NICHT_SICHER)
            return False
        self.versuche[rel] = self.versuche.get(rel, 0) + 1
        self.einzelheiten.append("beet import %s: Exit %d, Versuch %d\n%s" % (rel, rc, self.versuche[rel],
                                                                               "\n".join((aus + err).strip().splitlines()[-10:])))
        if self.versuche[rel] >= VERSUCHE:
            self.versuche.pop(rel)
            self.unklar_ablegen(lied, ziel, KAPUTT)
        return True

    def reste(self, bestand):
        """what is left of a container with no song to do, and the loose leftovers at the top, into _unklar"""
        offen = {l for l, _ in bestand.lieder if os.path.lexists(l)}
        for p, ziel, rest in bestand.behaelter:
            if not os.path.isdir(p) or any(l.startswith(p + "/") for l in offen):
                continue
            if rest:
                for w, _, ns in os.walk(p, topdown=False):
                    if not ns and w != p:
                        try:
                            os.rmdir(w)
                        except OSError:
                            pass
                z = frei(os.path.join(MUSIK, UNKLAR, ziel))
                os.makedirs(os.path.dirname(z), exist_ok=True)
                os.rename(p, z)
                self.liesmich([(os.path.relpath(z, os.path.join(MUSIK, UNKLAR)), "Rest eines Download-Skripts")])
                continue
            for w, _, ns in os.walk(p):
                for n in ns:
                    q = os.path.join(w, n)
                    verschiebe(q, frei(os.path.join(MUSIK, UNKLAR, ziel, os.path.relpath(q, p))))
            leere_weg(p, os.path.join(MUSIK, os.path.relpath(p, MUSIK).split(os.sep)[0]))
        for name in sorted(os.listdir(MUSIK)):
            p = os.path.join(MUSIK, name)
            low = name.lower()
            if not os.path.isfile(p) or name.startswith(".") or low.endswith(AUDIO + BLEIBT_OBEN):
                continue
            if low.endswith(TEXTE) and any(os.path.splitext(l)[0] == os.path.splitext(p)[0] for l in offen):
                continue   # it follows its song
            z = frei(os.path.join(MUSIK, UNKLAR, RESTE, name))
            verschiebe(p, z)
            self.liesmich([(os.path.relpath(z, os.path.join(MUSIK, UNKLAR)), "Rest eines Download-Skripts")])


def gesprochen(bestaetigt, alt):
    """the confirmed spoken word into the audiobooks folder, merged, never over a file there"""
    verschoben, probleme = list(alt.get("verschoben", [])), []
    for name in bestaetigt:
        if "/" in name or name in (".", "..") or name.startswith(".") or name == UNKLAR:
            probleme.append({"name": name, "grund": "kein Ordnername in der Musik"})
            continue
        # the folder in any spelling („DIE SENDUNG MIT DER MAUS“), and what an earlier sort put into
        # _unklar before the name was confirmed: all of it goes under the confirmed spelling
        quellen = [os.path.join(b, n) for b in (MUSIK, os.path.join(MUSIK, UNKLAR)) if os.path.isdir(b)
                   for n in sorted(os.listdir(b)) if n.casefold() == name.casefold()]
        quellen = [q for q in quellen if os.path.isdir(q) and not os.path.islink(q)]
        if not quellen:
            if name not in verschoben:
                probleme.append({"name": name, "grund": "kein Ordner dieses Namens in der Musik"})
            continue
        geblieben = []
        for quelle in quellen:
            for w, ds, ns in os.walk(quelle):
                ds.sort()
                for n in sorted(ns):
                    q = os.path.join(w, n)
                    z = os.path.join(HOER, name, os.path.relpath(q, quelle))
                    if os.path.lexists(z):
                        geblieben.append(os.path.relpath(q, MUSIK))
                        continue
                    verschiebe(q, z)
            leere_weg(quelle, quelle)
        if geblieben:
            probleme.append({"name": name, "grund": "%d Datei(en) liegen schon gleichnamig in den Hörbüchern und bleiben in der Musik: %s"
                             % (len(geblieben), ", ".join(geblieben[:3]))})
        elif name not in verschoben:
            verschoben.append(name)
    return verschoben, probleme


def zaehle_unklar():
    u = os.path.join(MUSIK, UNKLAR)
    return sum(1 for _, _, ns in os.walk(u) for n in ns if n.lower().endswith(AUDIO))


def main():
    if not AN:
        print("Bestand: aus (MUSIK_BESTAND)")
        return
    os.makedirs(ZUSTAND, exist_ok=True)
    sperre = open(os.path.join(ZUSTAND, "lauf.lock"), "w")
    try:
        fcntl.flock(sperre, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        print("Bestand läuft schon: dieser Durchlauf lässt ihn in Ruhe")
        return
    leise()
    alt = lies(STAND, {})
    vorschlag, bestaetigt = namen(E.get("BESTAND_GESPROCHEN_VORSCHLAG")), namen(E.get("BESTAND_GESPROCHEN"))
    verschoben, probleme = gesprochen(bestaetigt, alt.get("gesprochen", {}))
    lauf = Lauf(alt)
    konfig = lauf.overlay()
    bestand = Bestand(vorschlag + bestaetigt)
    bestand.finde()
    start, fehler_folge = time.monotonic(), 0
    for nr, (lied, ziel) in enumerate(bestand.lieder):
        if lauf.genommen >= JE_LAUF or time.monotonic() - start > MINUTEN * 60 or fehler_folge >= 3:
            break
        if not os.path.lexists(lied):
            continue
        fehler_folge = fehler_folge + 1 if lauf.lied(konfig, lied, ziel, nr) else 0
    if fehler_folge >= 3:
        lauf.einzelheiten.append("drei Fehler hintereinander: dieser Lauf hört auf, der nächste versucht es wieder")
    lauf.reste(bestand)
    offen = sum(1 for l, _ in bestand.lieder if os.path.lexists(l))
    einsortiert, unklar = lauf.zaehle(konfig), zaehle_unklar()
    stand = {"at": jetzt_utc(), "einsortiert": einsortiert, "unklar": unklar, "offen": offen,
             "gesamt": einsortiert + unklar + offen, "fertig": offen == 0, "versuche": lauf.versuche,
             "gesprochen": {"vorschlag": [n for n in vorschlag if n.casefold() not in {b.casefold() for b in bestaetigt}
                                           and any(m.casefold() == n.casefold() and os.path.isdir(os.path.join(MUSIK, m)) for m in os.listdir(MUSIK))],
                            "verschoben": verschoben, "probleme": probleme},
             "lauf": {"at": jetzt_utc(), "genommen": lauf.genommen, "einsortiert": lauf.einsortiert, "unklar": lauf.unklar},
             "einzelheiten": "\n\n".join(lauf.einzelheiten)}
    schreib(STAND, stand)
    print("Bestand: %d genommen, %d einsortiert, %d unklar; offen %d" % (lauf.genommen, lauf.einsortiert, lauf.unklar, offen))


if __name__ == "__main__":
    main()
