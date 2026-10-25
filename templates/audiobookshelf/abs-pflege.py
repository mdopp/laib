"""Audiobookshelf's care (#646), one run: audiobookshelf-pflege.service feeds
this file on stdin to a stock Python image, every 30 minutes
(audiobookshelf-pflege.timer). The standard library only.

The owner: "wie die Bücher und Podcasts und co miserabel taggen und
pflegen". The folders are decent (<Autor>/<Titel>, a Hörspiel series as
<Serie>/<NNN> - <Titel>), the file tags are poor. So:

1. The one-time match. Each book without an ASIN gets ABS's quick match
   against audible.de once (POST /api/items/<id>/match). The quick match
   fills only what is empty (overrideDetails false, sent explicitly so the
   server setting "prefer matched metadata" cannot turn it on): title,
   author, narrator, description, cover, ASIN. An item the owner edited
   keeps the edit; an item that already has an ASIN is not asked at all.
   Paced: at most MATCH_PER_RUN items a run, MATCH_PAUSE_S apart; the next
   run goes on where this one stopped. Which items are done is kept in
   abgleich.json, so an item is matched once and never again, whatever
   the owner changes in ABS afterwards.
2. Chapters: Audible's (GET /api/search/chapters, region de) where the
   item has at most one chapter and Audible's runtime is within 1 %
   (at least 30 s) of the item's: a different edition gets none.
3. Hörspiel series: a folder with at least two children named
   "<Nummer> - <Titel>" (Die drei ??? as "001 - …") is a series; each
   child gets the folder as series with its number as sequence, and the
   bare title, where the match gave no series. Audible is asked for the
   bare title with the folder as author.
4. Cleaning up after the tags (#678). ABS's default precedence lets the
   album tag beat the folder, and metadata.json keeps it: Hohlbein's
   "Dunkel" was "CD 1" by "Dunkel", "Die Känguru-Chroniken" had a U+FFFD.
   A title that is broken that way (mojibake, only "CD n"/"Teil n", the
   author's or series' name, the folder's name plus "CD n") gets its
   folder's name, through ABS's API, which rewrites metadata.json; a
   folder that lost a letter itself ("h_llische") is left alone. Audible
   is asked up to three times: the title with the author, then with the
   umlauts restored ("Schuesse" → "Schüsse") and without the author. A
   series folder takes the name Audible gave most of its children ("Die
   drei ???", not the folder's "Die drei Fragezeichen"). An item without a
   hit is asked again once a question is new. Once per item and number of
   files: files of several CDs in one folder ("1-01", "CD 1-01") go in
   disc order through PATCH /api/items/<id>/tracks, the per-file chapters
   with them; copies of a file (same size and length) are a finding with
   the moves the owner may make. No file is moved or renamed.
5. The status (laib status audiobookshelf, laib diag): "Hörbücher: 140
   abgeglichen, 3 ohne Treffer: …" and a finding per item without a hit,
   in German, until the owner matched it in ABS (it then has an ASIN).
6. Podcasts: the feeds of the hand-made sync_podcasts.py on the share,
   read as data (its PODCASTS = {name: feed} literal, never executed),
   subscribed in ABS with auto-download on. A podcast folder ABS already
   holds under that name gets the feed and its episodes matched against
   the feed (titles without "&amp;"); a new one is created from the
   feed's own metadata, with its three newest episodes.

Nothing here deletes or rewrites an audio file. ABS down is a red status
and a failed run; an item ABS refuses is tried again next run."""
import ast, json, os, re, sys, time, urllib.error, urllib.parse, urllib.request
from datetime import datetime, timezone

