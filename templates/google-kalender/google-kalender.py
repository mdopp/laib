#!/usr/bin/env python3
"""google-kalender (#495): the family's Google calendars into Radicale, one way.

The owner decided (2026-10-20): Google stays where appointments are made;
the box keeps a copy, so Home Assistant, Haus and the Bote see them. Every
quarter of an hour (google-kalender-sync.timer) this reads each person's
chosen Google calendars over Google's CalDAV with OAuth, and writes them as
that person over CalDAV into their own collection `google-<kalender>` in
Radicale: never into Radicale's files (#371), never back to Google. Towards
Google it only reads (REPORT); a mistake on the box deletes nothing there.
What Google no longer has goes from the box's copy too: the collection is
Google's mirror, nobody writes into it by hand.

Each item's path is the one scripts/import-google.py gives it, uuid5 of the
event's UID (#414): a Takeout import done before lands on the same paths,
and the first run replaces it instead of doubling it. That is why this is a
small script and not vdirsyncer, which names items itself.

  python3 google-kalender.py          one run (the timer's): writes /data/laib-status.json
  python3 google-kalender.py konten   asks the owner, once, for the Google client and
                                      each person; writes /data/konten.neu (README.md)

Its only secret is GOOGLE_KALENDER_KONTEN, base64url of
{"client_id", "client_secret", "konten": [{"user", "password", "token",
"kalender": [{"id", "name"}]}]}: one line systemd's Environment= takes as it
is. It reaches the container through the environment, never on argv, and
is never printed. Stdlib only.
"""
import base64, datetime, getpass, hashlib, json, os, re, sys, time, urllib.error, urllib.parse, urllib.request, uuid
import xml.etree.ElementTree as ET

NS_CAL = uuid.UUID("6f1a1c2e-9b1e-4b7a-9c2d-000000000001")   # import-google.py's (#414): the same paths
OBJECTS = ("VEVENT", "VTODO", "VJOURNAL")
GOOGLE_TOKEN = os.environ.get("GOOGLE_TOKEN_URL", "https://oauth2.googleapis.com/token")
GOOGLE_AUTH = "https://accounts.google.com/o/oauth2/v2/auth"
GOOGLE_CALDAV = os.environ.get("GOOGLE_CALDAV_URL", "https://apidata.googleusercontent.com/caldav/v2")
GOOGLE_LIST = os.environ.get("GOOGLE_LIST_URL", "https://www.googleapis.com/calendar/v3/users/me/calendarList")
SCOPE = "https://www.googleapis.com/auth/calendar"   # Google's CalDAV takes no narrower one; this script only reads
REDIRECT = "http://127.0.0.1:8765/"
DAV = "{DAV:}"
CAL = "{urn:ietf:params:xml:ns:caldav}"
FIX_KONTEN = "Einrichtung in templates/google-kalender/README.md: `konten` erneut ausführen, dann laib secret set GOOGLE_KALENDER_KONTEN und laib apply google-kalender"


# --- iCalendar, as scripts/import-google.py does it (tests/ holds the two to the same paths) ---

def unfold(text):
    return re.sub(r"\r?\n[ \t]", "", text).replace("\r\n", "\n").split("\n")


def blocks(lines, name):
    """the top-level BEGIN:name … END:name blocks, as line lists"""
    out, cur, depth = [], None, 0
    for line in lines:
        if cur is None and line == "BEGIN:" + name:
            cur, depth = [line], 1
        elif cur is not None:
            cur.append(line)
            if line.startswith("BEGIN:"):
                depth += 1
            elif line.startswith("END:"):
                depth -= 1
                if depth == 0:
                    out.append(cur)
                    cur = None
    return out


def prop(block, name):
    for line in block:
        key, _, value = line.partition(":")
        if key.split(";")[0].upper() == name:
            return value
    return ""


def with_line(block, line):
    return [block[0], line] + block[1:]


def fold(lines):
    out = []
    for line in lines:
        b = line.encode()
        while len(b) > 75:
            cut = 75
            while (b[cut] & 0xC0) == 0x80:
                cut -= 1
            out.append(b[:cut].decode())
            b = b" " + b[cut:]
        out.append(b.decode())
    return "\r\n".join(out) + "\r\n"


