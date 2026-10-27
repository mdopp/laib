#!/usr/bin/env python3
"""bote's detail line for the Box page (#785):

- Signal, when it is switched on: signal-cli's daemon answering /api/v1/check
  in the pod (it runs only with a linked account, -a SIGNAL_ACCOUNT); silent is
  a gelb finding. The account's number reaches this script only as "set".
- the other channels switched on (Matrix, Telegram, Discord): named as
  switched on, which is what the values say; whether they are connected is
  not measured here.
- with usage-metrics' token: the last 7 days' conversations and unanswered
  turns, the bote app's events zug and unbeantwortet (nutzung.py, #266).

Only reads. A part not measured says so; no number is made up.

Environment: CH_SIGNAL, CH_MATRIX, CH_TELEGRAM, CH_DISCORD (set or empty),
SIGNAL_URL, USAGE_URL, USAGE_TOKEN, STATUS_DIR.
"""
import datetime, json, os, urllib.error, urllib.request

SIGNAL = os.environ.get("SIGNAL_URL", "http://127.0.0.1:8080").rstrip("/")
USAGE = os.environ.get("USAGE_URL", "http://host.containers.internal:8094").rstrip("/")
TOKEN = os.environ.get("USAGE_TOKEN", "")
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def n(x):
    return f"{int(x or 0):,}".replace(",", ".")


def on(name):
    return bool(os.environ.get(name, "").strip())


def main():
    parts, findings = [], []
    if on("CH_SIGNAL"):
        try:
            urllib.request.urlopen(SIGNAL + "/api/v1/check", timeout=10).close()
            parts.append("Signal verbunden")
        except (OSError, ValueError) as e:
            why = f"HTTP {e.code}" if isinstance(e, urllib.error.HTTPError) else str(e)
            parts.append(f"Signal antwortet nicht ({why})")
            findings.append({"severity": "gelb", "detail": f"Signal antwortet nicht ({why}): Nachrichten über Signal kommen nicht an",
                             "fix": "laib logs bote; ist das Gerät noch verknüpft, sagt signal-cli dort"})
    others = [label for env, label in (("CH_MATRIX", "Matrix"), ("CH_TELEGRAM", "Telegram"), ("CH_DISCORD", "Discord")) if on(env)]
    if others:
        parts.append(", ".join(others) + " eingeschaltet")
    if TOKEN:
        try:
            req = urllib.request.Request(USAGE + "/summary?days=7", headers={"Authorization": "Bearer " + TOKEN})
            with urllib.request.urlopen(req, timeout=15) as r:
                apps = json.load(r)["apps"]
            ev = {e["event"]: e["count"] for a in apps if a.get("app") == "bote" for e in a.get("events") or []}
            turns, missed = int(ev.get("zug", 0)), int(ev.get("unbeantwortet", 0))
            parts.append(f"letzte 7 Tage: {n(turns)} Gespräch{'e' if turns != 1 else ''}, {n(missed)} unbeantwortet" if turns else "letzte 7 Tage: keine Gespräche")
        except (OSError, ValueError, KeyError, TypeError) as e:
            why = f"HTTP {e.code}" if isinstance(e, urllib.error.HTTPError) else str(e)
            parts.append(f"Gespräche nicht gemessen ({why})")
            findings.append({"severity": "info", "unmeasured": True, "detail": f"Die Gespräche des Boten nicht gemessen ({why})", "fix": "laib inspect usage-metrics"})
    status(" · ".join(parts) or "kein Kanal eingeschaltet", findings)


if __name__ == "__main__":
    main()
