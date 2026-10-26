"""Failed logins for the owner (#653). auth-anmeldungen.service runs this in a
stock Python image every five minutes (the script on stdin, as kalender and
audiobookshelf-pflege do), with no network: Authelia's data folder and
LLDAP's read-only, its own folder STATE_DIR writable.

It reads the rows of Authelia's authentication_logs since the last run (its
cursor in STATE_DIR/stand.json) from a copy of db.sqlite3, and LLDAP's
user names and mail addresses from a copy of users.db, so neither database
is ever opened for writing. Authelia logs neither passwords nor what was
typed as one; nothing here reads or writes one.

One message per incident, not per attempt (#653's thresholds):
- a ban by Authelia's regulation (AUTH_MAX_RETRIES failures for one name
  within AUTH_FIND_TIME, then AUTH_BAN_TIME), from anywhere: always;
- from one address outside the LAN within an hour, 10 failures or 5
  different names (the second "sieht nach Brute Force aus");
- a ban and the brute force from its address in one run are one message.
An incident stays open until its ban ends or an hour after its address's
last failure; what comes meanwhile is counted, never sent again. Failures
below every threshold (a typo on the phone in the LAN) are one journal
note per run (watch), no message.

The other login doors (#703): Vaultwarden, Samba and Home Assistant log
their failed logins to their units' journal; anmeldungen.sh exports each
after its cursor into STATE_DIR/journal/<door>.json (journalctl -o json),
and this reads them (DOORS below) and writes the cursor back. A door has no
ban of its own (Home Assistant's, when it bans, is a message): one address
with 10 failures or 5 names within an hour is an incident, from outside;
Samba's from the LAN too, the share has no door to the outside.

Out: STATE_DIR/meldungen.jsonl, bote-notify's payloads {"an","text",
"betreff","at"} for NOTIFY_PERSON (with SMTP_HOST also without one: the
mail fallback's subject is betreff), and STATE_DIR/notizen.tsv, "<kind>\\t<text>" per journal
note (incident for each message, watch for the rest). anmeldungen.sh sends
both from the host. LAN_NETZE: the box's own networks, comma-separated
CIDRs (anmeldungen.sh reads them with ip addr). ANMELDUNGEN_JETZT: a fixed
clock for the tests."""
import datetime, ipaddress, json, os, re, shutil, sqlite3, sys, tempfile, time

E = os.environ
STATE = E.get("STATE_DIR", "/state")
NOW = int(E.get("ANMELDUNGEN_JETZT") or time.time())
HOUR = 3600
IP_FAILS, IP_NAMES = 10, 5   # #653: from outside, within an hour


def seconds(v, name):
    m = re.fullmatch(r"(\d+)([smhd]?)", (v or "").strip())
    if not m:
        sys.exit(f"{name}={v!r}: no duration (like 10m)")
    return int(m[1]) * {"": 1, "s": 1, "m": 60, "h": 3600, "d": 86400}[m[2]]


MAX = int(E.get("AUTH_MAX_RETRIES") or 5)
FIND = seconds(E.get("AUTH_FIND_TIME") or "10m", "AUTH_FIND_TIME")
BAN = seconds(E.get("AUTH_BAN_TIME") or "30m", "AUTH_BAN_TIME")
KEEP = max(HOUR, FIND) + 300   # how far back the windows look


def nets():
    out = []
    for c in (E.get("LAN_NETZE") or "").split(","):
        try:
            out.append(ipaddress.ip_network(c.strip(), strict=False))
        except ValueError:
            pass
    return out


LAN = nets()


def in_lan(ip):
    try:
        a = ipaddress.ip_address(ip)
    except ValueError:
        return False   # no address: counted as outside, so it is not hushed
    if a.version == 6 and a.ipv4_mapped:
        a = a.ipv4_mapped
    return a.is_loopback or a.is_link_local or any(a in n for n in LAN if n.version == a.version)


def epoch(t):
    """Authelia's time column as its sqlite driver writes it (2026-10-25
    18:10:00.123+00:00), or an epoch"""
    if isinstance(t, (int, float)):
        return int(t)
    m = re.match(r"(\d{4}-\d\d-\d\d)[ T](\d\d:\d\d:\d\d)(?:\.\d+)?\s*(Z|[+-]\d\d:?\d\d)?", str(t))
    if not m:
        return None
    tz = (m[3] or "+00:00").replace("Z", "+00:00")
    if ":" not in tz:
        tz = tz[:3] + ":" + tz[3:]
    return int(datetime.datetime.fromisoformat(f"{m[1]}T{m[2]}{tz}").timestamp())


