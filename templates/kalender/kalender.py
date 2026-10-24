#!/usr/bin/env python3
"""kalender (#495): calendars from outside into Radicale, one way, every
quarter of an hour (kalender-abgleich.timer).

The owner's decision (2026-10-21): Radicale is the one place. Haus, the Bote
and Home Assistant read there. A calendar comes in as an address:

  google  Google's "Geheime Adresse im iCal-Format" of one calendar. Read only,
          no Google login, no OAuth app. The address is a secret.
  abo     a public ICS: Feiertage, Schulferien, Müllabfuhr, any address.

The owner enters it in Haus (haus#66); Haus hands it to laib (`laib calendar
subscribe`, the address on stdin), laib keeps the list in secrets.age as
KALENDER_ABOS (base64url of [{"person", "name", "kind", "title", "url",
"since"}]) and applies this template again, so it reaches the unit through
the environment, never argv. The address is never printed, logged or written
anywhere else: an error names the host only.

Each subscription is mirrored into <person>/google-<name> or
<person>/abo-<name> (person `haus`: the family's). This script writes as
Radicale's service account `kalender`, whose rights (templates/radicale)
allow exactly those collections and Home Assistant's: nobody's password. The
person only reads it. What the source drops, the next run removes, in that
collection only; an empty answer removes nothing and is a finding. Item paths
are scripts/import-google.py's (#414), uuid5 of the UID, so a Takeout import
of the same calendar is replaced, not doubled.

Home Assistant (HA_USER, its own Radicale account `homeassistant`) finds only
the calendars in its own home, as every CalDAV client does. So every mirror,
and each family calendar of Haus's account (haus/haushalt, haus/familie-*),
is copied into homeassistant/<person>-<collection>, read only for HA.

Each run writes laib-status.json (laib diag): yellow when a calendar has not
been mirrored for STALE_HOURS (2) or Google answers 404 (the address was
reset), red when Radicale refuses the service account. Each mirrored
collection carries {urn:laib:kalender}abgleich (the last good run) and
{urn:laib:kalender}problem, which Haus shows. Stdlib only.
"""
import base64, datetime, hashlib, json, os, re, sys, time, urllib.error, urllib.parse, urllib.request, uuid
import xml.etree.ElementTree as ET

NS_CAL = uuid.UUID("6f1a1c2e-9b1e-4b7a-9c2d-000000000001")   # import-google.py's (#414): the same paths
OBJECTS = ("VEVENT", "VTODO", "VJOURNAL")
DAV, CAL, LAIB = "{DAV:}", "{urn:ietf:params:xml:ns:caldav}", "urn:laib:kalender"
MAX_ICS = 20 << 20
MIRROR = re.compile(r"^[^/]+/(google|abo)-[a-z0-9-]+/$")   # the only collections a run ever deletes, besides HA's copies
FIX_HAUS = "in Haus → Kalender → „Kalender hinzufügen“ den Kalender neu verbinden"


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
        body = ["BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//laib//kalender//DE"]
        for tz in tzs:
            body += tz
        for c in comps:
            body += c
        items[uid] = fold(body + ["END:VCALENDAR"])
    return items


def href(uid):
    return "%s.ics" % uuid.uuid5(NS_CAL, uid)


def collection(abo):
    """where a subscription is mirrored: <person>/google-<name>/ or <person>/abo-<name>/"""
    return "%s/%s-%s/" % (abo["person"], "google" if abo.get("kind") == "google" else "abo", abo["name"])


def ha_collection(home, col):
    """Home Assistant's copy of a collection: homeassistant/<person>-<collection>/"""
    return home + col.rstrip("/").replace("/", "-") + "/"


# --- HTTP ---

class Fail(Exception):
    def __init__(self, what, code=0):
        super().__init__(what)
        self.code = code


def source_url(url, now):
    """the address as fetched: OpenHolidays wants a date range, last year to next year"""
    u = urllib.parse.urlsplit(url)
    if u.hostname != "openholidaysapi.org":
        return url
    q = [(k, v) for k, v in urllib.parse.parse_qsl(u.query) if k not in ("validFrom", "validTo")]
    year = datetime.datetime.fromtimestamp(now, datetime.timezone.utc).year
    q += [("validFrom", f"{year - 1}-01-01"), ("validTo", f"{year + 1}-12-31")]
    return urllib.parse.urlunsplit(u._replace(query=urllib.parse.urlencode(q)))


