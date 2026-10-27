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

Environment: VAULTWARDEN_URL, ADMIN_TOKEN, STATUS_DIR.
"""
import datetime, http.cookiejar, json, os, urllib.error, urllib.parse, urllib.request

URL = os.environ.get("VAULTWARDEN_URL", "http://127.0.0.1:8222").rstrip("/")
TOKEN = os.environ.get("ADMIN_TOKEN", "")
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")


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
    status(detail)


if __name__ == "__main__":
    main()
