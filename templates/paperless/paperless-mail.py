"""Paperless' mail inbox from values, and its state as laib's status (#521).

Paperless fetches mail itself (paperless_mail): an IMAP account, a rule
which mails it takes, every ten minutes. Both live in Paperless' database,
so they come from this template's values through Paperless' API, never
from a script by hand:

  setup  post-deploy, on every apply: makes the household's groups admins
         and family with their permissions (GROUPS), makes or updates the
         account and the rule from PAPERLESS_MAIL_* (the password from the
         secret PAPERLESS_MAIL_PASSWORD), then checks; clears the AI and OCR
         fields of Paperless' own settings that the values set, and makes
         the contract fields (#567); makes Paperless' side of Dokumente
         (ADR 0028, DOKUMENTE): its user, its token, the workflow that
         tells it of a new document. An empty
         PAPERLESS_MAIL_HOST changes nothing of Paperless' mail: an account
         made by hand stays.
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
with the account's id: Paperless logs in with the stored password), and
the consume tasks Paperless lists as failed and nobody dismissed (a mail's
attachment, a file from the consume folder or an upload not filed): yellow,
with the file and Paperless' reason. What it cannot see: a rule that
matches nothing (a wrong recipient), and a failure someone dismissed.

setup ends by asking Paperless to fetch now (POST /api/mail_accounts/<id>/
process/), in its worker. Never fetch by hand with `python3 manage.py
mail_fetcher` under a root `podman exec`: the attachment lands in a 0700
dir of root's that the worker (uid 1000) cannot read, and the consume fails
with Permission denied (2026-10-22). The image's own `mail_fetcher` wrapper
drops to the paperless user; laib's own exec runs `--user paperless`.

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

    def __call__(self, method, path, body=None, accept="application/json"):
        r = urllib.request.Request(self.base + path, method=method,
                                   data=json.dumps(body).encode() if body is not None else None,
                                   headers={"Authorization": self.auth, "Accept": accept,
                                            "Content-Type": "application/json"})
        with urllib.request.urlopen(r, timeout=30) as resp:
            data = resp.read()
            return json.loads(data) if data else None

    def all(self, path):
        out, page = [], path + ("&" if "?" in path else "?") + "page_size=100"
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
    # fetch now, in Paperless' worker (uid 1000): the reason not to run mail_fetcher by hand,
    # which under a root podman exec leaves attachments the worker cannot read (2026-10-22)
    api("POST", "/api/mail_accounts/%d/process/" % acc["id"], {})
    print("paperless: Abruf angestoßen, Paperless holt die Mails jetzt")


# The household's groups (templates/auth seeds them in LLDAP; Authelia sends them as the groups
# claim): Paperless syncs a login's groups by name on every OIDC login, but only to groups it
# has, and a group without permissions shows nothing, not even the settings (#231, 2026-10-22).
CRUD = ("view", "add", "change", "delete")
GROUPS = {
    # everything of Paperless' own: the users, groups, mail, workflows and settings included
    "admins": ["%s_%s" % (a, m) for m in ("document", "tag", "correspondent", "documenttype", "storagepath",
               "savedview", "note", "uisettings", "sharelink", "sharelinkbundle", "customfield", "paperlesstask",
               "mailaccount", "mailrule", "processedmail", "workflow", "user", "group",
               "applicationconfiguration", "logentry") for a in CRUD]
              + ["view_global_statistics", "view_system_monitoring"],
    # the documents and what files them, their own views, notes, share links and settings, their
    # tasks (to see and dismiss an upload); not the mail inbox (laib makes it from values, a change
    # by hand is overwritten on the next apply), the workflows, the custom fields' definitions,
    # the users, the groups or the history
    "family": ["%s_%s" % (a, m) for m in ("document", "tag", "correspondent", "documenttype", "storagepath",
               "savedview", "note", "uisettings", "sharelink", "sharelinkbundle") for a in CRUD]
              + ["view_paperlesstask", "change_paperlesstask", "view_customfield"],
}


def groups(api):
    """the household's groups with their permissions: made when missing, given what they lack;
    a permission someone added by hand stays. Only codenames this Paperless has (its admin's
    own list), so a Paperless without one of them still applies."""
    have = set((api("GET", "/api/ui_settings/") or {}).get("permissions") or [])
    existing = {g.get("name"): g for g in api.all("/api/groups/")}
    for name, want in GROUPS.items():
        perms = sorted(c for c in want if c in have)
        g = existing.get(name)
        if g is None:
            api("POST", "/api/groups/", {"name": name, "permissions": perms})
            print("paperless: Gruppe %s angelegt (%d Rechte)" % (name, len(perms)))
            continue
        missing = [c for c in perms if c not in (g.get("permissions") or [])]
        if not missing:
            print("paperless: Gruppe %s unverändert" % name)
            continue
        api("PATCH", "/api/groups/%d/" % g["id"], {"permissions": sorted(set(g.get("permissions") or []) | set(perms))})
        print("paperless: Gruppe %s ergänzt um %d Rechte" % (name, len(missing)))
    return have


# Paperless' AI and OCR come from the values, as environment (#567). Paperless takes a field of
# its own settings (Einstellungen → Konfiguration, ApplicationConfiguration) over the environment,
# so setup clears the fields the template sets: the values rule, a field set there by hand is
# reset on the next apply (as the mail rule). The others stay the owner's.
OWNED = ("ai_enabled", "llm_backend", "llm_model", "llm_api_key", "llm_endpoint", "llm_embedding_backend",
         "llm_embedding_model", "llm_embedding_endpoint", "llm_embedding_chunk_size", "llm_context_size",
         "llm_request_timeout", "llm_output_language", "mode", "rotate_pages", "deskew")
# the contract and invoice fields (#567, #818, #820): made once each, found by name, never changed; the family fills them
CONTRACT = [("Anbieter", "string"), ("Vertragsnummer", "string"), ("Vertragsbeginn", "date"), ("Vertragsende", "date"),
            ("Kündigungsfrist", "string"), ("Nächster Kündigungstermin", "date"), ("Betrag", "monetary"),
            ("Zahlungsintervall", "string"), ("Rechnungsnummer", "string"), ("Fällig am", "date"),   # the last three: invoices (#818, #820)
            ("Verwendungszweck", "string")]


def config(api):
    rows = api("GET", "/api/config/") or []
    for row in rows if isinstance(rows, list) else rows.get("results", []):
        hand = {k: None for k in OWNED if row.get(k) not in (None, "")}
        if hand:
            api("PATCH", "/api/config/%d/" % row["id"], hand)
            print("paperless: in Paperless' Konfiguration von Hand gesetzt, jetzt aus den Werten:", ", ".join(sorted(hand)))
    have = {f.get("name", "").casefold() for f in api.all("/api/custom_fields/")}
    for name, kind in CONTRACT:
        if name.casefold() not in have:
            api("POST", "/api/custom_fields/", {"name": name, "data_type": kind})
            print("paperless: Feld %s angelegt" % name)


# Paperless' clients: Dokumente (ADR 0028, mdopp/dokumente, run by the scan template) reads contracts
# and invoices, Gesundheit (laib#893, mdopp/gesundheit, its own template) reads lab sheets and
# reports. Each through Paperless' API as its own user, never the admin: it sees and changes
# documents (their tags, notes, custom field values, correspondent and owner), makes tags (prüfen,
# korrespondent-ok, gesundheit), reads the names it needs (correspondents, document types, custom
# field definitions, and the users, so it never takes a household person for a sender, #737, and
# knows whose a sheet is). Nothing else: no deleting, no mail, no workflows, no settings, no users
# or groups. One workflow „Dokument hinzugefügt“ gives every client view and change on each new
# document and posts to each client's hook with its own token.
KUNDEN = [
    {"user": "dokumente", "name": "Dokumente", "passwort": "PAPERLESS_DOKUMENTE_PASSWORD", "token": "PAPERLESS_TOKEN",
     "hook": "http://host.containers.internal:8720/hook", "header": "X-Dokumente-Token", "hook_token": "DOKUMENTE_WEBHOOK_TOKEN",
     "ohne_hook": "Dokumente sieht neue Dokumente erst zur vollen Stunde"},   # the scan service's listener on the host, never through Caddy
    {"user": "gesundheit", "name": "Gesundheit", "passwort": "PAPERLESS_GESUNDHEIT_PASSWORD", "token": "PAPERLESS_GESUNDHEIT_TOKEN",
     "hook": "http://host.containers.internal:18529/hook", "header": "X-Gesundheit-Token", "hook_token": "GESUNDHEIT_WEBHOOK_TOKEN",
     "ohne_hook": "Gesundheit sieht neue Befunde erst zur vollen Stunde"},   # the gesundheit template's inner port (laib#894)
]
DOKUMENTE_USER = KUNDEN[0]["user"]
# add_document: a „nur für mich“ stack goes in through the API, owned by dokumente until it hands it on
DOKUMENTE_RECHTE = ["view_document", "add_document", "change_document", "view_note", "add_note", "view_tag", "add_tag",
                    "view_correspondent", "view_documenttype", "view_customfield", "view_user"]
WORKFLOW = "Dokument hinzugefügt"
HOOK_URL = KUNDEN[0]["hook"]
# the document types that are medical records (laib#893): made once each, found by name, never
# changed; the gesundheit template's BEFUND_ARTEN names the same (compared folded)
BEFUND_ARTEN = "Befund,Arztbrief,Labor,Röntgen,Atemtest,Lungenfunktion"


def kunden(api, have):
    """Paperless' side of its clients: each user with exactly its rights and its token handed back
    (only when it differs), then the one workflow with every client's hook and view and change for
    every client on the documents a person owns. A client whose password the box has not (its
    template not installed) is left out and said; the others go on. A second run writes nothing."""
    uids, hooks = [], []
    for k in KUNDEN:
        pw = E.get("LAIB_SECRET_" + k["passwort"], "")
        if not pw:
            print("paperless: kein %s, %s nicht eingerichtet" % (k["passwort"], k["name"]))
            continue
        uids.append(benutzer(api, have, k, pw)["id"])
        token = E.get("LAIB_SECRET_" + k["hook_token"], "")
        if token:
            hooks.append((k["hook"], {k["header"]: token}))
        else:
            print("paperless: kein %s, %s" % (k["hook_token"], k["ohne_hook"]))
    if uids:
        workflow(api, uids, hooks)
        besitz(api, uids)


def dokumente(api, have):
    """kept for the tests and the README: Paperless' side of its clients"""
    kunden(api, have)