def fetch(url, now):
    """the ICS behind a subscription; an error names the host, never the address"""
    host = urllib.parse.urlsplit(url).hostname or "?"
    r = urllib.request.Request(source_url(url, now), headers={"Accept": "text/calendar", "User-Agent": "laib-kalender"})
    try:
        with urllib.request.urlopen(r, timeout=60) as f:
            body = f.read(MAX_ICS + 1)
    except urllib.error.HTTPError as e:
        raise Fail(f"{host} antwortet HTTP {e.code}", e.code)
    except (urllib.error.URLError, OSError) as e:
        raise Fail(f"{host} nicht erreichbar ({type(getattr(e, 'reason', e)).__name__})")
    if len(body) > MAX_ICS:
        raise Fail(f"{host}: mehr als 20 MB")
    text = body.decode("utf-8", "replace")
    if "BEGIN:VCALENDAR" not in text:
        raise Fail(f"{host} liefert keinen Kalender")
    return text


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;")


class Radicale:
    def __init__(self, url, user, pw):
        self.base = url.rstrip("/")
        self.auth = "Basic " + base64.b64encode(f"{user}:{pw}".encode()).decode()

    def call(self, method, path, body=b"", ctype="application/xml; charset=utf-8", depth="0", ok=(200, 201, 204, 207)):
        r = urllib.request.Request(self.base + "/" + urllib.parse.quote(path), data=body or None, method=method,
                                   headers={"Authorization": self.auth, "Content-Type": ctype, "Depth": depth})
        try:
            with urllib.request.urlopen(r, timeout=60) as f:
                return f.status, f.read()
        except urllib.error.HTTPError as e:
            data = e.read()
            if e.code in ok:
                return e.code, data
            raise Fail(f"radicale:{e.code}", e.code)
        except (urllib.error.URLError, OSError) as e:
            raise Fail(f"Radicale nicht erreichbar ({type(getattr(e, 'reason', e)).__name__})")

    def ensure(self, col, display):
        if self.call("PROPFIND", col, ok=(207, 404))[0] == 207:
            return
        self.call("MKCALENDAR", col, f'<?xml version="1.0" encoding="utf-8"?><C:mkcalendar xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:set><D:prop><D:displayname>{esc(display)}</D:displayname></D:prop></D:set></C:mkcalendar>'.encode())

    def ensure_home(self, home):
        if self.call("PROPFIND", home, ok=(207, 404))[0] == 404:
            self.call("MKCOL", home, ok=(201, 405))

    def note(self, col, ok_at, problem):
        """what Haus shows for the collection: the last good run, and what went wrong since"""
        p = (f"<L:abgleich>{ok_at}</L:abgleich>" if ok_at else "") + f"<L:problem>{esc(problem)}</L:problem>"
        self.call("PROPPATCH", col, f'<?xml version="1.0" encoding="utf-8"?><D:propertyupdate xmlns:D="DAV:" xmlns:L="{LAIB}"><D:set><D:prop>{p}</D:prop></D:set></D:propertyupdate>'.encode())

    def children(self, col, calendars=False):
        """the names below col: its .ics items, or its calendars"""
        _, body = self.call("PROPFIND", col, b'<?xml version="1.0" encoding="utf-8"?><D:propfind xmlns:D="DAV:"><D:prop><D:resourcetype/></D:prop></D:propfind>', depth="1")
        names = set()
        for resp in ET.fromstring(body).iter(DAV + "response"):
            path = urllib.parse.unquote(urllib.parse.urlsplit(resp.findtext(DAV + "href") or "").path).strip("/")
            name = path.rsplit("/", 1)[-1]
            if calendars and path != col.strip("/") and resp.find(f".//{DAV}resourcetype/{CAL}calendar") is not None:
                names.add(name)
            elif not calendars and name.endswith(".ics"):
                names.add(name)
        return names

    def ics(self, col):
        """a Radicale calendar as one ICS (GET on the collection)"""
        return self.call("GET", col, ok=(200,))[1].decode("utf-8", "replace")

    def put(self, col, name, text):
        self.call("PUT", col + name, text.encode(), "text/calendar; charset=utf-8")

    def delete(self, path):
        self.call("DELETE", path, ok=(200, 204, 404))


