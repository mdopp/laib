#!/usr/bin/env python3
"""adguard's detail line for the Box page (#785): how many DNS queries the
house sent and how many AdGuard blocked, measured from AdGuard's own
statistics (/control/stats, the admin's basic auth).

The window is AdGuard's: its time_units times the length of its series (24
hours by default, a setting in the UI), named as such, never assumed. Only
reads. AdGuard refusing or not answering is "nicht gemessen" and why, never
a number.

Environment: ADGUARD_URL, ADGUARD_USER, ADGUARD_PASSWORD, STATUS_DIR.
"""
import base64, datetime, json, os, urllib.error, urllib.request

URL = os.environ.get("ADGUARD_URL", "http://127.0.0.1:8083").rstrip("/")
AUTH = "Basic " + base64.b64encode(f"{os.environ.get('ADGUARD_USER', 'admin')}:{os.environ.get('ADGUARD_PASSWORD', '')}".encode()).decode()
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def unmeasured(why, fix):
    status("nicht gemessen: " + why, [{"severity": "info", "unmeasured": True, "detail": "AdGuards Anfragen nicht gemessen: " + why, "fix": fix}])


def n(x):
    return f"{int(x or 0):,}".replace(",", ".")


def window(s):
    k = len(s.get("dns_queries") or [])
    if s.get("time_units") == "days":
        return "am letzten Tag" if k == 1 else f"in den letzten {k} Tagen"
    return f"in den letzten {k} h"


def main():
    try:
        with urllib.request.urlopen(urllib.request.Request(URL + "/control/stats", headers={"Authorization": AUTH}), timeout=15) as r:
            s = json.loads(r.read())
    except urllib.error.HTTPError as e:
        return unmeasured(f"AdGuard lässt den Admin nicht herein (HTTP {e.code})", "laib logs adguard; das Passwort: laib apply adguard")
    except (OSError, ValueError) as e:
        return unmeasured(f"AdGuard antwortet nicht ({e})", "laib inspect adguard")
    q, b = int(s.get("num_dns_queries") or 0), int(s.get("num_blocked_filtering") or 0)
    if not q:
        return status(f"{window(s)}: keine Anfragen")
    status(f"{window(s)}: {n(q)} Anfragen, {n(b)} blockiert ({round(100 * b / q)} %)")


if __name__ == "__main__":
    main()
