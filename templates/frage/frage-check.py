#!/usr/bin/env python3
"""frage's detail line for the Box page (#785): one SearXNG search, as Frage
itself asks it (pod-local, format=json), and what came back: how many results
and which search engines stayed silent. Once an hour, so the box asks the web
engines no more than a person would. No results is a gelb finding (Frage
would answer from nothing); a SearXNG that refuses or does not answer is
"nicht gemessen" and why.

Environment: SEARX_URL, STATUS_DIR.
"""
import datetime, json, os, urllib.error, urllib.parse, urllib.request

URL = os.environ.get("SEARX_URL", "http://127.0.0.1:8888").rstrip("/")
QUERY = "Wetter"
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def unmeasured(why, fix):
    status("nicht gemessen: " + why, [{"severity": "info", "unmeasured": True, "detail": "Websuche nicht gemessen: " + why, "fix": fix}])


def n(x):
    return f"{int(x or 0):,}".replace(",", ".")


def main():
    try:
        with urllib.request.urlopen(URL + "/search?" + urllib.parse.urlencode({"q": QUERY, "format": "json"}), timeout=30) as r:
            d = json.load(r)
        results = len(d["results"])
        silent = [str(e[0]) for e in d.get("unresponsive_engines") or [] if e]
    except urllib.error.HTTPError as e:
        return unmeasured(f"SearXNG verweigert die Suche (HTTP {e.code})", "laib apply frage: pre-start schreibt das JSON-Format in die Einstellungen (#490)")
    except (OSError, ValueError, KeyError, TypeError) as e:
        return unmeasured(f"SearXNG antwortet nicht ({e})", "laib inspect frage")
    still = f"{len(silent)} Suchmaschine{'n' if len(silent) != 1 else ''} still ({', '.join(silent)})" if silent else ""
    if not results:
        detail = "Websuche findet nichts" + (": " + still if still else "")
        return status(detail, [{"severity": "gelb", "detail": detail + ": Frage antwortet ohne Quellen", "fix": "laib logs frage"}])
    status(f"Websuche antwortet: {n(results)} Treffer" + (", " + still if still else ""))


if __name__ == "__main__":
    main()
