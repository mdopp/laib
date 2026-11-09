"""The mail receipt (owner 2026-11-09): whoever files a document by mail
hears whether it arrived, in one message per mail.

paperless-quittung.sh runs it every five minutes inside the running
Paperless container (`podman exec -i --user paperless … python3 - quittung`,
the script on stdin) and appends what it prints to the messages file the
same script then sends to bote-notify from the host. Stdout is only those
lines, bote-notify's payload {"an", "text", "at"}; everything else goes to
stderr (the unit's journal).

What says a document came by mail: Paperless' own record of a fetched mail
(/api/processed_mail/, one per mail the rule took, never one for a scan, the
consume folder or an upload), of a rule on the account the values name. Its
documents are the consume tasks (/api/tasks/, API version 9 as the check
reads them) whose file name is one of the mail's attachments, made before
Paperless recorded the mail; each task counts for one mail only. Paperless
keeps neither the sender nor the attachments' names, so both come from the
mailbox itself, by the record's folder and IMAP uid, read-only (BODY.PEEK:
no flag changes).

A task with a document: „abgelegt“, its title, pages and link. A duplicate
(Paperless 2.x refuses it, 3.x with CONSUMER_DELETE_DUPLICATES too):
„schon vorhanden“. Any other failure: „nicht lesbar“. A mail whose tasks
still run waits, at most WARTEN; one Paperless recorded without any task
(no attachment it takes) is said to a household sender, never to a
stranger's (a newsletter, spam). Never the document's content: the
title, the file name and the page count only.

The recipient: the household person whose Paperless account has the
sender's address (the address Authelia's login brought, LLDAP's), as their
user name, which is their name in box.yml people; else NOTIFY_PERSON.
Nobody to tell: no line. The first run only notes where Paperless' record
stands, so the mail before it is never told.

State: QUITTUNG_STAND (in Paperless' data folder): {"bis", "offen", "aufgaben"}.
Standard library only.
"""
import base64, email, email.policy, imaplib, json, os, re, ssl, sys, time, unicodedata, urllib.error, urllib.request
from datetime import datetime
from email.utils import getaddresses

E = os.environ
WARTEN = 2 * 3600    # seconds a mail waits for its tasks, or for the mailbox
FENSTER = 6 * 3600   # a task made this long before Paperless recorded the mail can be one of its attachments
SECURITY = {"ssl", "starttls", "none"}


def log(*a):
    print("Mail-Quittung:", *a, file=sys.stderr)


class API:
    def __init__(self, base, user, password):
        self.base = base.rstrip("/")
        self.auth = "Basic " + base64.b64encode((user + ":" + password).encode()).decode()

    def __call__(self, path, accept="application/json"):
        r = urllib.request.Request(self.base + path, headers={"Authorization": self.auth, "Accept": accept})
        with urllib.request.urlopen(r, timeout=30) as resp:
            data = resp.read()
            return json.loads(data) if data else None

    def all(self, path):
        out, page = [], path + ("&" if "?" in path else "?") + "page_size=100"
        while page:
            res = self(page)
            out += res.get("results", [])
            page = res.get("next") and res["next"][res["next"].index("/api/"):]
        return out


def zeit(s):
    """an ISO time of Paperless' as epoch seconds; 0 when there is none"""
    try:
        return datetime.fromisoformat((s or "").replace("Z", "+00:00")).timestamp()
    except ValueError:
        return 0


def name(s):
    """a file name as Paperless keeps it and as the mail has it, compared: NFC, without what
    Paperless' sanitising drops or a mail client changes (blanks, underscores, slashes, quotes), folded"""
    return re.sub(r"[^\w.]+|_", "", unicodedata.normalize("NFC", s or "")).casefold()


def regeln(api):
    """the ids of the rules on the account the values name"""
    host, user = E.get("PAPERLESS_MAIL_HOST", "").strip().lower(), E.get("PAPERLESS_MAIL_USER", "").strip().lower()
    konten = {a["id"] for a in api.all("/api/mail_accounts/")
              if (a.get("imap_server") or "").lower() == host and (a.get("username") or "").lower() == user}
    return {r["id"] for r in api.all("/api/mail_rules/") if r.get("account") in konten}


