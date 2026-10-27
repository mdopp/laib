#!/usr/bin/env python3
"""usage-metrics' detail line for the Box page (#785): what was counted in the
last 7 days and from which apps (GET /summary?days=7), and how many sentences
the assistants could not answer (GET /misses?days=7, its total), with the
service's own bearer. Only reads, never a sentence itself. Refused or silent
is "nicht gemessen" and why.

Environment: USAGE_URL, USAGE_TOKEN, STATUS_DIR.
"""
import datetime, json, os, urllib.error, urllib.request

URL = os.environ.get("USAGE_URL", "http://host.containers.internal:8094").rstrip("/")
TOKEN = os.environ.get("USAGE_TOKEN", "")
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def unmeasured(why, fix):
    status("nicht gemessen: " + why, [{"severity": "info", "unmeasured": True, "detail": "Nutzung nicht gemessen: " + why, "fix": fix}])


def n(x):
    return f"{int(x or 0):,}".replace(",", ".")


def get(path):
    with urllib.request.urlopen(urllib.request.Request(URL + path, headers={"Authorization": "Bearer " + TOKEN}), timeout=15) as r:
        return json.load(r)


def main():
    try:
        s, m = get("/summary?days=7"), get("/misses?days=7")
        apps = [a["app"] for a in s["apps"] if a.get("total")]
        total, misses = int(s["total"]), int(m["total"])
    except urllib.error.HTTPError as e:
        return unmeasured(f"usage-metrics lehnt den Schlüssel ab (HTTP {e.code})" if e.code in (401, 403) else f"usage-metrics antwortet mit HTTP {e.code}",
                          "laib logs usage-metrics")
    except (OSError, ValueError, KeyError, TypeError) as e:
        return unmeasured(f"usage-metrics antwortet nicht ({e})", "laib inspect usage-metrics")
    counted = f"{n(total)} Zählungen aus {', '.join(sorted(apps))}" if total and apps else "nichts gezählt"
    asked = f"{n(misses)} unbeantwortete Frage{'n' if misses != 1 else ''}" if misses else "keine unbeantworteten Fragen"
    status(f"letzte 7 Tage: {counted} · {asked}")


if __name__ == "__main__":
    main()