def benutzer(api, have, k, pw):
    """one client's user (no administrator, exactly its rights, the box's password) and its token,
    printed as laib-secret when it differs from the one the box has"""
    want = {"is_superuser": False, "is_staff": False, "is_active": True, "groups": [],
            "user_permissions": sorted(c for c in DOKUMENTE_RECHTE if c in have)}
    u = next((x for x in api.all("/api/users/") if x.get("username") == k["user"]), None)
    if u is None:
        u = api("POST", "/api/users/", {"username": k["user"], "first_name": k["name"], "password": pw, **want})
        print("paperless: Benutzer %s für %s angelegt (%d Rechte, kein Administrator)" % (k["user"], k["name"], len(want["user_permissions"])))
    elif any(sorted(u.get(x) or []) != v if isinstance(v, list) else u.get(x) != v for x, v in want.items()):
        u = api("PATCH", "/api/users/%d/" % u["id"], want)
        print("paperless: Benutzer %s wieder auf seine Rechte gesetzt" % k["user"])
    token = obtain(api, k["user"], pw)
    if token is None:   # its password is not the box's (made by hand, or a restored database): the box's wins
        api("PATCH", "/api/users/%d/" % u["id"], {"password": pw})
        token = obtain(api, k["user"], pw)
    if token is None:
        raise SystemExit("paperless: der Benutzer %s bekommt mit seinem Passwort keinen Token" % k["user"])
    if token != E.get("LAIB_SECRET_" + k["token"], ""):
        print("laib-secret: %s=%s" % (k["token"], token))
        print("paperless: Zugang für %s ausgestellt" % k["name"])
    return u