class Postfach:
    """the mailbox, read-only: a mail's sender and its attachments' names by folder and uid"""

    def __init__(self):
        self.imap, self.ordner = None, None

    def __enter__(self):
        return self

    def __exit__(self, *a):
        if self.imap is not None:
            try:
                self.imap.logout()
            except (OSError, imaplib.IMAP4.error):
                pass

    def verbinden(self):
        host, sec = E["PAPERLESS_MAIL_HOST"].strip(), (E.get("PAPERLESS_MAIL_SECURITY", "ssl").strip().lower() or "ssl")
        port = int(E.get("PAPERLESS_MAIL_PORT", "").strip() or (993 if sec == "ssl" else 143))
        if sec not in SECURITY:
            raise OSError("PAPERLESS_MAIL_SECURITY %r" % sec)
        if sec == "ssl":
            imap = imaplib.IMAP4_SSL(host, port, ssl_context=ssl.create_default_context(), timeout=30)
        else:
            imap = imaplib.IMAP4(host, port, timeout=30)
            if sec == "starttls":
                imap.starttls(ssl.create_default_context())
        imap.login(E.get("PAPERLESS_MAIL_USER", "").strip(), E.get("PAPERLESS_MAIL_PASSWORD", ""))
        self.imap = imap

    def mail(self, ordner, uid):
        """(the sender's address, [attachment names]), or None when the mailbox does not answer"""
        try:
            if self.imap is None:
                self.verbinden()
            if self.ordner != ordner:
                self.imap.select('"%s"' % ordner.replace('"', '\\"'), readonly=True)
                self.ordner = ordner
            typ, data = self.imap.uid("FETCH", str(uid), "(BODY.PEEK[])")
        except (OSError, imaplib.IMAP4.error) as e:
            log("das Postfach antwortet nicht:", e)
            return None
        roh = next((d[1] for d in data or [] if isinstance(d, tuple)), None)
        if typ != "OK" or roh is None:
            log("Mail %s in %s nicht im Postfach" % (uid, ordner))
            return None
        msg = email.message_from_bytes(roh, policy=email.policy.default)
        von = next((a for _, a in getaddresses([str(msg.get("From", ""))]) if "@" in a), "")
        return von, [p.get_filename() for p in msg.walk() if p.get_filename()]


def seiten(n):
    return "" if not n else ", 1 Seite" if n == 1 else ", %d Seiten" % n


def ergebnis(api, t, base):
    """one task as ("ok", title, pages, link) or ("nein", file, reason); None while it runs"""
    status, result = t.get("status"), (t.get("result") or "")
    if status not in ("SUCCESS", "FAILURE", "REVOKED"):
        return None
    if "duplicate" in result.lower():
        return ("nein", t.get("task_file_name") or "?", "schon vorhanden")
    doc = t.get("related_document")
    if status == "SUCCESS" and doc:
        try:
            d = api("/api/documents/%s/?fields=id,title,page_count" % doc)
        except (OSError, ValueError) as e:   # gone or not readable: the file name says which
            log("Dokument %s nicht lesbar: %s" % (doc, e))
            d = {}
        return ("ok", d.get("title") or t.get("task_file_name") or "?", d.get("page_count"),
                "%s/documents/%s/details" % (base, doc))
    return ("nein", t.get("task_file_name") or "?", "nicht lesbar")


def text(von, teile, betreff):
    v = " (von %s)" % von if von else ""
    ok, nein = [p for p in teile if p[0] == "ok"], [p for p in teile if p[0] == "nein"]
    if not teile:
        return "Per Mail nicht abgelegt: %s%s: kein Anhang, den Paperless ablegen kann" % (betreff or "(ohne Betreff)", v)
    if len(ok) == 1 and not nein:
        return "Per Mail abgelegt: %s%s%s – %s" % (ok[0][1], v, seiten(ok[0][2]), ok[0][3])
    if len(nein) == 1 and not ok:
        return "Per Mail nicht abgelegt: %s%s: %s" % (nein[0][1], v, nein[0][2])
    zeilen = ["abgelegt: %s%s – %s" % (p[1], seiten(p[2]), p[3]) for p in ok]
    zeilen += ["nicht abgelegt: %s: %s" % (p[1], p[2]) for p in nein]
    return "Per Mail%s:\n%s" % (v, "\n".join(zeilen))


def empfaenger(api, von):
    """(the person whose Paperless account has the sender's address, True), else (NOTIFY_PERSON, False)"""
    if von:
        try:
            for u in api.all("/api/users/"):
                if (u.get("email") or "").strip().casefold() == von.casefold() and u.get("username"):
                    return u["username"], True
        except (OSError, ValueError) as e:
            log("Paperless' Konten nicht lesbar:", e)
    return E.get("NOTIFY_PERSON", "").strip(), False