E = os.environ
ABS = E.get("ABS_URL", "http://host.containers.internal:13378").rstrip("/")
PW = E.get("ABS_PASSWORD", "")
DATA, PODS = E.get("STATE_DIR", "/data"), E.get("PODCASTS_DIR", "/podcasts")
PER_RUN, PAUSE = int(E.get("MATCH_PER_RUN") or 25), float(E.get("MATCH_PAUSE_S") or 3)
PROVIDER, REGION = "audible.de", "de"
NUMBERED = re.compile(r"^\s*(\d{1,4})\s*[-–.]\s*(.+?)\s*$")
DISC_TAIL = re.compile(r"\s*[-–,(]?\s*\b(?:cd|dis[ck]|teil|part)\s*\d{1,3}\)?\s*$", re.I)   # „… CD 1“, „… - Teil 1“
DISC_TRACK = re.compile(r"^(?:(?:cd|dis[ck])\s*)?(\d{1,2})-(?:(?:cd|dis[ck])\s*\d{1,2}-)?(\d{1,3})(?!\d)", re.I)  # 1-01, CD 1-01, CD1-CD1-01
MOJIBAKE = re.compile("[\ufffdĂÃÂ]")
JUNK = {"unknown", "unknown title", "unbekannt"}
STATUS, STATE = os.path.join(DATA, "laib-status.json"), os.path.join(DATA, "abgleich.json")


def write(path, obj):
    with open(path + ".tmp", "w") as f:
        json.dump(obj, f, ensure_ascii=False, indent=1)
    os.replace(path + ".tmp", path)


def status(detail, findings):
    write(STATUS, {"at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": findings})


def req(method, path, body=None, token=None):
    h = {"Content-Type": "application/json", **({"Authorization": "Bearer " + token} if token else {})}
    r = urllib.request.Request(ABS + path, data=json.dumps(body).encode() if body is not None else None, headers=h, method=method)
    with urllib.request.urlopen(r, timeout=120) as resp:
        data = resp.read()
    try:
        return json.loads(data) if data else None
    except ValueError:
        return None  # "OK", text


def items(lib, token):
    out, page = [], 0
    while True:
        r = req("GET", f"/api/libraries/{lib}/items?limit=100&page={page}&minified=1", token=token)
        out += r.get("results") or []
        if not r.get("results") or len(out) >= r.get("total", 0):
            return out
        page += 1


def feeds():
    """the PODCASTS literal of sync_podcasts.py, as data: {name: feed url}; nothing runs"""
    try:
        tree = ast.parse(open(os.path.join(PODS, "sync_podcasts.py"), encoding="utf-8").read())
    except (OSError, SyntaxError):
        return {}
    for node in ast.walk(tree):
        if isinstance(node, ast.Assign) and any(isinstance(t, ast.Name) and t.id == "PODCASTS" for t in node.targets):
            try:
                v = ast.literal_eval(node.value)
            except ValueError:
                return {}
            return {str(k): str(u) for k, u in v.items() if str(u).startswith(("http://", "https://"))} if isinstance(v, dict) else {}
    return {}


def bare(folder):
    """the title in a folder name, without its number: „001 - Der Nebelturm“ is „Der Nebelturm“"""
    m = NUMBERED.match(folder)
    return m.group(2) if m else folder


def series_of(books):
    """{item id: (series, sequence, bare title, folder)} for "<Serie>/<Nummer> - <Titel>" with two or more such
    children. The series is the name Audible gave most of the folder's matched children („Die drei ???“), else the
    folder's: one Hörspiel folder, one series."""
    by_parent = {}
    for b in books:
        parts = (b.get("relPath") or "").split("/")
        if len(parts) >= 2 and (m := NUMBERED.match(parts[-1])):
            by_parent.setdefault("/".join(parts[:-1]), []).append((b, str(int(m.group(1))), m.group(2)))
    out = {}
    for p, kids in by_parent.items():
        if len(kids) < 2:
            continue
        folder, names = p.split("/")[-1], {}
        for b, _, _ in kids:
            md = b["media"]["metadata"]
            if md.get("asin") and (n := re.sub(r"\s+#[\d.]+$", "", (md.get("seriesName") or "").split(",")[0]).strip()):
                names[n] = names.get(n, 0) + 1
        name = max(names, key=names.get) if names and max(names.values()) * 2 > len(kids) else folder
        out.update({b["id"]: (name, seq, title, folder) for b, seq, title in kids})
    return out