def copy_db(path, into):
    """a copy of the database and its WAL, opened from there: the original
    is never opened, and a read-only mount needs no -shm"""
    if not os.path.exists(path):
        return None
    dst = os.path.join(into, os.path.basename(path))
    for ext in ("", "-wal"):
        if os.path.exists(path + ext):
            shutil.copyfile(path + ext, dst + ext)
    return sqlite3.connect(dst)


def clean(s, n=40):
    """a name someone typed: one printable line, short"""
    s = "".join(c if c.isprintable() and c not in "\t\r\n" else "?" for c in str(s or ""))
    return s if len(s) <= n else s[: n - 1] + "…"


def load_state():
    try:
        with open(os.path.join(STATE, "stand.json")) as f:
            return json.load(f)
    except (OSError, ValueError):
        return {}


def users(tmp):
    """LLDAP's names and mail addresses, lower case; None when unreadable"""
    try:
        db = copy_db(E.get("LLDAP_DB", "/lldap/users.db"), tmp)
        if db is None:
            return None
        cur = db.execute("SELECT * FROM users")
        cols = [c[0] for c in cur.description]
        out = set()
        for row in cur:
            for c in ("user_id", "email", "lowercase_email"):
                if c in cols and row[cols.index(c)]:
                    out.add(str(row[cols.index(c)]).lower())
        return out
    except sqlite3.Error:
        return None


# the other doors (#703): (title in a message, its journal's regexes → (user, ip, banned), advice, counts in the LAN)
VW_ADVICE = ("Warst du das? Ein Gerät mit einem alten Master-Passwort versucht es auch immer wieder. Sonst nichts tun: "
             "Vaultwarden bremst wiederholte Anmeldungen, sperrt aber niemanden. Bei Häufung: sag es dem Operator.")
SMB_ADVICE = ("Warst du das? Oft ist es ein Gerät, das sich noch mit einem alten Passwort am Netzlaufwerk anmeldet: "
              "dort das Passwort neu eingeben. Sonst versucht ein Gerät im Heimnetz Passwörter: sag es dem Operator.")
HA_ADVICE = ("Warst du das? Eine App mit abgelaufener Anmeldung versucht es auch immer wieder. Home Assistant sperrt eine "
             "Adresse nur, wenn HA_LOGIN_SPERRE gesetzt ist (services/home-assistant/values.yml). Bei Häufung: sag es dem "
             "Operator, er kann das vorschlagen.")
HA_BAN_ADVICE = ("Warst du das? Dann die Adresse aus ip_bans.yaml im Ordner von Home Assistant löschen und Home Assistant "
                 "neu starten, der Operator kann das vorschlagen. Sonst nichts tun, die Sperre greift.")
DOORS = {
    "vaultwarden": ("Vaultwarden (Passwörter)", [
        (r"(?:Username or password|Username or access code) is incorrect\. Try again\. IP: ([^\s]+?)\. Username: (.*)\.\s*$", lambda m: (m[2], m[1], False)),
        (r"Invalid admin token\. IP: ([^\s]+?)\.?\s*$", lambda m: ("Admin-Seite", m[1], False))], VW_ADVICE, False),
    "samba": ("Netzlaufwerk (Samba)", [
        (r"Auth: \[[^\]]*\] user \[[^\]]*\]\\\[(.*?)\] at \[.*?\] with \[.*?\] status \[(NT_STATUS_\w+)\] workstation \[.*?\] remote host \[ipv[46]:([^\]]*):\d+\]",
         lambda m: None if m[2] == "NT_STATUS_OK" or not m[1] else (m[1], m[3], False))], SMB_ADVICE, True),
    "home-assistant": ("Home Assistant", [
        (r"invalid authentication from .* \(([0-9A-Fa-f:.]+)\)\.", lambda m: ("", m[1], False)),
        (r"Banned IP (\S+) for too many login attempts", lambda m: ("", m[1], True))], HA_ADVICE, False),
}