# --- one run ---

def load_abos(raw):
    raw = raw.strip()
    if not raw:
        return []
    abos = json.loads(base64.urlsafe_b64decode(raw + "=" * (-len(raw) % 4)))
    if not isinstance(abos, list) or not all(isinstance(a, dict) and a.get("url") and a.get("person") and a.get("name") for a in abos):
        raise ValueError("keine Liste von Abos")
    return abos


def mirror(dav, col, display, want, seen):
    """want: {uid: text}; seen: {href: sha256} of the last run. Returns the new seen."""
    dav.ensure(col, display)
    have = dav.children(col)
    items = {href(uid): text for uid, text in want.items()}
    now = {h: hashlib.sha256(t.encode()).hexdigest() for h, t in items.items()}
    for h, text in items.items():
        if h not in have or seen.get(h) != now[h]:
            dav.put(col, h, text)
    if items or not have:
        for h in sorted(have - set(items)):
            dav.delete(col + h)
    return now


def ago(seconds):
    return f"{int(seconds // 3600)} h" if seconds >= 3600 else f"{int(seconds // 60)} min"


def stamp(t):
    return datetime.datetime.fromtimestamp(t, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def why(e):
    """(severity, German sentence, fix) for a failure; a refused service account is red"""
    msg = str(e)
    if msg == "radicale:401":
        return "rot", "Radicale lehnt das Konto kalender ab", "laib apply auth, dann laib apply kalender"
    if msg == "radicale:403":
        return "rot", "Radicale verweigert dem Konto kalender diesen Kalender", "laib apply radicale (die Rechte), dann laib apply kalender"
    if msg == "radicale:409":
        return "gelb", "die Person hat Radicale noch nie geöffnet (ihr Ordner fehlt)", \
            "einmal mit dem Telefon (DAVx⁵) oder auf caldav.<domain>/.web anmelden; der nächste Abgleich holt alles"
    return "gelb", msg, "systemctl --user status kalender-abgleich.service; hält es an: " + FIX_HAUS


def every(abo, abo_every_days):
    """seconds between fetches: the subscription's every (30m, 12h, 30d), else by kind"""
    m = re.fullmatch(r"([1-9][0-9]{0,3})([mhd])", abo.get("every") or "")
    if m:
        return int(m[1]) * {"m": 60, "h": 3600, "d": 86400}[m[2]]
    return 0 if abo.get("kind") == "google" else abo_every_days * 86400


def run(data, raw, dav, ha_user, stale_hours, now=None, abo_every_days=30):
    now = now or time.time()
    state_path = os.path.join(data, "state.json")
    try:
        with open(state_path) as f:
            state = json.load(f)
    except (OSError, ValueError):
        state = {}
    try:
        abos = load_abos(raw)
    except (ValueError, TypeError) as e:
        return write_status(data, {"detail": "KALENDER_ABOS nicht lesbar", "findings": [{
            "severity": "rot", "detail": f"Kalender: KALENDER_ABOS ist keine Abo-Liste von laib calendar ({type(e).__name__})",
            "fix": "laib calendar list; im Zweifel die Kalender in Haus neu verbinden"}]}, now)
    ha_home = ha_user + "/" if ha_user else ""
    findings, new_state, total = [], {}, 0
    # (collection, shown name, where its items come from, the subscription or None)
    targets = [(collection(a), a.get("title") or a["name"], lambda a=a: fetch(a["url"], now), a) for a in abos]
    if ha_home:
        try:
            dav.ensure_home(ha_home)
            for name in sorted(dav.children("haus/", calendars=True)):
                if name == "haushalt" or name.startswith("familie-"):
                    targets.append(("haus/" + name + "/", name, lambda n=name: dav.ics("haus/" + n + "/"), None))
        except (Fail, ET.ParseError) as e:
            sev, text, fix = why(e)
            findings.append({"severity": sev, "detail": f"Kalender für Home Assistant: {text}", "fix": fix})
    for col, display, get, abo in targets:
        where = f"„{display}“ ({col.split('/')[0]})"
        cols = ([col] if abo else []) + ([ha_collection(ha_home, col)] if ha_home else [])
        # each calendar's own interval (#619): its `every`, else Google every run and a public abo
        # (Müllabfuhr, Feiertage) once in ABO_EVERY_DAYS. A failed or new one is asked again at once.
        if abo and every(abo, abo_every_days) > 0 and all(
                c in state and not state[c].get("problem") and now - state[c].get("ok", 0) < every(abo, abo_every_days) for c in cols):
            new_state.update({c: state[c] for c in cols})
            total += len(state[col].get("seen", {}))
            continue
        try:
            want, err = events(get()), None
        except Fail as e:
            want, err = None, e
        for c in cols:
            prev = state.get(c, {})
            entry = dict(prev)
            try:
                if err:
                    raise err
                entry["seen"] = mirror(dav, c, display, want, prev.get("seen", {}))
                if not want and prev.get("seen"):
                    entry["seen"] = prev["seen"]
                    findings.append({"severity": "gelb", "detail": f"Kalender {where}: die Quelle meldet keine Termine mehr, die Kopie bleibt stehen",
                                     "fix": "nachsehen, ob der Kalender geleert wurde; sonst löscht der nächste Lauf nach einem neuen Termin"})
                entry["ok"], entry["problem"] = now, ""
                dav.note(c, stamp(now), "")
                if c == cols[0]:
                    total += len(want)
            except (Fail, ET.ParseError) as e:
                sev, text, fix = why(e)
                gone = e is err and abo.get("kind") == "google" and e.code == 404   # the source, not Radicale
                if gone:
                    text, fix = "Google kennt die geheime Adresse nicht mehr (in Google zurückgesetzt?)", FIX_HAUS
                last = entry.get("ok")
                entry["problem"] = text
                if sev == "rot" or gone or not last or now - last > stale_hours * 3600:
                    since = f"seit {ago(now - last)} nicht" if last else "noch nie"
                    findings.append({"severity": sev, "detail": f"Kalender {where}: {since} abgeglichen: {text}", "fix": fix})
                try:
                    dav.note(c, stamp(last) if last else "", text)
                except Fail:
                    pass   # the collection is not there yet: the status file says it
            new_state[c] = entry
    # what was mirrored before and is no more (getrennt): its copy goes, and only the mirror's own collections
    for c in sorted(set(state) - set(new_state)):
        if MIRROR.match(c) or (ha_home and c.startswith(ha_home)):
            try:
                dav.delete(c)
            except Fail as e:
                new_state[c] = state[c]
                findings.append({"severity": "gelb", "detail": f"Kalender {c}: getrennt, aber nicht gelöscht ({why(e)[1]})", "fix": "laib logs radicale"})
    tmp = state_path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(new_state, f)
    os.replace(tmp, state_path)
    once = {}
    for f in findings:   # the HA copy fails as its source does: one finding
        once.setdefault(f["detail"], f)
    findings = list(once.values())
    detail = (f"{len(abos)} Kalender verbunden, {total} Termine" if abos else "noch kein Kalender verbunden") + \
        (", Kopien für Home Assistant" if ha_home else "") + (f", {len(findings)} mit Problem" if findings else "")
    return write_status(data, {"detail": detail, "findings": findings}, now)


def write_status(data, status, now):
    status["at"] = stamp(now)
    p = os.path.join(data, "laib-status.json")
    with open(p + ".tmp", "w") as f:
        json.dump(status, f, ensure_ascii=False)
    os.replace(p + ".tmp", p)
    return status


def main():
    dav = Radicale(os.environ.get("RADICALE_URL", "http://host.containers.internal:5232"),
                   os.environ.get("RADICALE_USER", "kalender"), os.environ.get("RADICALE_PASSWORD", ""))
    s = run(os.environ.get("DATA", "/data"), os.environ.get("KALENDER_ABOS", ""), dav,
            os.environ.get("HA_USER", ""), float(os.environ.get("STALE_HOURS") or 2),
            abo_every_days=float(os.environ.get("ABO_EVERY_DAYS") or 30))
    print(s["detail"])
    for f in s["findings"]:
        print(f"{f['severity']}: {f['detail']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