def obtain(api, user, pw):
    """the user's token (Paperless makes it once, then hands the same one), None for a wrong password"""
    try:
        return api("POST", "/api/token/", {"username": user, "password": pw})["token"]
    except urllib.error.HTTPError as e:
        if e.code == 400:
            return None
        raise


def workflow(api, uids, hooks):
    """„Dokument hinzugefügt“: every client may see and change the new document, and each with a
    hook token hears of it there; found by name, made again when it differs. No hook at all: no
    workflow, the hourly runs see what they may"""
    if not hooks:
        return
    want = {"name": WORKFLOW, "order": 0, "enabled": True,
            "triggers": [{"type": 2, "sources": [1, 2, 3, 4]}],   # Document Added, from the consume folder, the API, mail and the web page
            "actions": [{"type": 1, "assign_view_users": uids, "assign_change_users": uids}]
            + [{"type": 4, "webhook": {"url": url, "use_params": True, "as_json": False, "params": None, "body": None,
                                       "headers": headers, "include_document": False}} for url, headers in hooks]}
    have = [w for w in api.all("/api/workflows/") if w.get("name") == WORKFLOW]
    if len(have) == 1 and same(have[0], want):
        print("paperless: Ablauf %s unverändert" % WORKFLOW)
        return
    for w in have:
        api("DELETE", "/api/workflows/%d/" % w["id"])
    api("POST", "/api/workflows/", want)
    print("paperless: Ablauf %s %s" % (WORKFLOW, "erneuert" if have else "angelegt"))


