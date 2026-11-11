#!/usr/bin/env python3
"""vaultwarden's detail line for the Box page (#785): how many accounts the
password vault has, how many of them with a second factor, measured through
Vaultwarden's admin API.

It logs in to /admin with the admin token (VAULTWARDEN_ADMIN_TOKEN, the
recovery sheet's), takes the VW_ADMIN cookie and reads /admin/users. Only
reads; the cookie is gone with the run. Vaultwarden counts an admin login
against its rate limit (3 in 5 minutes by default): the timer runs every 15
minutes, laib's look at most once per stale. A refused token or a silent
Vaultwarden is "nicht gemessen" and why, never a number.

Whose account has no second factor (#1168): LLDAP's logins and mail
addresses, read through its GraphQL with the read-only account vaultwarden
(LDAP_PASSWORD, seeded by the auth template like file-share's), never as
the admin. An enabled account without a second factor whose address is a
login's is a finding that asks for that person's task, „Zweiten Faktor für
deinen Passwort-Tresor einrichten“; laib closes it once the account has
one. The finding's words name nobody: the admins read the count, as in the
line. Without LDAP_PASSWORD only the line; LLDAP refusing or silent is
"nicht gemessen", so no task closes on a guess.

Environment: VAULTWARDEN_URL, ADMIN_TOKEN, LLDAP_URL, LDAP_PASSWORD, STATUS_DIR.
"""
import datetime, http.cookiejar, json, os, re, urllib.error, urllib.parse, urllib.request

URL = os.environ.get("VAULTWARDEN_URL", "http://127.0.0.1:8222").rstrip("/")
TOKEN = os.environ.get("ADMIN_TOKEN", "")
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")
LLDAP = os.environ.get("LLDAP_URL", "http://127.0.0.1:17170").rstrip("/")
LDAP_PASSWORD = os.environ.get("LDAP_PASSWORD", "")
LOGIN = re.compile(r"^[a-z0-9][a-z0-9._-]{0,48}$")   # a login the kernel takes for person:<login>, short enough for the topic
WEG = "/einrichten/passwoerter"   # Homes' page for the vault: its address and the Bitwarden app
WARUM = ("Dein Tresor ist bisher nur mit dem Master-Passwort geschützt. Öffne ihn im Browser, dann Einstellungen → "
         "Sicherheit → Zweistufige Anmeldung, und richte eine Authenticator-App ein. Laib hakt die Aufgabe ab, sobald es klappt.")


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def unmeasured(why, fix):
    status("nicht gemessen: " + why, [{"severity": "info", "unmeasured": True, "detail": "Vaultwardens Konten nicht gemessen: " + why, "fix": fix}])


def n(x):
    return f"{int(x or 0):,}".replace(",", ".")


def logins():
    """LLDAP's mail addresses, lower case, to their logins, through the read-only account"""
    def post(path, body, token=None):
        req = urllib.request.Request(LLDAP + path, data=json.dumps(body).encode(),
                                     headers={"Content-Type": "application/json", **({"Authorization": "Bearer " + token} if token else {})})
        with urllib.request.urlopen(req, timeout=10) as r:
            return json.load(r)
    token = post("/auth/simple/login", {"username": "vaultwarden", "password": LDAP_PASSWORD})["token"]
    res = post("/api/graphql", {"query": "{ users { id email } }"}, token)
    if res.get("errors"):
        raise ValueError("LLDAP antwortet mit einem Fehler")
    return {str(u["email"]).lower(): u["id"] for u in res["data"]["users"] if u.get("email") and LOGIN.match(u["id"])}


def asks(users):
    """a finding per enabled account without a second factor whose address is a login's"""
    try:
        whose = logins()
    except (OSError, ValueError, KeyError, TypeError) as e:   # urllib's errors name the URL and the status, never the body sent
        why = f"LLDAP antwortet nicht ({e.code if isinstance(e, urllib.error.HTTPError) else type(e).__name__})"
        return [{"severity": "info", "unmeasured": True, "detail": "Wessen Konto ohne zweiten Faktor ist, nicht gemessen: " + why, "fix": "laib logs auth"}]
    out = []
    for u in users:
        login = whose.get(str(u.get("email") or "").lower())
        if login and u.get("userEnabled", True) and not u.get("twoFactorEnabled"):
            out.append({"severity": "info", "topic": "zweiter-faktor." + login, "detail": "Ein Konto ohne zweiten Faktor, seine Aufgabe steht in Homes",
                        "neue_aufgabe": {"titel": "Zweiten Faktor für deinen Passwort-Tresor einrichten", "warum": WARUM,
                                         "fuer": "person:" + login, "weg": {"seite": WEG}}})
    return out


class Stay(urllib.request.HTTPRedirectHandler):
    """the login answers with a redirect to the admin page: the cookie is what counts, not the page"""
    def redirect_request(self, *a):
        return None


def main():
    jar = http.cookiejar.CookieJar()
    web = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), Stay)
    try:
        web.open(urllib.request.Request(URL + "/admin", data=urllib.parse.urlencode({"token": TOKEN}).encode(), method="POST"), timeout=15).read()
    except urllib.error.HTTPError as e:
        if not (300 <= e.code < 400):
            return unmeasured(f"Vaultwarden nimmt das Admin-Token nicht an (HTTP {e.code})", "laib logs vaultwarden; das Token: VAULTWARDEN_ADMIN_TOKEN")
    except OSError as e:
        return unmeasured(f"Vaultwarden antwortet nicht ({e})", "laib inspect vaultwarden")
    if not any(c.name == "VW_ADMIN" for c in jar):
        return unmeasured("Vaultwarden gab nach dem Admin-Token keine Sitzung", "laib logs vaultwarden")
    try:
        with web.open(urllib.request.Request(URL + "/admin/users", headers={"Accept": "application/json"}), timeout=15) as r:
            users = json.loads(r.read())
    except (OSError, ValueError) as e:
        return unmeasured(f"Vaultwarden antwortet nicht vollständig ({e})", "laib logs vaultwarden")
    on = [u for u in users if u.get("userEnabled", True)]
    off = len(users) - len(on)
    two = sum(1 for u in on if u.get("twoFactorEnabled"))
    detail = f"{n(len(on))} Kont" + ("o" if len(on) == 1 else "en") + f", {n(two)} mit zweitem Faktor"
    if off:
        detail += f" · {n(off)} gesperrt"
    status(detail, asks(users) if LDAP_PASSWORD else [])


if __name__ == "__main__":
    main()