def zuordnen(m, namen, aufgaben, belegt):
    """the consume tasks that are this mail's attachments: by name, made before Paperless
    recorded the mail, the newest each, each task once"""
    bis = zeit(m.get("processed"))
    out = []
    for n in dict.fromkeys(name(x) for x in namen):
        passt = [t for t in aufgaben if t.get("id") not in belegt and name(t.get("task_file_name")) == n
                 and bis - FENSTER <= zeit(t.get("date_created")) <= bis + 60]
        if passt:
            t = max(passt, key=lambda t: zeit(t.get("date_created")))
            belegt.add(t.get("id"))
            out.append(t)
    return out


def quittung(api, stand, jetzt, postfach):
    """the lines for the mails Paperless recorded since the last run; changes stand"""
    meine = regeln(api)
    mails = [m for m in api.all("/api/processed_mail/") if m.get("rule") in meine]
    if stand.get("bis") is None:   # the first run: from here on, nothing before
        stand["bis"] = max((m["id"] for m in mails), default=0)
        log("beginnt bei Mail %d" % stand["bis"])
        return []
    offen = stand.setdefault("offen", {})
    neu = [m for m in mails if m["id"] > stand["bis"] or str(m["id"]) in offen]
    if not neu:
        return []
    stand["bis"] = max([stand["bis"]] + [m["id"] for m in neu])
    aufgaben = api("/api/tasks/?task_name=consume_file", accept="application/json; version=9") or []
    if isinstance(aufgaben, dict):
        aufgaben = aufgaben.get("results", [])
    belegt = set(stand.get("aufgaben") or [])
    base = E.get("PAPERLESS_URL", "").rstrip("/")
    zeilen = []
    for m in sorted(neu, key=lambda m: m["id"]):
        seit = offen.setdefault(str(m["id"]), jetzt)
        lange = jetzt - seit >= WARTEN
        gelesen = postfach.mail(m.get("folder") or "INBOX", m.get("uid"))
        if gelesen is None and not lange:
            continue   # the mailbox once more on the next run
        von, namen = gelesen or ("", [])
        probe = set(belegt)
        teile = [ergebnis(api, t, base) for t in zuordnen(m, namen, aufgaben, probe)]
        if None in teile and not lange:
            continue   # Paperless still files one
        teile = [p for p in teile if p is not None]
        belegt = probe
        offen.pop(str(m["id"]), None)
        an, person = empfaenger(api, von)
        if not teile and m.get("status") == "FAILED":   # no task to read: what Paperless recorded
            teile = [("nein", m.get("subject") or "?", "nicht lesbar")]
        if not teile and not person:   # no attachment Paperless takes, from someone else (a newsletter, spam): not told
            log("Mail %s: nichts abgelegt, nicht von jemandem aus dem Haushalt, keine Mitteilung" % m["id"])
            continue
        if not an:
            log("Mail %s: niemand zu benachrichtigen (box.yml notify.person fehlt)" % m["id"])
            continue
        zeilen.append({"an": an, "text": text(von, teile, m.get("subject")), "at": int(jetzt)})
    stand["aufgaben"] = sorted(belegt)[-500:]
    return zeilen


def laden(pfad):
    try:
        with open(pfad, encoding="utf-8") as f:
            return json.load(f)
    except (OSError, ValueError):
        return {}


def sichern(pfad, stand):
    tmp = pfad + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(stand, f)
    os.replace(tmp, pfad)


if __name__ == "__main__":
    if not E.get("PAPERLESS_MAIL_HOST", "").strip():
        sys.exit(0)   # no mail inbox, nothing to tell
    api = API(E.get("PAPERLESS_API", "http://127.0.0.1:8000"), E.get("PAPERLESS_ADMIN_USER", "admin"), E.get("PAPERLESS_ADMIN_PASSWORD", ""))
    pfad = E.get("QUITTUNG_STAND", "/usr/src/paperless/data/laib-mailquittung.json")
    stand = laden(pfad)
    try:
        with Postfach() as postfach:
            zeilen = quittung(api, stand, time.time(), postfach)
    except (OSError, ValueError) as e:   # Paperless restarts: the next run
        log("Paperless antwortet nicht:", e)
        sys.exit(0)
    for z in zeilen:   # the lines first: a run that dies before sichern tells twice, never not at all
        print(json.dumps(z, ensure_ascii=False))
    sys.stdout.flush()
    sichern(pfad, stand)