def broken(title, parts):
    """a title the tags made up: mojibake, „CD 1“, the author's or series' name, the folder's name plus „CD 1“"""
    t = (title or "").strip()
    if not t or "\ufffd" in t or t.casefold() in JUNK or not DISC_TAIL.sub("", t).strip():
        return True
    if len(parts) >= 2 and t.casefold() == parts[-2].casefold():
        return True
    return bool(DISC_TAIL.search(t)) and DISC_TAIL.sub("", t).strip().casefold() in (parts[-1].casefold(), bare(parts[-1]).casefold())


def repair(b, series, token):
    """a broken title gets its folder's name; a book's author its folder's too where the tags put the title there"""
    md, parts = b["media"]["metadata"], (b.get("relPath") or "").split("/")
    new = bare(parts[-1]).strip()
    if not broken(md.get("title"), parts) or not new or MOJIBAKE.search(new) or "_" in new or new == md.get("title"):
        return
    patch = {"title": new}
    if len(parts) == 2 and b["id"] not in series and (md.get("authorName") or "").casefold() in (
            parts[-1].casefold(), (md.get("title") or "").casefold(), "", "unknown", "unknown artist"):
        patch["authors"] = [{"name": parts[0]}]
    req("PATCH", f"/api/items/{b['id']}/media", {"metadata": patch}, token=token)
    print(f"abs-pflege: „{md.get('title')}“ heißt nach seinem Ordner „{new}“")
    md["title"] = new
    if "authors" in patch:
        md["authorName"] = parts[0]


def umlauts(t):
    """„Schuesse“ is „Schüsse“, „Feuer“ stays: for Audible's search only, never for a file"""
    return re.sub(r"(?<![aeiouqAEIOUQ])([AaOoUu])e", lambda m: "ÄäÖöÜü"["AaOoUu".index(m.group(1))], t)


def queries(b, s):
    """what Audible is asked, at most three times: the folder's title (the tag's where the folder lost a letter to „_“)
    with the author; then the umlauts restored and no author (ABS falls back to the item's author on an empty one)"""
    md = b["media"]["metadata"]
    own = DISC_TAIL.sub("", bare((md.get("title") or "").strip())).strip() or md.get("title") or ""
    base = s[2] if s and not re.search("[_\ufffd]", s[2]) else own
    out = [(base, s[3] if s else md.get("authorName") or "")]
    for t in (umlauts(own), umlauts(base), base):
        if (t, " ") not in out:
            out.append((t, " "))
    return out[:3]


def disc_track(name, folder):
    """(disc, track) from „1-01 - …“, „CD 1-01-…“, „CD1-CD1-01 …“, also after the book's own name; else None"""
    stem, title = name.rsplit(".", 1)[0], bare(folder)
    if stem.casefold().startswith(title.casefold()):
        stem = stem[len(title):].lstrip(" -_")
    m = DISC_TRACK.match(stem)
    return (int(m.group(1)), int(m.group(2))) if m else None


