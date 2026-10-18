#!/usr/bin/env python3
"""auth's person tool (#360): what `laib person invite|pending` asks of the
identity service, run by laib in the hooks image (host network, the
service's data paths, its secrets as LAIB_SECRET_*), never on the host.

invite <user> <first> <last> <email> <group|-> <resend 0|1>
  creates the LLDAP user without a password, in the group, and starts
  Authelia's reset-password identity flow for it: Authelia mails the link
  (German, templates/IdentityVerificationJWT.*, pre-start), or, with the
  file notifier, the link is read back from notification.txt and returned.
  No password passes through here. --resend starts the flow again for a
  user that exists and keeps its group.
pending
  the invited users who have not used their link yet.

The last line of stdout is one JSON object: the result, or {"error": "<a
German sentence>"} with exit code 3. Nothing secret is printed."""
import json, os, re, sqlite3, sys, time, urllib.error, urllib.request

E = os.environ
D = E.get("LAIB_DATA_DIR", "/mnt/data/stacks")
DOMAIN = E["LAIB_DOMAIN"]
LLDAP = E.get("LAIB_LLDAP_URL", "http://127.0.0.1:17170")
AUTHELIA = E.get("LAIB_AUTHELIA_URL", "http://127.0.0.1:9091")
CFG = os.path.join(D, "auth", "authelia-config")
DATA = os.path.join(D, "auth", "authelia-data")
INVITES = os.path.join(CFG, "laib-invites.json")   # user -> {invited_at, used}: who was invited, how many links were used then
GROUPS = ("family", "admins")
LIFESPAN = 24 * 3600   # identity_validation.reset_password.jwt_lifespan, pre-start


def out(obj, code=0):
    print(json.dumps(obj, ensure_ascii=False))
    sys.exit(code)


def refuse(sentence):
    out({"error": sentence}, 3)


