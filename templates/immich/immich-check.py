#!/usr/bin/env python3
"""immich's detail line for the Box page (#785): how many photos and videos
Immich holds, how much space they take and how many accounts there are,
measured through Immich's own API.

It logs in as Immich's admin (break-glass, as post-deploy does), reads
/api/server/statistics (admin only; the API key post-deploy mints may read
the system config and nothing else) and logs that session out again, so the
timer leaves no session behind. Only reads. Immich refusing or not answering
is "nicht gemessen" and why, never a number.

Environment: IMMICH_URL, ADMIN_EMAIL (empty: admin@DOMAIN), DOMAIN,
IMMICH_PASSWORD, STATUS_DIR.
"""
import datetime, json, os, urllib.error, urllib.request

URL = os.environ.get("IMMICH_URL", "http://127.0.0.1:2283").rstrip("/")
EMAIL = os.environ.get("ADMIN_EMAIL") or "admin@" + os.environ.get("DOMAIN", "")
PW = os.environ.get("IMMICH_PASSWORD", "")
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def unmeasured(why, fix):
    status("nicht gemessen: " + why, [{"severity": "info", "unmeasured": True, "detail": "Immichs Bestand nicht gemessen: " + why, "fix": fix}])


def n(x):
    return f"{int(x or 0):,}".replace(",", ".")


def size(b):
    """bytes in the units a phone shows: 412 GB, 1,2 TB"""
    b = int(b or 0)
    for unit, k in (("TB", 10**12), ("GB", 10**9), ("MB", 10**6)):
        if b >= k:
            v = b / k
            return (f"{v:.1f}".replace(".", ",").removesuffix(",0") if v < 10 else n(round(v))) + " " + unit
    return n(b) + " Bytes"


def req(method, path, body=None, token=""):
    h = {"Content-Type": "application/json", "Accept": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    data = json.dumps(body).encode() if body is not None else None
    with urllib.request.urlopen(urllib.request.Request(URL + path, data=data, headers=h, method=method), timeout=15) as r:
        raw = r.read()
    return json.loads(raw) if raw else None


def main():
    try:
        token = req("POST", "/api/auth/login", {"email": EMAIL, "password": PW})["accessToken"]
    except urllib.error.HTTPError as e:
        return unmeasured(f"Immich lässt den Admin {EMAIL} nicht herein (HTTP {e.code})", "ADMIN_EMAIL und IMMICH_ADMIN_PASSWORD prüfen, dann laib apply immich")
    except (OSError, ValueError, KeyError, TypeError) as e:
        return unmeasured(f"Immich antwortet nicht ({e})", "laib inspect immich")
    try:
        s = req("GET", "/api/server/statistics", token=token)
    except (OSError, ValueError) as e:
        return unmeasured(f"Immich antwortet nicht vollständig ({e})", "laib logs immich")
    finally:
        try:
            req("POST", "/api/auth/logout", token=token)
        except (OSError, ValueError):
            pass
    users = len(s.get("usageByUser") or [])
    status(f"{n(s.get('photos'))} Fotos, {n(s.get('videos'))} Videos · {size(s.get('usage'))} · {users} Kont" + ("o" if users == 1 else "en"))


if __name__ == "__main__":
    main()