def files_of(b, token, seen):
    """once per item and number of files: copies of a file (same size, same length) become a proposal for the
    owner; without copies, the files of several CDs are put in disc order through ABS's track API, the chapters
    with them. ABS sorts by track number alone when the disc is only in the file name: 1-01, 2-01, 1-02 …"""
    n = b["media"].get("numAudioFiles") or 0
    if n < 2 or (seen.get(b["id"]) or {}).get("n") == n:
        return
    media = (req("GET", f"/api/items/{b['id']}?expanded=1", token=token) or {}).get("media") or {}
    every = media.get("audioFiles") or []
    files = sorted((f for f in every if not f.get("exclude")), key=lambda f: f.get("index") or 0)
    folder = b["relPath"].split("/")[-1]
    name = lambda f: f["metadata"]["filename"]
    keep = lambda f: (bool(MOJIBAKE.search(name(f))), not disc_track(name(f), folder), name(f).startswith("_"), name(f))
    by_size = {}
    for f in files:
        by_size.setdefault(f["metadata"].get("size") or 0, []).append(f)
    dup = sorted(name(f) for size, g in by_size.items() if size and len(g) > 1
                 for f in sorted(g, key=keep)[1:] if abs(float(f.get("duration") or 0) - float(g[0].get("duration") or 0)) < 1)
    seen[b["id"]] = {"n": n, "dup": dup}
    pos = [disc_track(name(f), folder) for f in files]
    if dup or not all(pos) or len({p[0] for p in pos}) < 2 or len(set(pos)) < len(pos):
        return
    new = [f for _, f in sorted(zip(pos, files), key=lambda x: x[0])]
    if new == files:
        return
    req("PATCH", f"/api/items/{b['id']}/tracks", {"orderedFileData": [{"ino": f["ino"], "exclude": False} for f in new] +
                                                   [{"ino": f["ino"], "exclude": True} for f in every if f.get("exclude")]}, token=token)
    ch = media.get("chapters") or []
    if len(ch) == len(files) and all(abs((c["end"] - c["start"]) - float(f.get("duration") or 0)) < 1.5 for c, f in zip(ch, files)):
        title, t, out = {f["ino"]: c["title"] for c, f in zip(ch, files)}, 0.0, []
        for i, f in enumerate(new):
            out.append({"id": i, "start": t, "end": t + float(f["duration"]), "title": title[f["ino"]]})
            t += float(f["duration"])
        req("POST", f"/api/items/{b['id']}/chapters", {"chapters": out}, token=token)
    print(f"abs-pflege: {b['media']['metadata'].get('title')}: {len(new)} Dateien in CD-Reihenfolge")


def chapters(item, asin, token):
    """Audible's chapters when the item has none of its own and the runtime fits"""
    duration = float(item["media"].get("duration") or 0)
    if not asin or item["media"].get("numChapters", 0) > 1 or not duration:
        return False
    c = req("GET", f"/api/search/chapters?asin={urllib.parse.quote(asin)}&region={REGION}", token=token) or {}
    rt = float(c.get("runtimeLengthSec") or 0)
    if c.get("error") or not c.get("chapters") or abs(rt - duration) > max(30.0, duration / 100):
        return False
    req("POST", f"/api/items/{item['id']}/chapters", {"chapters": [
        {"id": n, "start": x["startOffsetMs"] / 1000, "end": (x["startOffsetMs"] + x["lengthMs"]) / 1000, "title": x.get("title") or f"Kapitel {n + 1}"}
        for n, x in enumerate(c["chapters"])]}, token=token)
    return True