def http(url, body=None, headers=None):
    req = urllib.request.Request(url, data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Content-Type": "application/json", **(headers or {})})
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.load(r)


def lldap():
    try:
        token = http(LLDAP + "/auth/simple/login", {"username": "admin", "password": E["LAIB_SECRET_LLDAP_ADMIN_PASSWORD"]})["token"]
    except (OSError, KeyError, ValueError):
        refuse("LLDAP antwortet nicht oder nimmt das Admin-Passwort nicht: niemand wurde angelegt (laib status auth).")

    def gql(query, variables=None):
        res = http(LLDAP + "/api/graphql", {"query": query, "variables": variables or {}}, {"Authorization": "Bearer " + token})
        if res.get("errors"):
            raise RuntimeError(res["errors"][0].get("message", "LLDAP error"))
        return res["data"]
    return gql


def used_links(user):
    """how many reset links the user has used, and the expiry of the newest one (Authelia's identity_verification)"""
    try:
        db = sqlite3.connect(f"file:{os.path.join(DATA, 'db.sqlite3')}?mode=ro", uri=True, timeout=10)
        try:
            used = db.execute("SELECT count(*) FROM identity_verification WHERE username = ? AND consumed IS NOT NULL", (user,)).fetchone()[0]
            row = db.execute("SELECT exp FROM identity_verification WHERE username = ? ORDER BY id DESC LIMIT 1", (user,)).fetchone()
        finally:
            db.close()
    except sqlite3.Error:
        return 0, None
    return used, rfc3339(row[0]) if row else None


def rfc3339(v):
    """Authelia's DATETIME text (Go's "2006-01-02 15:04:05.999999999-07:00") as RFC 3339"""
    m = re.match(r"(\d{4}-\d\d-\d\d)[ T](\d\d:\d\d:\d\d)(?:\.\d+)?\s*(Z|[+-]\d\d:?\d\d)?", str(v))
    if not m:
        return None
    tz = (m.group(3) or "Z").replace("+00:00", "Z")
    return f"{m.group(1)}T{m.group(2)}{tz}"


def load_invites():
    try:
        return json.load(open(INVITES))
    except (OSError, ValueError):
        return {}


def save_invites(inv):
    with open(INVITES + ".tmp", "w") as f:
        json.dump(inv, f, indent=1, sort_keys=True)
    os.replace(INVITES + ".tmp", INVITES)


def notifier_is_smtp():
    try:
        return "\n  smtp:\n" in open(os.path.join(CFG, "configuration.yml")).read()
    except OSError:
        return False


def invite(user, first, last, email, group, resend):
    if not re.fullmatch(r"[a-z0-9][a-z0-9._-]{0,31}", user):
        refuse(f"„{user}“ geht nicht als Benutzername: Kleinbuchstaben, Ziffern, Punkt, Bindestrich, höchstens 32 Zeichen.")
    if not re.fullmatch(r"[^@\s]+@[^@\s]+\.[^@\s]+", email):
        refuse(f"„{email}“ ist keine Mailadresse.")
    if group not in GROUPS + ("-",):
        refuse(f"Die Gruppe „{group}“ gibt es nicht: family oder admins.")
    gql = lldap()
    users = {u["id"]: u for u in gql("{ users { id email displayName groups { displayName } } }")["users"]}
    if user in users:
        if not resend:
            refuse(f"Den Benutzer {user} gibt es schon. Eine neue Mail schickt „erneut senden“.")
        if users[user]["email"].lower() != email.lower():
            refuse(f"{user} hat die Adresse {users[user]['email']}, nicht {email}: die Mail geht nur an die Adresse in LLDAP.")
    else:
        other = next((u["id"] for u in users.values() if u["email"].lower() == email.lower()), None)
        if other:
            refuse(f"Die Adresse {email} gehört schon {other}.")
        groups = {g["displayName"]: g["id"] for g in gql("{ groups { id displayName } }")["groups"]}
        if group == "-":
            group = "family"
        gql("mutation($u:CreateUserInput!){ createUser(user:$u){ id } }",
            {"u": {"id": user, "email": email, "displayName": f"{first} {last}".strip(), "firstName": first, "lastName": last}})
        gql("mutation($u:String!,$g:Int!){ addUserToGroup(userId:$u, groupId:$g){ ok } }", {"u": user, "g": groups[group]})
        print(f"LLDAP: {user} created in {group}, no password", file=sys.stderr)
    used, _ = used_links(user)
    inv = load_invites()
    inv[user] = {"invited_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "used": used, "name": f"{first} {last}".strip(), "email": email}
    save_invites(inv)

    # Authelia's "Passwort vergessen", started for the user: it looks the user up in LLDAP and mails the link
    note = os.path.join(DATA, "notification.txt")
    before = os.stat(note).st_mtime_ns if os.path.exists(note) else 0
    try:
        http(AUTHELIA + "/api/reset-password/identity/start", {"username": user},
             {"X-Forwarded-Proto": "https", "X-Forwarded-Host": "auth." + DOMAIN, "X-Forwarded-For": "127.0.0.1"})
    except (OSError, ValueError) as e:
        refuse(f"{user} ist angelegt, aber Authelia hat den Link nicht verschickt ({e}); später erneut senden.")
    mailed, link = notifier_is_smtp(), None
    if not mailed:   # the file notifier: the link is in the file it just wrote
        for _ in range(50):
            if os.path.exists(note) and os.stat(note).st_mtime_ns != before:
                found = re.findall(r"https://\S+?/reset-password/step2\?token=[^\s\"'<>]+", open(note).read())
                if found:
                    link = found[-1]
                    break
            time.sleep(0.2)
        if not link:
            refuse(f"{user} ist angelegt, aber Authelia hat keinen Link geschrieben; später erneut senden.")
    _, expires = used_links(user)
    res = {"user": user, "name": f"{first} {last}".strip(), "email": email, "mailed": mailed,
           "expires": expires or time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + LIFESPAN))}
    if link:
        res["link"] = link
    out(res)


def pending():
    inv = load_invites()
    if not inv:
        out([])
    users = {u["id"] for u in lldap()("{ users { id } }")["users"]}
    left, rows = {}, []
    for user, i in sorted(inv.items()):
        if user not in users or used_links(user)[0] > i.get("used", 0):
            continue   # gone from LLDAP, or the link was used: no longer pending
        left[user] = i
        rows.append({"user": user, "name": i.get("name", ""), "email": i.get("email", ""), "invited_at": i["invited_at"]})
    if left != inv:
        save_invites(left)
    out(rows)


if __name__ == "__main__":
    a = sys.argv[1:]
    if a[:1] == ["invite"] and len(a) == 7:
        invite(a[1], a[2].strip(), a[3].strip(), a[4].strip(), a[5], a[6] == "1")
    elif a == ["pending"]:
        pending()
    refuse("Aufruf: person.py invite <user> <first> <last> <email> <group|-> <0|1> | pending")