def door_rows(st):
    """the doors' new failures from STATE_DIR/journal/<door>.json; the cursors to write back"""
    out, cursors = [], {}
    for door, (_, pats, _, _) in DOORS.items():
        path = os.path.join(STATE, "journal", door + ".json")
        try:
            lines = open(path, encoding="utf-8", errors="replace").read().splitlines()
        except OSError:
            continue
        for line in lines:
            try:
                e = json.loads(line)
            except ValueError:
                continue
            if not isinstance(e, dict) or "__CURSOR" not in e:
                continue
            cursors[door] = e["__CURSOR"]
            msg = e.get("MESSAGE") or ""
            if isinstance(msg, list):   # journald's bytes, when not UTF-8
                msg = bytes(b & 0xFF for b in msg if isinstance(b, int)).decode(errors="replace")
            t = int(int(e.get("__REALTIME_TIMESTAMP") or 0) / 1_000_000)
            if t < NOW - KEEP:
                continue   # a first export reads the last hour; never what is older than the windows
            for rx, row in pats:
                m = re.search(rx, str(msg))
                r = row(m) if m else None
                if r:
                    seq = re.search(r"\bi=([0-9a-f]+)", e["__CURSOR"])
                    out.append({"id": f"{door}:{seq[1] if seq else e.get('__REALTIME_TIMESTAMP')}", "t": t, "ok": False, "banned": r[2],
                                "user": str(r[0]), "ip": r[1].strip("[]"), "tuer": door})
                    break
    return out, cursors


def tuer(r):
    return r.get("tuer", "authelia")


def read_rows(tmp, st):
    db = copy_db(E.get("AUTH_DB", "/authelia/db.sqlite3"), tmp)
    if db is None:
        return [], st.get("cursor", {})
    cur = st.get("cursor") or {}
    if cur:   # a new database (a restore, a reinstall) has other rows under the same id
        r = db.execute("SELECT time FROM authentication_logs WHERE id = ?", (cur["id"],)).fetchone()
        if not r or str(r[0]) != cur["time"]:
            print("authentication_logs: not the database of the last run, reading it from the start")
            cur, st["recent"], st["offen"] = {}, [], {}
    rows = db.execute("SELECT id, time, successful, banned, username, remote_ip FROM authentication_logs WHERE id > ? ORDER BY id",
                      (cur.get("id", 0),)).fetchall()
    first = not cur
    out = []
    for i, t, ok, banned, user, ip in rows:
        cur = {"id": i, "time": str(t)}
        e = epoch(t)
        if e is None or (first and e < NOW - KEEP):
            continue   # the first run starts with the last hour, not with every old failure
        out.append({"id": i, "t": e, "ok": bool(ok), "banned": bool(banned), "user": str(user or ""), "ip": str(ip or "")})
    return out, cur


def bans(recent):
    """Authelia's bans in the window, per name: (name, until, ids). A ban
    starts at the failure that makes the name's last MAX attempts within
    FIND all failures and lasts BAN from there; what fails meanwhile is
    part of it. An attempt Authelia logged as banned starts one too, when
    the rule here did not see it (other values, an older window)"""
    out, by_user = [], {}
    for r in recent:
        by_user.setdefault(r["user"].lower(), []).append(r)
    for u, att in sorted(by_user.items()):
        until = None
        for r in att:
            if r["ok"] or (until and r["t"] <= until):
                continue
            last = [a for a in att if a["id"] <= r["id"] and a["t"] > r["t"] - FIND][-MAX:]
            if (len(last) == MAX and not any(a["ok"] for a in last)) or r["banned"]:
                until = r["t"] + BAN
                ids = {a["id"] for a in att if not a["ok"] and r["t"] - FIND < a["t"] <= until}
                out.append((u, until, ids))
    return out


def dauer(s):
    if s % 3600 == 0:
        h = s // 3600
        return f"{h} Stunde" if h == 1 else f"{h} Stunden"
    m = max(1, round(s / 60))
    return "1 Minute" if m == 1 else f"{m} Minuten"


def hhmm(t):
    return time.strftime("%H:%M", time.localtime(t))


def woher(ips):
    ips = sorted(set(ips))
    words = [f"{clean(ip or 'unbekannt')} ({'aus dem Heimnetz' if in_lan(ip) else 'von außen'})" for ip in ips[:3]]
    return ", ".join(words) + (f" und {len(ips) - 3} weitere" if len(ips) > 3 else "")


def names(ns):
    ns = list(dict.fromkeys(ns))
    return ", ".join(f"„{clean(n)}“" for n in ns[:5]) + (f" und {len(ns) - 5} weitere" if len(ns) > 5 else "")


def unknown_words(ns, known):
    if known is None:
        return ""
    unk = [n for n in dict.fromkeys(ns) if n.lower() not in known]
    if not unk:
        return "Den Benutzernamen gibt es." if len(set(ns)) == 1 else "Die Benutzernamen gibt es alle."
    if len(set(ns)) == 1:
        return "Den Benutzernamen gibt es nicht, das ist typisch für Brute Force."
    return f"{len(unk)} davon gibt es nicht, das ist typisch für Brute Force."


HINT = ("Bei Häufung: sag es dem Operator, er kann eine längere Sperre vorschlagen "
        "(AUTH_BAN_TIME in services/auth/values.yml).")


