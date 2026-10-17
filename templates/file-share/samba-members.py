#!/usr/bin/env python3
"""file-share's Samba members (#296): who may have a Samba account, the LLDAP
users in family or admins, written to $ACCOUNTS_DIR/members one name a line
for samba-accounts.sh. Read at the start and every few minutes through LLDAP's
GraphQL with the read-only account file-share (lldap_strict_readonly, seeded by
the auth template like radicale's, #55), never as the admin. LLDAP down or the
login refused: the list stays as it was, an outage removes nobody. A name
Samba cannot take is said and left out."""
import json, os, re, sys, time, urllib.request

URL = os.environ.get("LLDAP_URL", "http://127.0.0.1:17170")
DIR = os.environ.get("ACCOUNTS_DIR", "/samba-accounts")
EVERY = int(os.environ.get("EVERY", "300"))
GROUPS = ("family", "admins")
NAME = re.compile(r"^[a-z0-9][a-z0-9._-]{0,31}$")   # as samba-accounts.sh takes them


def post(path, body, token=None):
    req = urllib.request.Request(URL + path, data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json", **({"Authorization": "Bearer " + token} if token else {})})
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.load(r)


def members():
    token = post("/auth/simple/login", {"username": "file-share", "password": os.environ["LDAP_PASSWORD"]})["token"]
    res = post("/api/graphql", {"query": "{ groups { displayName users { id } } }"}, token)
    if res.get("errors"):
        raise RuntimeError(res["errors"])
    names = {u["id"] for g in res["data"]["groups"] if g["displayName"] in GROUPS for u in g["users"]}
    for n in sorted(n for n in names if not NAME.match(n)):
        print(f"samba: {n!r} gets no Samba account: not a name Samba takes (a-z, 0-9, . _ -)", file=sys.stderr)
    return sorted(n for n in names if NAME.match(n))


def write(names):
    path, text = os.path.join(DIR, "members"), "".join(n + "\n" for n in names)
    old = open(path).read() if os.path.exists(path) else None
    if old == text:
        return
    with open(path + ".tmp", "w") as f:
        f.write(text)
    os.replace(path + ".tmp", path)
    print("samba: members:", ", ".join(names) or "(none)", flush=True)


def main():
    os.makedirs(DIR, exist_ok=True)
    while True:
        try:
            write(members())
        except Exception as e:   # urllib's errors name the URL and the status, never the body sent
            print(f"samba: LLDAP not read ({e}); the member list stays as it was", file=sys.stderr, flush=True)
        if "--once" in sys.argv:
            return
        time.sleep(EVERY)


if __name__ == "__main__":
    main()