def same(w, want):
    """the parts of a workflow this template sets, compared"""
    def shape(x):
        acts = []
        for a in x.get("actions") or []:
            h = a.get("webhook") or {}
            acts.append((a.get("type"), sorted(a.get("assign_view_users") or []), sorted(a.get("assign_change_users") or []),
                         h.get("url"), h.get("headers") or {}))
        return (x.get("enabled"), [(t.get("type"), sorted(t.get("sources") or [])) for t in x.get("triggers") or []], acts)
    return shape(w) == shape(want)


def besitz(api, uids):
    """the documents a person owns: every client may see and change them too (merged, the owner's
    and the others' permissions stay); asked once per document"""
    docs = api.all("/api/documents/?owner__isnull=false&full_perms=true&fields=id,permissions")
    fehlt = [d["id"] for d in docs if any(u not in (((d.get("permissions") or {}).get("change") or {}).get("users") or []) for u in uids)]
    if not fehlt:
        return
    api("POST", "/api/documents/bulk_edit/", {"documents": fehlt, "method": "set_permissions",
                                               "parameters": {"set_permissions": {"view": {"users": uids, "groups": []},
                                                                                  "change": {"users": uids, "groups": []}},
                                                              "merge": True}})
    print("paperless: die Helfer dürfen %d Dokument%s sehen, die einer Person gehören" % (len(fehlt), "" if len(fehlt) == 1 else "e"))


def befunde(api):
    """the document types of medical records (laib#893), from PAPERLESS_BEFUND_ARTEN: made when
    missing, found by name, never changed; Paperless' AI then has them to suggest"""
    arten = [a.strip() for a in E.get("PAPERLESS_BEFUND_ARTEN", BEFUND_ARTEN).split(",") if a.strip()]
    have = {t.get("name", "").casefold() for t in api.all("/api/document_types/")}
    for name in arten:
        if name.casefold() not in have:
            api("POST", "/api/document_types/", {"name": name, "matching_algorithm": 0})
            print("paperless: Dokumenttyp %s angelegt" % name)


def failed_consumes(api):
    """findings for the documents Paperless could not file and still lists as failed
    (a task nobody dismissed): from mail, the consume folder or an upload alike.
    API version 9 on purpose: Paperless 2.x speaks it, 3.x keeps it for old clients,
    and its shape (a plain list, uppercase states) is the stable one."""
    try:
        tasks = api("GET", "/api/tasks/?task_name=consume_file&acknowledged=false",
                    accept="application/json; version=9")
    except urllib.error.HTTPError as e:
        if e.code in (401, 403):
            return refused(e.code)["findings"]
        return [{"severity": "gelb", "detail": "Paperless antwortet nicht auf die Dateiaufgaben: %s" % e, "fix": "laib logs paperless"}]
    except (OSError, ValueError) as e:
        return [{"severity": "gelb", "detail": "Paperless antwortet nicht auf die Dateiaufgaben: %s" % e, "fix": "laib logs paperless"}]
    if isinstance(tasks, dict):
        tasks = tasks.get("results", [])
    bad = [t for t in tasks or [] if t.get("status") == "FAILURE" and t.get("task_name") == "consume_file"
           and not t.get("acknowledged")]
    if not bad:
        return []
    names = ", ".join("%s (%s)" % (t.get("task_file_name") or "?", (t.get("result") or "").strip()[:160]) for t in bad[:3])
    return [{"severity": "gelb",
             "detail": "Paperless konnte %d Dokument%s nicht ablegen: %s%s" % (len(bad), "" if len(bad) == 1 else "e", names, " …" if len(bad) > 3 else ""),
             "fix": "in Paperless unter Dateiaufgaben → Fehlgeschlagen den Grund lesen und das Dokument neu ablegen "
                    "(aus einer Mail: Einstellungen → E-Mail, bei der Regel die verarbeiteten E-Mails öffnen und den "
                    "fehlgeschlagenen Eintrag löschen, beim nächsten Abruf holt Paperless sie neu), dann die Aufgabe verwerfen"}]


