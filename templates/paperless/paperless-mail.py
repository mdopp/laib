"""Paperless' mail inbox from values, and its state as laib's status (#521).

Paperless fetches mail itself (paperless_mail): an IMAP account, a rule
which mails it takes, every ten minutes. Both live in Paperless' database,
so they come from this template's values through Paperless' API, never
from a script by hand:

  setup  post-deploy, on every apply: makes or updates the account and the
         rule from PAPERLESS_MAIL_* (the password from the secret
         PAPERLESS_MAIL_PASSWORD), then checks. An empty PAPERLESS_MAIL_HOST
         changes nothing in Paperless: an account made by hand stays.
  check  paperless-check.service, inside the Paperless container
         (`podman exec -i … python3 - check`): laib starts it when someone
         looks at diag or the Box page and the status is older than its
         stale, the timer once an hour.

The account and the rule are found by what they are, not by a name: the
account by server and user, the rule by account and recipient, so the
ones the owner made by hand (paperless-mail.sh, 2026-10-21) are taken
over, not doubled. The rule takes attachments only, titles a document by
the mail's subject, gives it no owner (so the family sees it, as a file
from the consume folder) and flags the mail afterwards; Paperless skips
flagged mail, so nothing is filed twice.

The check is Paperless' own connection test (POST /api/mail_accounts/test/
with the account's id: Paperless logs in with the stored password). What it
cannot see: a mail Paperless fetched but could not file (that is in
Paperless' log), and a rule that matches nothing (a wrong recipient).

Status file: {"at", "detail", "findings": [{severity, detail, fix}]}.
Standard library only: it runs in the hooks image and in Paperless'.
"""
import base64, json, os, sys, time, urllib.error, urllib.request

E = os.environ
SECURITY = {"none": 1, "ssl": 2, "starttls": 3}
FIX_PASSWORD = "das Passwort des Postfachs: laib secret set PAPERLESS_MAIL_PASSWORD (über stdin), dann laib apply paperless"


def want():
    """the account and the rule the values ask for; None without a host"""
    host = E.get("PAPERLESS_MAIL_HOST", "").strip()
    if not host:
        return None
    sec = E.get("PAPERLESS_MAIL_SECURITY", "ssl").strip().lower() or "ssl"
    if sec not in SECURITY:
        raise SystemExit("paperless: PAPERLESS_MAIL_SECURITY ist %r, erlaubt: ssl, starttls, none" % sec)
    account = {"imap_server": host, "imap_port": int(E.get("PAPERLESS_MAIL_PORT", "").strip() or 993),
               "imap_security": SECURITY[sec], "username": E.get("PAPERLESS_MAIL_USER", "").strip(),
               "character_set": "UTF-8", "account_type": 1, "is_token": False}
    to = E.get("PAPERLESS_MAIL_FILTER_TO", "").strip()
    rule = {"name": "Dokumente an " + (to or account["username"]), "enabled": True,
            "folder": E.get("PAPERLESS_MAIL_FOLDER", "").strip() or "INBOX",
            "filter_to": to or None, "maximum_age": 30,
            "attachment_type": 1,         # attachments only (no inline images)
            "consumption_scope": 1,       # the attachments, never the mail itself
            "action": 4, "action_parameter": None,   # flag the mail: Paperless skips flagged ones
            "assign_title_from": 1,       # the subject is the title
            "assign_correspondent_from": 1,
            "assign_owner_from_rule": False}  # no owner: the family sees it, like a file from the consume folder
    return account, rule


class API:
    def __init__(self, base, user, password):
        self.base = base.rstrip("/")
        self.auth = "Basic " + base64.b64encode((user + ":" + password).encode()).decode()

    def __call__(self, method, path, body=None):
        r = urllib.request.Request(self.base + path, method=method,
                                   data=json.dumps(body).encode() if body is not None else None,
                                   headers={"Authorization": self.auth, "Accept": "application/json",
                                            "Content-Type": "application/json"})
        with urllib.request.urlopen(r, timeout=30) as resp:
            data = resp.read()
            return json.loads(data) if data else None

    def all(self, path):
        out, page = [], path + "?page_size=100"
        while page:
            res = self("GET", page)
            out += res.get("results", [])
            page = res.get("next") and res["next"][res["next"].index("/api/"):]
        return out


def find(api, account, rule):
    acc = next((a for a in api.all("/api/mail_accounts/")
                if a.get("imap_server", "").lower() == account["imap_server"].lower()
                and a.get("username", "").lower() == account["username"].lower()), None)
    rul = None
    if acc:
        rul = next((r for r in api.all("/api/mail_rules/") if r.get("account") == acc["id"]
                    and (r.get("filter_to") or None) == rule["filter_to"]), None)
    return acc, rul