def care_books(lib, token, state):
    books = items(lib["id"], token)
    # a library ABS never read in holds nothing to match (2026-10-25: „0 abgeglichen“ for hours):
    # ask for a scan when it is empty, and once a day so new folders come in; ABS scans in the background
    scans = state.setdefault("scan", {})
    if not books or time.time() - scans.get(lib["id"], 0) > 86400:
        if time.time() - scans.get(lib["id"], 0) > 86400:
            req("POST", f"/api/libraries/{lib['id']}/scan", token=token)
            scans[lib["id"]] = time.time()
        if not books:
            return "Hörbücher: noch nichts eingelesen – Audiobookshelf liest die Bibliothek gerade ein", []
    done, seen = state.setdefault("items", {}), state.setdefault("dateien", {})
    budget, findings = PER_RUN, []
    series = series_of(books)
    for b in books:
        md = b["media"]["metadata"]
        try:
            repair(b, series, token)
            if (s := series.get(b["id"])) and s[0] != s[3] and md.get("seriesName") == f"{s[3]} #{s[1]}":   # the folder's, from before
                req("PATCH", f"/api/items/{b['id']}/media", {"metadata": {"series": [{"name": s[0], "sequence": s[1]}]}}, token=token)
                md["seriesName"] = f"{s[0]} #{s[1]}"
            files_of(b, token, seen)
        except (urllib.error.URLError, OSError, ValueError, KeyError) as e:
            findings.append({"severity": "gelb", "detail": f"Hörbücher: „{md.get('title')}“ ließ sich nicht aufräumen ({e})",
                             "fix": "läuft beim nächsten Durchlauf wieder"})
        d = done.get(b["id"])   # without a hit before: asked again once there is a question it was not asked
        if d and d["r"] == "ohne Treffer" and not md.get("asin") and \
                any(f"{t}|{a}" not in d.get("q", []) for t, a in queries(b, series.get(b["id"]))):
            del done[b["id"]]
    for b in books:
        md = b["media"]["metadata"]
        if b["id"] in done:
            continue
        if md.get("asin"):  # matched before, by the owner or ABS: left as it is
            done[b["id"]] = {"r": "vorhanden", "title": md.get("title")}
            continue
        if budget <= 0:
            continue
        budget -= 1
        s, asked = series.get(b["id"]), []
        try:
            for title, author in queries(b, s):
                if asked and PAUSE:
                    time.sleep(PAUSE)
                asked.append(f"{title}|{author}")
                r = req("POST", f"/api/items/{b['id']}/match", {"provider": PROVIDER, "title": title, "author": author,
                                                                "overrideDetails": False, "overrideCover": False}, token=token) or {}
                if hit := not r.get("warning"):
                    break
            got = ((r.get("libraryItem") or {}).get("media") or {}).get("metadata") or {}
            if s and not (got.get("series") or got.get("seriesName") or md.get("seriesName")):
                patch = {"series": [{"name": s[0], "sequence": s[1]}]}
                if (got.get("title") or md.get("title")) == b["relPath"].split("/")[-1]:
                    patch["title"] = s[2]  # the folder's name, not the owner's: the bare title, the number is the sequence
                req("PATCH", f"/api/items/{b['id']}/media", {"metadata": patch}, token=token)
            ch = hit and chapters(b, got.get("asin"), token)
        except (urllib.error.URLError, OSError, ValueError) as e:
            findings.append({"severity": "gelb", "detail": f"Hörbücher: „{md.get('title')}“ konnte nicht abgeglichen werden ({e})",
                             "fix": "läuft beim nächsten Durchlauf wieder"})
            continue
        done[b["id"]] = {"r": "abgeglichen" if hit else "ohne Treffer", "title": md.get("title"), **({"kapitel": True} if ch else {}),
                         **({} if hit else {"q": asked})}
        print(f"abs-pflege: {md.get('title')}: {'abgeglichen' if hit else 'ohne Treffer'}" + (", Kapitel von Audible" if ch else ""))
        if PAUSE:
            time.sleep(PAUSE)
    # one the owner matched by hand since (it has an ASIN now) is no longer missing
    asin = {b["id"] for b in books if b["media"]["metadata"].get("asin")}
    for i, d in done.items():
        if d["r"] == "ohne Treffer" and i in asin:
            d["r"] = "von Hand"
    ids = {b["id"] for b in books}
    have = [d for i, d in done.items() if i in ids]
    ok = sum(d["r"] != "ohne Treffer" for d in have)
    miss = [d["title"] for d in have if d["r"] == "ohne Treffer"]
    left = len(books) - len(have)
    detail = f"Hörbücher: {len(books)} Titel, {ok} abgeglichen"
    if miss:
        detail += f", {len(miss)} ohne Treffer: " + ", ".join(miss[:10]) + (" …" if len(miss) > 10 else "")
    if left:
        detail += f", {left} noch offen ({PER_RUN} pro Durchlauf)"
    findings += [{"severity": "info", "detail": f"Hörbücher: „{t}“ hat bei Audible keinen Treffer",
                  "fix": "In Audiobookshelf das Buch öffnen → Bearbeiten → Abgleichen, Titel oder Autor anpassen und den richtigen Treffer wählen"}
                 for t in miss]
    share = os.path.basename(((lib.get("folders") or [{}])[0].get("fullPath") or "/audiobooks").rstrip("/"))
    findings += [{"severity": "info", "detail": f"Hörbücher: „{b['media']['metadata'].get('title')}“ enthält {len(e['dup'])} Dateien doppelt "
                                                "(gleich groß, gleich lang) und spielt sie zweimal",
                  "fix": f"Vorschlag, noch nichts verschoben: aus „{share}/{b['relPath']}“ nach „aussortiert/{share}/{b['relPath']}“ "
                         f"verschieben: {', '.join(e['dup'])}. Danach liest Audiobookshelf den Ordner neu ein und die Pflege ordnet den Rest"}
                 for b in books if (e := seen.get(b["id"])) and e.get("dup")]
    return detail, findings