def check(api, status):
    """the status: whether Paperless can log in to the mailbox the values name, and the
    documents it fetched or was given but could not file"""
    r = inbox(api)
    if not any("lehnt den Admin" in f["detail"] for f in r["findings"]):
        r["findings"] += failed_consumes(api)
    return r


def inbox(api):
    w = want()
    if w is None:
        return {"detail": "", "findings": []}  # no mail inbox, nothing to say about one (as before #521)
    account, rule = w
    who = account["username"] + " auf " + account["imap_server"]
    try:
        acc, rul = find(api, account, rule)
    except urllib.error.HTTPError as e:
        if e.code in (401, 403):
            return refused(e.code)
        return {"detail": "E-Mail-Eingang nicht geprüft", "findings": [{"severity": "gelb",
                "detail": "Paperless antwortet nicht auf die Mailkonten: %s" % e, "fix": "laib logs paperless"}]}
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


def refused(code):
    """the admin laib knows is not one Paperless takes: after a move from ServiceBay its
    database keeps ServiceBay's administrator, and Paperless makes ADMIN_USER only in an
    empty one. A finding, not a red apply: Paperless itself runs, only the inbox waits."""
    user = E.get("ADMIN_USER") or E.get("PAPERLESS_ADMIN_USER") or "admin"
    return {"detail": "Paperless nicht eingerichtet", "findings": [{"severity": "rot",
            "detail": "Paperless lehnt den Admin %s ab (%d): die Gruppen admins und family, Mailkonto und Regel werden nicht angelegt" % (user, code),
            "fix": "ADMIN_USER und PAPERLESS_ADMIN_PASSWORD auf einen Paperless-Administrator setzen "
                   "(laib secret set PAPERLESS_ADMIN_PASSWORD über stdin), dann laib apply paperless"}]}


def up(api):
    """None when the API takes the admin, else the status that says why not"""
    for _ in range(60):
        try:
            api("GET", "/api/ui_settings/")
            return None
        except urllib.error.HTTPError as e:
            if e.code in (401, 403):
                return refused(e.code)
            time.sleep(5)
        except OSError:
            time.sleep(5)
    raise SystemExit("paperless: die API antwortet nicht")


if __name__ == "__main__":
    mode = sys.argv[1] if len(sys.argv) > 1 else "check"
    if mode == "setup":   # post-deploy, in the hooks image on the host network
        if E.get("AI_ENABLED", "").strip().lower() == "true" and not E.get("MODEL_URL"):
            # Paperless' AI client without an endpoint asks OpenAI's: no document leaves the box by default
            raise SystemExit("paperless: AI_ENABLED ist true, aber die Box hat kein Modell (keine Rolle model, kein box.yml model.url): "
                             "in services/paperless/values.yml AI_ENABLED: \"false\" setzen oder ein Modell einrichten, dann laib apply paperless")
        api = API(E.get("PAPERLESS_API", "http://127.0.0.1:8000"), E.get("ADMIN_USER", "admin"), E["LAIB_SECRET_PAPERLESS_ADMIN_PASSWORD"])
        status = os.path.join(E["LAIB_DATA_DIR"], "paperless", "data", "laib-status.json")
        r = up(api)   # a refused admin is a finding, not a red apply: Paperless runs, the groups and the inbox wait
        if r is None:
            have = groups(api)
            config(api)
            kunden(api, have)
            befunde(api)
            setup(api, E.get("LAIB_SECRET_PAPERLESS_MAIL_PASSWORD", ""))
    else:                 # inside the Paperless container: its own admin from its environment
        api = API(E.get("PAPERLESS_API", "http://127.0.0.1:8000"), E.get("PAPERLESS_ADMIN_USER", "admin"), E.get("PAPERLESS_ADMIN_PASSWORD", ""))
        status = E.get("LAIB_STATUS_FILE", "/usr/src/paperless/data/laib-status.json")
        r = None
    r = r or check(api, status)
    write(status, r)
    for f in r["findings"]:
        print("%s: %s" % (f["severity"], f["detail"]))