def events(text):
    """{uid: VCALENDAR text}: one item per UID with its recurrence overrides and the source's timezones"""
    lines = unfold(text)
    cal = (blocks(lines, "VCALENDAR") or [lines])[0]
    inner = cal[1:-1]
    tzs = blocks(inner, "VTIMEZONE")
    groups = {}
    for kind in OBJECTS:
        for comp in blocks(inner, kind):
            uid = prop(comp, "UID")
            if not uid:
                uid = "import-%s" % uuid.uuid5(NS_CAL, prop(comp, "SUMMARY") + "|" + prop(comp, "DTSTART"))
                comp = with_line(comp, "UID:" + uid)
            groups.setdefault(uid, []).append(comp)
    items = {}
    for uid, comps in groups.items():
        body = ["BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//laib//google-kalender//DE"]
        for tz in tzs:
            body += tz
        for c in comps:
            body += c
        items[uid] = fold(body + ["END:VCALENDAR"])
    return items


def href(uid):
    return "%s.ics" % uuid.uuid5(NS_CAL, uid)


def slug(name):
    return re.sub(r"[^a-z0-9]+", "-", name.lower().replace("ä", "ae").replace("ö", "oe").replace("ü", "ue").replace("ß", "ss")).strip("-") or "kalender"


def collection(name):
    """google-<kalender>, as import-google.py names a Takeout calendar"""
    s = slug(name)
    return s if s == "google" or s.startswith("google-") else "google-" + s


# --- HTTP ---

class Fail(Exception):
    def __init__(self, what, code=0):
        super().__init__(what)
        self.code = code


def call(method, url, body=b"", headers=None, ok=(200, 201, 204, 207)):
    r = urllib.request.Request(url, data=body if body else None, method=method, headers=headers or {})
    try:
        with urllib.request.urlopen(r, timeout=60) as f:
            return f.status, f.read()
    except urllib.error.HTTPError as e:
        data = e.read()
        if e.code in ok:
            return e.code, data
        raise Fail(f"{method} {urllib.parse.urlsplit(url).path}: HTTP {e.code}", e.code)
    except (urllib.error.URLError, OSError) as e:
        raise Fail(f"{method} {urllib.parse.urlsplit(url).netloc}: {getattr(e, 'reason', e)}")


def access_token(client_id, client_secret, refresh):
    form = urllib.parse.urlencode({"client_id": client_id, "client_secret": client_secret,
                                   "refresh_token": refresh, "grant_type": "refresh_token"}).encode()
    r = urllib.request.Request(GOOGLE_TOKEN, data=form, method="POST", headers={"Content-Type": "application/x-www-form-urlencoded"})
    try:
        with urllib.request.urlopen(r, timeout=30) as f:
            return json.load(f)["access_token"]
    except urllib.error.HTTPError as e:
        try:
            err = json.load(e).get("error", "")
        except ValueError:
            err = ""
        if err in ("invalid_grant", "unauthorized_client", "invalid_client"):
            raise Fail("token:" + err, e.code)
        raise Fail(f"Google-Anmeldung: HTTP {e.code}", e.code)
    except (urllib.error.URLError, OSError) as e:
        raise Fail(f"Google nicht erreichbar: {getattr(e, 'reason', e)}")


QUERY = (b'<?xml version="1.0" encoding="utf-8"?><C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">'
         b'<D:prop><D:getetag/><C:calendar-data/></D:prop><C:filter><C:comp-filter name="VCALENDAR"/></C:filter></C:calendar-query>')


def google_items(token, cal_id):
    """{uid: text} of one Google calendar, read only (REPORT)"""
    url = f"{GOOGLE_CALDAV}/{urllib.parse.quote(cal_id, safe='')}/events/"
    _, body = call("REPORT", url, QUERY, {"Authorization": "Bearer " + token, "Depth": "1",
                                          "Content-Type": "application/xml; charset=utf-8"}, ok=(207,))
    items = {}
    for resp in ET.fromstring(body).iter(DAV + "response"):
        data = resp.find(f".//{CAL}calendar-data")
        if data is not None and data.text:
            items.update(events(data.text))
    return items