def care_podcasts(lib, token, state):
    want = feeds()
    if not want:
        return "", []
    subs = state.setdefault("podcasts", {})
    have = items(lib["id"], token)
    findings = []
    for name, url in want.items():
        if url in subs:
            continue
        try:
            old = next((p for p in have if p["media"]["metadata"].get("feedUrl") == url), None) or \
                next((p for p in have if p.get("relPath") == name or p["media"]["metadata"].get("title") == name), None)
            if old:
                if old["media"]["metadata"].get("feedUrl") != url or not old["media"].get("autoDownloadEpisodes"):
                    req("PATCH", f"/api/items/{old['id']}/media", {"metadata": {"feedUrl": url}, "autoDownloadEpisodes": True}, token=token)
                req("POST", f"/api/podcasts/{old['id']}/match-episodes?override=1", token=token)  # the episodes' titles from the feed
                subs[url] = old["id"]
                print(f"abs-pflege: Podcast {name} abonniert (Ordner war da, Folgen nach dem Feed benannt)")
                continue
            feed = req("POST", "/api/podcasts/feed", {"rssFeed": url}, token=token)["podcast"]
            folder = (lib.get("folders") or [{}])[0]
            safe = re.sub(r'[\\/*?:"<>|]', "", name).strip() or "Podcast"
            new = req("POST", "/api/podcasts", {"path": folder["fullPath"].rstrip("/") + "/" + safe, "folderId": folder["id"], "libraryId": lib["id"],
                                                "media": {"metadata": {**feed.get("metadata", {}), "feedUrl": url}, "autoDownloadEpisodes": True}},
                      token=token)
            if feed.get("episodes"):
                req("POST", f"/api/podcasts/{new['id']}/download-episodes", feed["episodes"][:3], token=token)
            subs[url] = new["id"]
            print(f"abs-pflege: Podcast {name} abonniert, die drei neuesten Folgen kommen")
        except (urllib.error.URLError, OSError, ValueError, KeyError) as e:
            findings.append({"severity": "gelb", "detail": f"Podcasts: {name} ließ sich nicht abonnieren ({e})",
                             "fix": "läuft beim nächsten Durchlauf wieder; sonst in Audiobookshelf → Podcasts → Hinzufügen mit der Feed-Adresse"})
    n = sum(1 for u in want.values() if u in subs)
    return f"Podcasts: {n} abonniert, neue Folgen laden von selbst", findings


def main():
    os.makedirs(DATA, exist_ok=True)
    token = None
    for _ in range(int(E.get("ABS_WAIT_S", "60")) // 2 + 1):
        try:
            u = req("POST", "/login", {"username": "root", "password": PW})["user"]
            token = u.get("accessToken") or u.get("token")
            break
        except urllib.error.HTTPError as e:
            if e.code == 401:
                break
        except OSError:
            time.sleep(2)
    if not token:
        status("Audiobookshelf antwortet nicht oder lässt root nicht herein", [
            {"severity": "rot", "detail": "Audiobookshelf: die Pflege kommt nicht hinein", "fix": "laib inspect audiobookshelf"}])
        print("abs-pflege: Audiobookshelf does not let root in", file=sys.stderr)
        return 1
    try:
        state = json.load(open(STATE))
    except (OSError, ValueError):
        state = {}
    details, findings = [], []
    try:
        for lib in req("GET", "/api/libraries", token=token)["libraries"]:
            care = {"book": care_books, "podcast": care_podcasts}.get(lib.get("mediaType"))
            if care:
                d, f = care(lib, token, state)
                details += [d] if d else []
                findings += f
    finally:
        write(STATE, state)
    status("; ".join(details) or "Audiobookshelf hat noch keine Bibliothek", findings)
    print("abs-pflege: " + ("; ".join(details) or "nothing to do"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
