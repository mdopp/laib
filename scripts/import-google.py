#!/usr/bin/env python3
"""#372, ADR 0017 (4.): Google appointments and contacts into Radicale again,
as a one-shot import, not a sync. Takes a Google Takeout export (the zip, or
its Calendar .ics and Contacts .vcf files) and PUTs every event and card into
the person's own Radicale collections over CalDAV/CardDAV, as that person
(Radicale is owner_only), never into Radicale's files. Re-running it
duplicates nothing: each item's path comes from its UID.

What solaris-import-google did (an upload page writing into radicale/data),
kept where it was right: one item per UID with the recurrence overrides that
share it, wrapped with the timezones of the source; a stable UID where Google
gave none (summary and start, or name and first mail/phone for a card); FN
for a card that has only a number. Stdlib only.

  python3 scripts/import-google.py --url https://caldav.<domain> --user <uid> takeout.zip
    [--calendar <name>]   one calendar for every .ics (default: each file's own name)
    [--password-stdin]    the password from stdin; otherwise it is asked for
"""
import argparse, base64, getpass, re, sys, urllib.error, urllib.parse, urllib.request, uuid, zipfile
from pathlib import Path

NS_CAL = uuid.UUID("6f1a1c2e-9b1e-4b7a-9c2d-000000000001")   # solaris-import-google's: the same UIDs
NS_CARD = uuid.UUID("6f1a1c2e-9b1e-4b7a-9c2d-000000000002")
OBJECTS = ("VEVENT", "VTODO", "VJOURNAL")


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


def with_line(block, line):   # insert a property right after BEGIN
    return [block[0], line] + block[1:]


def fold(lines):
    out = []
    for line in lines:
        b = line.encode()
        while len(b) > 75:
            cut = 75
            while (b[cut] & 0xC0) == 0x80:   # never inside a UTF-8 sequence
                cut -= 1
            out.append(b[:cut].decode())
            b = b" " + b[cut:]
        out.append(b.decode())
    return "\r\n".join(out) + "\r\n"


def events(text):
    """(calendar name from the file, {uid: VCALENDAR text})"""
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
        body = ["BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//laib//import-google//DE"]
        for tz in tzs:
            body += tz
        for c in comps:
            body += c
        items[uid] = fold(body + ["END:VCALENDAR"])
    return prop(cal, "X-WR-CALNAME"), items


def cards(text):
    items = {}
    for card in blocks(unfold(text), "VCARD"):
        mails = sorted(v.lower() for v in (prop([l], "EMAIL") for l in card) if v)
        tels = sorted("".join(c for c in v if c.isdigit()) for v in (prop([l], "TEL") for l in card) if v)
        fn = prop(card, "FN")
        if not fn:
            fn = next((v for v in (prop([l], "EMAIL") or prop([l], "TEL") for l in card) if v), "")
            if not fn:
                continue
            card = with_line(card, "FN:" + fn)
        uid = prop(card, "UID")
        if not uid:
            ident = fn.lower() + ("|" + mails[0] if mails else "|" + tels[0] if tels else "")
            uid = "import-%s" % uuid.uuid5(NS_CARD, ident)
            card = with_line(card, "UID:" + uid)
        items[uid] = fold(card)
    return items


def slug(name):
    return re.sub(r"[^a-z0-9]+", "-", name.lower().replace("ä", "ae").replace("ö", "oe").replace("ü", "ue").replace("ß", "ss")).strip("-") or "kalender"


class Dav:
    def __init__(self, url, user, pw):
        self.base, self.user = url.rstrip("/"), user
        self.auth = "Basic " + base64.b64encode(f"{user}:{pw}".encode()).decode()

    def call(self, method, path, body=b"", ctype="application/xml; charset=utf-8", ok=(200, 201, 204, 207)):
        r = urllib.request.Request(self.base + "/" + urllib.parse.quote(self.user) + "/" + path, data=body, method=method,
                                   headers={"Authorization": self.auth, "Content-Type": ctype, "Depth": "0"})
        try:
            with urllib.request.urlopen(r, timeout=30) as f:
                return f.status
        except urllib.error.HTTPError as e:
            if e.code in ok:
                return e.code
            raise SystemExit(f"{method} {self.user}/{path}: HTTP {e.code}" + (" (user or password wrong)" if e.code == 401 else ""))

    def collection(self, path, kind, display):
        if self.call("PROPFIND", path, ok=(207, 404)) == 207:
            return
        d = display.replace("&", "&amp;").replace("<", "&lt;")
        if kind == "calendar":
            self.call("MKCALENDAR", path, f'<?xml version="1.0" encoding="utf-8"?><C:mkcalendar xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:set><D:prop><D:displayname>{d}</D:displayname></D:prop></D:set></C:mkcalendar>'.encode())
        else:
            self.call("MKCOL", path, f'<?xml version="1.0" encoding="utf-8"?><D:mkcol xmlns:D="DAV:" xmlns:CR="urn:ietf:params:xml:ns:carddav"><D:set><D:prop><D:resourcetype><D:collection/><CR:addressbook/></D:resourcetype><D:displayname>{d}</D:displayname></D:prop></D:set></D:mkcol>'.encode())

    def put(self, path, uid, text, ctype):
        href = "%s%s" % (uuid.uuid5(NS_CAL, uid), ".ics" if ctype == "text/calendar" else ".vcf")
        self.call("PUT", path + href, text.encode(), ctype + "; charset=utf-8")


def sources(paths):
    """(name, text) of every .ics and .vcf in the given zips, folders and files"""
    for p in map(Path, paths):
        if p.suffix == ".zip":
            with zipfile.ZipFile(p) as z:
                for n in sorted(z.namelist()):
                    if n.lower().endswith((".ics", ".vcf")):
                        yield n, z.read(n).decode("utf-8", "replace")
        elif p.is_dir():
            yield from sources(sorted(str(f) for f in p.rglob("*") if f.suffix.lower() in (".ics", ".vcf")))
        else:
            yield str(p), p.read_text(encoding="utf-8", errors="replace")


def main():
    a = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    a.add_argument("--url", required=True)
    a.add_argument("--user", required=True)
    a.add_argument("--calendar", default="")
    a.add_argument("--password-stdin", action="store_true")
    a.add_argument("export", nargs="+")
    args = a.parse_args()
    pw = sys.stdin.readline().rstrip("\n") if args.password_stdin else getpass.getpass(f"Radicale-Passwort für {args.user}: ")
    dav = Dav(args.url, args.user, pw)
    n_ev = n_card = 0
    for name, text in sources(args.export):
        if name.lower().endswith(".ics"):
            calname, items = events(text)
            display = args.calendar or calname or Path(name).stem
            col = slug(display) + "/"
            dav.collection(col, "calendar", display)
            for uid, item in items.items():
                dav.put(col, uid, item, "text/calendar")
            n_ev += len(items)
            print(f"{display}: {len(items)} Termine → {args.user}/{col}")
        else:
            items = cards(text)
            dav.collection("google-kontakte/", "addressbook", "Google-Kontakte")
            for uid, item in items.items():
                dav.put("google-kontakte/", uid, item, "text/vcard")
            n_card += len(items)
            print(f"{Path(name).name}: {len(items)} Kontakte → {args.user}/google-kontakte/")
    print(f"fertig: {n_ev} Termine, {n_card} Kontakte; ein zweiter Lauf überschreibt dieselben, er verdoppelt nichts")
    return 0


if __name__ == "__main__":
    sys.exit(main())