class Radicale:
    def __init__(self, url, user, pw):
        self.base, self.user = url.rstrip("/"), user
        self.auth = "Basic " + base64.b64encode(f"{user}:{pw}".encode()).decode()

    def url(self, path):
        return self.base + "/" + urllib.parse.quote(self.user) + "/" + path

    def call(self, method, path, body=b"", ctype="application/xml; charset=utf-8", depth="0", ok=(200, 201, 204, 207)):
        try:
            return call(method, self.url(path), body, {"Authorization": self.auth, "Content-Type": ctype, "Depth": depth}, ok)
        except Fail as e:
            if e.code == 401:
                raise Fail("radicale:401", 401)
            raise

    def ensure(self, col, display):
        if self.call("PROPFIND", col, ok=(207, 404))[0] == 207:
            return
        d = display.replace("&", "&amp;").replace("<", "&lt;")
        self.call("MKCALENDAR", col, f'<?xml version="1.0" encoding="utf-8"?><C:mkcalendar xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:set><D:prop><D:displayname>{d}</D:displayname></D:prop></D:set></C:mkcalendar>'.encode())

    def hrefs(self, col):
        _, body = self.call("PROPFIND", col, b'<?xml version="1.0" encoding="utf-8"?><D:propfind xmlns:D="DAV:"><D:prop><D:getetag/></D:prop></D:propfind>', depth="1")
        names = set()
        for h in ET.fromstring(body).iter(DAV + "href"):
            name = urllib.parse.unquote((h.text or "").rstrip("/").rsplit("/", 1)[-1])
            if name.endswith(".ics"):
                names.add(name)
        return names

    def put(self, col, name, text):
        self.call("PUT", col + name, text.encode(), "text/calendar; charset=utf-8")

    def delete(self, col, name):
        self.call("DELETE", col + name, ok=(200, 204, 404))


# --- one run ---

def load_blob(raw):
    raw = raw.strip()
    cfg = json.loads(base64.urlsafe_b64decode(raw + "=" * (-len(raw) % 4)))
    if not cfg.get("client_id") or not cfg.get("client_secret") or not isinstance(cfg.get("konten"), list):
        raise ValueError("client_id, client_secret oder konten fehlen")
    return cfg


def mirror(dav, col, display, want, seen):
    """want: {uid: text}; seen: {href: sha256} of the last run. Returns (written, deleted, new seen)."""
    dav.ensure(col, display)
    have = dav.hrefs(col)
    items = {href(uid): text for uid, text in want.items()}
    now = {h: hashlib.sha256(t.encode()).hexdigest() for h, t in items.items()}
    written = deleted = 0
    for h, text in items.items():
        if h not in have or seen.get(h) != now[h]:
            dav.put(col, h, text)
            written += 1
    if items or not have:
        for h in sorted(have - set(items)):
            dav.delete(col, h)
            deleted += 1
    return written, deleted, now


def ago(seconds):
    return f"{int(seconds // 3600)} h" if seconds >= 3600 else f"{int(seconds // 60)} min"