def setup(api, password):
    w = want()
    if w is None:
        print("paperless: kein E-Mail-Eingang (PAPERLESS_MAIL_HOST leer), in Paperless nichts geändert")
        return
    account, rule = w
    if not account["username"] or not password:
        raise SystemExit("paperless: PAPERLESS_MAIL_HOST ist gesetzt, es fehlen PAPERLESS_MAIL_USER oder das Passwort (laib secret set PAPERLESS_MAIL_PASSWORD)")
    acc, rul = find(api, account, rule)
    if acc is None:
        acc = api("POST", "/api/mail_accounts/", {"name": "Dokumente " + account["username"], **account, "password": password})
        print("paperless: Mailkonto angelegt:", account["username"], "@", account["imap_server"])
    else:
        # the password always: Paperless shows it masked, so a changed one cannot be compared
        same = all(acc.get(k) == v for k, v in account.items())
        acc = api("PATCH", "/api/mail_accounts/%d/" % acc["id"], {**account, "password": password})
        print("paperless: Mailkonto", "unverändert" if same else "angepasst", "(Passwort aus dem Secret)")
    body = {**rule, "account": acc["id"]}
    if rul is None:
        api("POST", "/api/mail_rules/", body)
        print("paperless: Regel angelegt:", rule["name"])
    elif all(rul.get(k) == v for k, v in body.items()):
        print("paperless: Regel unverändert:", rule["name"])
    else:
        api("PATCH", "/api/mail_rules/%d/" % rul["id"], body)
        print("paperless: Regel angepasst:", rule["name"])


def check(api):
    """the status: whether Paperless can log in to the mailbox the values name"""
    w = want()
    if w is None:
        return {"detail": "", "findings": []}  # no mail inbox, nothing to say (as before #521)
    account, rule = w
    who = account["username"] + " auf " + account["imap_server"]
    try:
        acc, rul = find(api, account, rule)
    except (OSError, ValueError) as e:
        return {"detail": "E-Mail-Eingang nicht geprüft", "findings": [{"severity": "gelb",
                "detail": "Paperless antwortet nicht auf die Mailkonten: %s" % e, "fix": "laib logs paperless"}]}
    if acc is None or rul is None:
        return {"detail": "E-Mail-Eingang fehlt", "findings": [{"severity": "rot",
                "detail": "Paperless hat %s für %s nicht" % ("das Mailkonto" if acc is None else "die Regel", who),
                "fix": "laib apply paperless (post-deploy legt Konto und Regel an)"}]}
    try:
        api("POST", "/api/mail_accounts/test/", {**{k: acc.get(k) for k in account}, "id": acc["id"],
                                                 "name": acc.get("name", ""), "password": "**********"})
    except urllib.error.HTTPError as e:
        why = e.read().decode(errors="replace").strip()[:200] or e.reason
        return {"detail": "E-Mail-Eingang: Abruf scheitert", "findings": [{"severity": "rot",
                "detail": "Paperless kommt nicht in das Postfach %s: %s" % (who, why), "fix": FIX_PASSWORD}]}
    except OSError as e:
        return {"detail": "E-Mail-Eingang nicht geprüft", "findings": [{"severity": "gelb",
                "detail": "Paperless antwortet nicht auf den Verbindungstest: %s" % e, "fix": "laib logs paperless"}]}
    out = {"detail": "E-Mail-Eingang: %s über %s" % (rule["filter_to"] or account["username"], account["imap_server"]), "findings": []}
    if not rul.get("enabled"):
        out["findings"].append({"severity": "gelb", "detail": "die Regel %r ist in Paperless ausgeschaltet" % rul.get("name"),
                                "fix": "laib apply paperless schaltet sie wieder ein"})
    return out


def write(path, r):
    r["at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(r, f, ensure_ascii=False)
        f.write("\n")
    os.replace(tmp, path)


def up(api):
    for _ in range(60):
        try:
            return api("GET", "/api/mail_accounts/?page_size=1")
        except urllib.error.HTTPError as e:
            if e.code in (401, 403):
                raise SystemExit("paperless: die API lehnt den Admin ab (%d): PAPERLESS_ADMIN_PASSWORD passt nicht mehr" % e.code)
            time.sleep(5)
        except OSError:
            time.sleep(5)
    raise SystemExit("paperless: die API antwortet nicht")


if __name__ == "__main__":
    mode = sys.argv[1] if len(sys.argv) > 1 else "check"
    if mode == "setup":   # post-deploy, in the hooks image on the host network
        api = API(E.get("PAPERLESS_API", "http://127.0.0.1:8000"), E.get("ADMIN_USER", "admin"), E["LAIB_SECRET_PAPERLESS_ADMIN_PASSWORD"])
        status = os.path.join(E["LAIB_DATA_DIR"], "paperless", "data", "laib-status.json")
        up(api)
        setup(api, E.get("LAIB_SECRET_PAPERLESS_MAIL_PASSWORD", ""))
    else:                 # inside the Paperless container: its own admin from its environment
        api = API(E.get("PAPERLESS_API", "http://127.0.0.1:8000"), E.get("PAPERLESS_ADMIN_USER", "admin"), E.get("PAPERLESS_ADMIN_PASSWORD", ""))
        status = E.get("LAIB_STATUS_FILE", "/usr/src/paperless/data/laib-status.json")
    r = check(api)
    write(status, r)
    for f in r["findings"]:
        print("%s: %s" % (f["severity"], f["detail"]))
