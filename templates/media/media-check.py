#!/usr/bin/env python3
"""media's detail line for the Box page (#785): what Jellyfin holds and who
has an account there, measured through Jellyfin's own API.

It logs in as Jellyfin's admin (break-glass, the same as post-deploy) under
its own device id, reads /Items/Counts, /Users and the sessions playing now,
and logs that session out again, so the timer leaves no device behind. Only
reads. The family's accounts are those the LDAP plugin made (#256): the
admin, Haus and the service accounts are not counted. Jellyfin refusing or
not answering is "nicht gemessen" and why, never a number.

Environment: JELLYFIN_URL, JELLYFIN_ADMIN, JELLYFIN_PASSWORD, STATUS_DIR.
"""
import datetime, json, os, urllib.error, urllib.request

URL = os.environ.get("JELLYFIN_URL", "http://127.0.0.1:8096").rstrip("/")
ADMIN, PW = os.environ.get("JELLYFIN_ADMIN", "admin"), os.environ.get("JELLYFIN_PASSWORD", "")
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")
# Jellyfin 12 reads the client id from Authorization only (as post-deploy)
CLIENT = 'MediaBrowser Client="laib", Device="laib", DeviceId="laib-check", Version="1"'
LDAP = "LDAP"


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def unmeasured(why, fix):
    status("nicht gemessen: " + why, [{"severity": "info", "unmeasured": True, "detail": "Jellyfins Bestand nicht gemessen: " + why, "fix": fix}])


def n(x):
    return f"{int(x or 0):,}".replace(",", ".")


def req(method, path, body=None, token=""):
    h = {"Content-Type": "application/json", "Authorization": CLIENT + (f', Token="{token}"' if token else "")}
    data = json.dumps(body).encode() if body is not None else None
    with urllib.request.urlopen(urllib.request.Request(URL + path, data=data, headers=h, method=method), timeout=15) as r:
        raw = r.read()
    return json.loads(raw) if raw else None


def main():
    try:
        token = req("POST", "/Users/AuthenticateByName", {"Username": ADMIN, "Pw": PW})["AccessToken"]
    except urllib.error.HTTPError as e:
        return unmeasured(f"Jellyfin lässt den Admin nicht herein (HTTP {e.code})", "laib logs media; das Admin-Passwort: laib apply media")
    except (OSError, ValueError, KeyError, TypeError) as e:
        return unmeasured(f"Jellyfin antwortet nicht ({e})", "laib inspect media")
    try:
        c = req("GET", "/Items/Counts", token=token)
        users = req("GET", "/Users", token=token)
        playing = sum(1 for s in req("GET", "/Sessions?ActiveWithinSeconds=900", token=token) or [] if s.get("NowPlayingItem"))
    except (OSError, ValueError, TypeError) as e:
        return unmeasured(f"Jellyfin antwortet nicht vollständig ({e})", "laib logs media")
    finally:
        try:
            req("POST", "/Sessions/Logout", token=token)
        except (OSError, ValueError):
            pass
    family = sum(1 for u in users or [] if LDAP in ((u.get("Policy") or {}).get("AuthenticationProviderId") or ""))
    detail = f"{n(c.get('MovieCount'))} Filme, {n(c.get('SeriesCount'))} Serien, {n(c.get('SongCount'))} Lieder · {family} Konten der Familie"
    if playing:
        detail += f" · läuft gerade auf {playing} Gerät" + ("en" if playing > 1 else "")
    status(detail)


if __name__ == "__main__":
    main()