def run(data, raw, radicale_url, stale_hours, now=None):
    now = now or time.time()
    state_path = os.path.join(data, "state.json")
    try:
        with open(state_path) as f:
            state = json.load(f)
    except (OSError, ValueError):
        state = {}
    findings, lines, total = [], [], 0
    if not raw.strip():
        status = {"detail": "noch kein Google-Konto eingerichtet", "findings": [{
            "severity": "gelb", "detail": "Google-Kalender: noch kein Konto eingerichtet (GOOGLE_KALENDER_KONTEN fehlt)",
            "fix": "Einrichtung in templates/google-kalender/README.md"}]}
        return write_status(data, status, now)
    try:
        cfg = load_blob(raw)
    except (ValueError, TypeError) as e:
        status = {"detail": "GOOGLE_KALENDER_KONTEN nicht lesbar", "findings": [{
            "severity": "rot", "detail": f"Google-Kalender: GOOGLE_KALENDER_KONTEN ist kein Ergebnis von `konten` ({type(e).__name__})",
            "fix": FIX_KONTEN}]}
        return write_status(data, status, now)
    new_state = {}
    for konto in cfg["konten"]:
        user = konto.get("user", "")
        try:
            token = access_token(cfg["client_id"], cfg["client_secret"], konto.get("token", ""))
            dav = Radicale(radicale_url, user, konto.get("password", ""))
        except Fail as e:
            token, err = None, e
        for kal in konto.get("kalender", []):
            key = f"{user}/{kal['id']}"
            col = collection(kal.get("name") or kal["id"]) + "/"
            prev = state.get(key, {})
            entry = dict(prev)
            try:
                if token is None:
                    raise err
                want = google_items(token, kal["id"])
                written, deleted, entry["seen"] = mirror(dav, col, kal.get("name") or kal["id"], want, prev.get("seen", {}))
                entry["ok"] = now
                total += len(want)
                lines.append(f"{user}/{col.rstrip('/')}: {len(want)}")
                if not want and prev.get("seen"):
                    findings.append({"severity": "gelb", "detail": f"Google-Kalender {user}/{col.rstrip('/')}: Google meldet keine Termine mehr, die Kopie bleibt stehen",
                                     "fix": "in Google nachsehen, ob der Kalender geleert wurde; sonst löscht der nächste Lauf nach einem Termin in Google"})
                    entry["seen"] = prev["seen"]
            except Fail as e:
                findings.append(problem(user, col, kal, e, entry.get("ok"), now, stale_hours))
            except ET.ParseError:
                findings.append(problem(user, col, kal, Fail("Antwort ist kein XML"), entry.get("ok"), now, stale_hours))
            new_state[key] = entry
    tmp = state_path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(new_state, f)
    os.replace(tmp, state_path)
    n = sum(len(k.get("kalender", [])) for k in cfg["konten"])
    detail = f"{n} Kalender von {len(cfg['konten'])} Personen, {total} Termine" + ("" if not findings else f", {len(findings)} mit Problem")
    return write_status(data, {"detail": detail, "findings": findings}, now)


def problem(user, col, kal, e, last_ok, now, stale_hours):
    where = f"{user}/{col.rstrip('/')}"
    msg = str(e)
    if msg.startswith("token:"):
        return {"severity": "rot", "detail": f"Google-Kalender {where}: Googles Zugang für {user} ist abgelaufen oder widerrufen ({msg[6:]})", "fix": FIX_KONTEN}
    if msg == "radicale:401":
        return {"severity": "rot", "detail": f"Google-Kalender {where}: Radicale lehnt {user} ab (Passwort geändert?)", "fix": FIX_KONTEN}
    since = f"seit {ago(now - last_ok)}" if last_ok else "noch nie"
    stale = not last_ok or now - last_ok > stale_hours * 3600
    return {"severity": "rot" if stale else "gelb",
            "detail": f"Google-Kalender {where}: Abgleich {since} nicht gelungen: {msg}",
            "fix": "systemctl --user status google-kalender-sync.service; hält es an: " + FIX_KONTEN}