def main():
    os.makedirs(STATE, exist_ok=True)
    st = load_state()
    with tempfile.TemporaryDirectory() as tmp:
        known = users(tmp)
        new, cursor = read_rows(tmp, st)
    more, cursors = door_rows(st)
    recent = [r for r in st.get("recent", []) if r["t"] >= NOW - KEEP]
    seen = {r["id"] for r in recent}
    new += [r for r in more if r["id"] not in seen]
    recent += new
    offen = {k: v for k, v in st.get("offen", {}).items() if v["bis"] >= NOW}
    fails = [r for r in recent if not r["ok"] and tuer(r) == "authelia"]
    covered, messages, notes = set(), [], []

    # 1. bans, from anywhere
    new_ids = {r["id"] for r in new}
    found = []
    for u, until, ids in bans([r for r in recent if tuer(r) == "authelia"]):
        if not ids & new_ids:
            continue
        covered |= ids & new_ids
        key = "sperre:" + u
        if key in offen:
            offen[key]["bis"] = max(offen[key]["bis"], until)
            continue
        offen[key] = {"bis": until}
        found.append((u, until, ids))
    # 2. one address outside the LAN, within the hour
    hits = {}
    for ip in sorted({r["ip"] for r in new if not r["ok"] and tuer(r) == "authelia" and not in_lan(r["ip"])}):
        mine = [r for r in fails if r["ip"] == ip and r["t"] > NOW - HOUR]
        ns = [r["user"] for r in mine]
        if len(mine) < IP_FAILS and len({n.lower() for n in ns}) < IP_NAMES:
            continue
        key = "ip:" + ip
        covered |= {r["id"] for r in new if r["ip"] == ip and tuer(r) == "authelia"}
        last = max(r["t"] for r in mine)
        if key in offen:
            offen[key]["bis"] = max(offen[key]["bis"], last + HOUR)
            continue
        offen[key] = {"bis": last + HOUR}
        hits[ip] = mine
    # the messages: a ban takes the brute force from its address along
    for u, until, ids in found:
        rows = [r for r in fails if r["id"] in ids]
        ips = [r["ip"] for r in rows]
        span = max(r["t"] for r in rows) - min(r["t"] for r in rows)
        text = (f"🔒 Anmeldung gesperrt: {len(rows)} Fehlversuche für „{clean(rows[-1]['user'])}“ in {dauer(max(span, 60))}, "
                f"die Anmeldung ist bis {hhmm(until)} gesperrt.\nWoher: {woher(ips)}.")
        w = unknown_words([rows[-1]["user"]], known)
        text += "\n" + w if w else ""
        for ip in sorted(set(ips) & set(hits)):
            mine = hits.pop(ip)
            n_names = len({r["user"].lower() for r in mine})
            if n_names >= IP_NAMES:
                text += (f"\nVon {clean(ip)} kamen in der letzten Stunde {len(mine)} Fehlversuche für "
                         f"{n_names} Benutzernamen. Sieht nach Brute Force aus.")
            elif len(mine) > len(rows):
                text += f"\nVon {clean(ip)} kamen in der letzten Stunde {len(mine)} Fehlversuche."
        messages.append((text, "Warst du das? Sonst nichts tun, die Sperre greift. " + HINT, f"Anmeldung gesperrt: {clean(rows[-1]['user'])} ({hhmm(NOW)})"))
    for ip, mine in hits.items():
        ns = [r["user"] for r in mine]
        n_names = len({n.lower() for n in ns})
        text = (f"⚠️ {len(mine)} Fehlversuche von {woher([ip])} in der letzten Stunde, für "
                + (f"{n_names} Benutzernamen: " if n_names > 1 else "") + names(ns) + ".")
        if n_names >= IP_NAMES:
            text += " Sieht nach Brute Force aus."
        w = unknown_words(ns, known)
        text += " " + w if w else ""
        messages.append((text, f"Warst du das? Sonst nichts tun: Authelia sperrt jeden Namen nach {MAX} Fehlversuchen "
                         f"für {dauer(BAN)}. " + HINT, f"Fehlversuche von {clean(ip)} ({hhmm(NOW)})"))
    # 4. the other doors (#703): one address within the hour, or the door's own ban
    for door, (title, _, advice, lan_too) in DOORS.items():
        mine_new = [r for r in new if tuer(r) == door]
        for ip in sorted({r["ip"] for r in mine_new if lan_too or not in_lan(r["ip"])}):
            mine = [r for r in recent if tuer(r) == door and r["ip"] == ip and r["t"] > NOW - HOUR]
            fl = [r for r in mine if not r["banned"]]
            ns = [r["user"] for r in fl if r["user"]]
            n_names = len({n.lower() for n in ns})
            banned = any(r["banned"] for r in mine_new if r["ip"] == ip)
            if not banned and len(fl) < IP_FAILS and n_names < IP_NAMES:
                continue
            covered |= {r["id"] for r in mine_new if r["ip"] == ip}
            key = f"{door}:{'sperre' if banned else 'ip'}:{ip}"
            last = max(r["t"] for r in mine)
            if key in offen or not banned and f"{door}:sperre:{ip}" in offen:
                offen.setdefault(key, {"bis": 0})["bis"] = max(offen[key]["bis"], last + HOUR)
                continue
            offen[key] = {"bis": last + (86400 if banned else HOUR)}   # a ban of Home Assistant stays until someone lifts it
            who = (f" für {n_names} Benutzernamen: " if n_names > 1 else " für " if ns else "") + (names(ns) if ns else "")
            if banned:
                text = (f"🔒 {title} hat {clean(ip)} gesperrt: zu viele Fehlversuche bei der Anmeldung ({len(fl)} Fehlversuche{who} "
                        f"in der letzten Stunde).\nWoher: {woher([ip])}.")
                advice_now, betreff = HA_BAN_ADVICE, f"{title} sperrt {clean(ip)} ({hhmm(NOW)})"
            else:
                text = f"⚠️ {title}: {len(fl)} Fehlversuche{who} in der letzten Stunde.\nWoher: {woher([ip])}."
                advice_now, betreff = advice, f"{title}: Fehlversuche von {clean(ip)} ({hhmm(NOW)})"
            if n_names >= IP_NAMES:
                text += "\nSieht nach Brute Force aus."
            messages.append((text, advice_now, betreff))
    # the journal: what happened, without the advice (Part 6)
    to = (f"gemeldet an {E['NOTIFY_PERSON']}" if E.get("NOTIFY_PERSON") else
          "box.yml notify.person fehlt, per Mail gemeldet" if E.get("SMTP_HOST") else "niemandem gemeldet, box.yml notify.person fehlt")
    for text, _, _ in messages:
        notes.append(("incident", "Anmeldungen: " + text.replace("\n", " · ") + f" ({to})"))
    messages = [(text + "\n" + advice, betreff) for text, advice, betreff in messages]
    # 3. below every threshold: one line in the journal, no message
    rest = [r for r in new if not r["ok"] and r["id"] not in covered]
    if rest:
        parts = []
        for door in ["authelia"] + list(DOORS):
            mine = [r for r in rest if tuer(r) == door]
            lan = [r for r in mine if in_lan(r["ip"])]
            out = [r for r in mine if not in_lan(r["ip"])]
            parts += [("" if door == "authelia" else DOORS[door][0] + ": ") +
                      f"{len(g)} {'Fehlversuch' if len(g) == 1 else 'Fehlversuche'} {where} ("
                      + (names([r['user'] for r in g if r['user']]) + " " if any(r["user"] for r in g) else "")
                      + f"von {', '.join(sorted({clean(r['ip'] or '?') for r in g}))})"
                      for g, where in ((lan, "aus dem Heimnetz"), (out, "von außen")) if g]
        notes.append(("watch", "Anmeldungen: " + "; ".join(parts) + ", unter jeder Schwelle, keine Mitteilung."))

    if (E.get("NOTIFY_PERSON") or E.get("SMTP_HOST")) and messages:   # without a person: for the mail (#703)
        with open(os.path.join(STATE, "meldungen.jsonl"), "a") as f:
            for text, betreff in messages:
                f.write(json.dumps({"an": E.get("NOTIFY_PERSON", ""), "text": text, "betreff": betreff, "at": NOW}) + "\n")
    if notes:
        with open(os.path.join(STATE, "notizen.tsv"), "a") as f:
            for kind, text in notes:
                f.write(kind + "\t" + text.replace("\t", " ").replace("\n", " ") + "\n")
    st = {"cursor": cursor, "recent": recent, "offen": offen}
    tmp = os.path.join(STATE, "stand.json.tmp")
    with open(tmp, "w") as f:
        json.dump(st, f)
    os.replace(tmp, os.path.join(STATE, "stand.json"))
    for door, c in cursors.items():   # after the state: a run that died reads the export again, its ids are known
        with open(os.path.join(STATE, "journal", door + ".cursor"), "w") as f:
            f.write(c + "\n")
    print(f"anmeldungen: {len(new)} neue Einträge, {len(messages)} Mitteilungen, {len(notes)} Notizen")


main()