def write_status(data, status, now):
    status["at"] = datetime.datetime.fromtimestamp(now, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    p = os.path.join(data, "laib-status.json")
    with open(p + ".tmp", "w") as f:
        json.dump(status, f, ensure_ascii=False)
    os.replace(p + ".tmp", p)
    return status


# --- the owner's one-time setup ---

def ask(text):
    print(text, end="", file=sys.stderr, flush=True)
    return sys.stdin.readline().strip()


def exchange(client_id, client_secret, code):
    form = urllib.parse.urlencode({"client_id": client_id, "client_secret": client_secret, "code": code,
                                   "redirect_uri": REDIRECT, "grant_type": "authorization_code"}).encode()
    r = urllib.request.Request(GOOGLE_TOKEN, data=form, method="POST", headers={"Content-Type": "application/x-www-form-urlencoded"})
    try:
        with urllib.request.urlopen(r, timeout=30) as f:
            got = json.load(f)
    except urllib.error.HTTPError as e:
        raise Fail(f"Google hat den Code abgelehnt (HTTP {e.code})")
    if not got.get("refresh_token"):
        raise Fail("Google gab keinen dauerhaften Zugang zurück; in myaccount.google.com/permissions den Zugang der App entfernen")
    return got["refresh_token"], got["access_token"]


def calendars(access):
    try:
        _, body = call("GET", GOOGLE_LIST, headers={"Authorization": "Bearer " + access}, ok=(200,))
        return [(c["id"], c.get("summaryOverride") or c.get("summary") or c["id"]) for c in json.loads(body).get("items", [])]
    except (Fail, ValueError, KeyError) as e:
        print(f"Kalenderliste nicht lesbar ({e}); ist die Google Calendar API im Projekt aktiviert?", file=sys.stderr)
        return []


def konten(data, radicale_url):
    print("Google-Kalender einrichten (#495). Nichts davon erscheint auf dem Bildschirm oder in einer Datei außer konten.neu.", file=sys.stderr)
    client_id = ask("Client-ID des Google-OAuth-Clients: ")
    client_secret = getpass.getpass("Clientschlüssel (verdeckt): ")
    out = []
    while True:
        user = ask("Laib-Benutzername der Person (z. B. anna): ")
        pw = getpass.getpass(f"Passwort von {user} (das für Kalender und Haus, verdeckt): ")
        try:
            Radicale(radicale_url, user, pw).call("PROPFIND", "", ok=(207, 404))
        except Fail as e:
            print(f"Radicale nimmt {user} nicht an ({'Passwort falsch' if str(e) == 'radicale:401' else e}); noch einmal.", file=sys.stderr)
            continue
        url = GOOGLE_AUTH + "?" + urllib.parse.urlencode({"client_id": client_id, "redirect_uri": REDIRECT, "response_type": "code",
                                                          "scope": SCOPE, "access_type": "offline", "prompt": "consent"})
        print(f"\n{user} öffnet diese Adresse im Browser und meldet sich bei Google an:\n\n{url}\n\n"
              "Am Ende zeigt der Browser „Seite nicht erreichbar“: das ist richtig. Die ganze Adresse aus der Adresszeile hier einfügen.", file=sys.stderr)
        back = ask("Adresse: ")
        code = urllib.parse.parse_qs(urllib.parse.urlsplit(back).query).get("code", [back])[0]
        try:
            refresh, access = exchange(client_id, client_secret, code)
        except Fail as e:
            print(f"{e}; {user} noch einmal.", file=sys.stderr)
            continue
        cals = calendars(access)
        for i, (cid, name) in enumerate(cals, 1):
            print(f"  {i}. {name}", file=sys.stderr)
        if cals:
            pick = ask("Welche Kalender? Nummern mit Komma, Enter für alle: ")
            chosen = cals if not pick else [cals[int(x) - 1] for x in pick.replace(" ", "").split(",") if x]
        else:
            ids = ask("Kalender-IDs mit Komma (Google Kalender → Einstellungen → Kalender-ID): ")
            chosen = [(x.strip(), x.strip()) for x in ids.split(",") if x.strip()]
        for cid, name in chosen:
            print(f"  {name} → {user}/{collection(name)}", file=sys.stderr)
        out.append({"user": user, "password": pw, "token": refresh, "kalender": [{"id": c, "name": n} for c, n in chosen]})
        if ask("Noch eine Person? [j/N] ").lower() not in ("j", "ja", "y"):
            break
    blob = base64.urlsafe_b64encode(json.dumps({"client_id": client_id, "client_secret": client_secret, "konten": out}).encode()).decode().rstrip("=")
    p = os.path.join(data, "konten.neu")
    fd = os.open(p, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        f.write(blob + "\n")
    print(f"\nfertig: {len(out)} Personen in konten.neu. Weiter mit Schritt 4 in der Anleitung (laib secret set …, dann die Datei löschen).", file=sys.stderr)
    return 0


def main():
    data = os.environ.get("DATA", "/data")
    radicale_url = os.environ.get("RADICALE_URL", "http://host.containers.internal:5232")
    if sys.argv[1:] == ["konten"]:
        return konten(data, radicale_url)
    if sys.argv[1:]:
        print(__doc__.split("\n\n")[2], file=sys.stderr)
        return 2
    s = run(data, os.environ.get("GOOGLE_KALENDER_KONTEN", ""), radicale_url, float(os.environ.get("STALE_HOURS") or 6))
    print(s["detail"])
    for f in s["findings"]:
        print(f"{f['severity']}: {f['detail']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
