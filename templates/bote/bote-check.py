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

- the last night's probes (#862), from proben.json beside the status file.

Only reads. A part not measured says so; no number is made up.

With PROBEN=1 (bote-proben.service, nightly) it instead sends ten fixed
sentences to Hermes' API server, each in a fresh session, streamed as Haus's
Sprechen field does, and keeps per probe the reply time, the tools that ran and
what is wrong: a fact question must call frage and name the fact, something
nobody can know must be said to be unknown, every reply comes within
PROBE_LIMIT seconds and carries no error text. One line goes to the unit's
journal, the result to proben.json.

Environment: CH_SIGNAL, CH_MATRIX, CH_TELEGRAM, CH_DISCORD (set or empty),
SIGNAL_URL, USAGE_URL, USAGE_TOKEN, STATUS_DIR; for the probes PROBEN,
API_URL, BOTE_API_KEY, PROBE_LIMIT.
"""
import datetime, json, os, re, statistics, time, urllib.error, urllib.request, uuid

SIGNAL = os.environ.get("SIGNAL_URL", "http://127.0.0.1:8080").rstrip("/")
USAGE = os.environ.get("USAGE_URL", "http://host.containers.internal:8094").rstrip("/")
TOKEN = os.environ.get("USAGE_TOKEN", "")
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")
PROBEN = os.path.join(os.path.dirname(STATUS), "proben.json")
API = os.environ.get("API_URL", "http://127.0.0.1:8642/v1").rstrip("/")
LIMIT = float(os.environ.get("PROBE_LIMIT") or 30)


def has(*words):
    return lambda a, t: "" if any(re.search(r"(?i)\b" + re.escape(w) + r"\b", a) for w in words) else "sagt nicht: " + " / ".join(words)


def ran(server):
    return lambda a, t: "" if any(x.startswith(f"mcp__{server}__") for x in t) else f"kein {server}-Werkzeug" + (f" (nur {', '.join(t)})" if t else "")


def both(*checks):
    return lambda a, t: next((w for c in checks if (w := c(a, t))), "")


# (name, sentence, check(answer, tools) -> "" or what is wrong). Nothing here switches or writes:
# a device question reads only; switching is scripts/bote-schalttest.py, by hand.
PROBES = [
    ("Hallo", "Hallo", has("Zum Beispiel")),
    ("Hilfe", "Hilfe", has("Zum Beispiel")),
    ("Zugspitze", "Wie hoch ist die Zugspitze?", both(ran("frage"), has("2962", "2.962", "2 962"))),
    ("Australien", "Was ist die Hauptstadt von Australien?", both(ran("frage"), has("Canberra"))),
    ("Relativität", "Wer hat die Relativitätstheorie entwickelt?", both(ran("frage"), has("Einstein"))),
    ("Unbekannt", "Wie heißt der Hund meines Nachbarn?", has("weiß ich nicht", "weiß nicht", "keine Ahnung", "nicht bekannt", "kann ich nicht",
                                                           "nicht sagen", "nichts gefunden", "nicht herausfinden", "keine Information", "keine Informationen")),
    ("Lichter", "Welche Lichter sind gerade an?", has("Licht", "Lichter", "Lampe", "Lampen", "an", "aus", "keine")),
    ("Uhrzeit", "Wie spät ist es?", lambda a, t: "" if re.search(r"\d", a) else "keine Uhrzeit"),
    ("Witz", "Erzähl mir einen kurzen Witz.", lambda a, t: ""),
    ("Danke", "Danke!", lambda a, t: ""),
]


def ask(key, sentence):
    """(answer, tools that ran) for one sentence in a fresh session, streamed (scripts/bote-schalttest.py)"""
    body = json.dumps({"model": "hermes-agent", "stream": True, "messages": [{"role": "user", "content": sentence}]}).encode()
    req = urllib.request.Request(API + "/chat/completions", data=body, headers={
        "Content-Type": "application/json", "Authorization": "Bearer " + key, "X-Hermes-Session-Id": "bote-probe-" + uuid.uuid4().hex[:12]})
    text, tools, event = [], [], ""
    with urllib.request.urlopen(req, timeout=max(LIMIT * 4, 120)) as f:
        for raw in f:
            line = raw.decode("utf-8", "replace").rstrip("\r\n")
            if line.startswith("event:"):
                event = line[6:].strip()
            elif not line:
                event = ""
            elif line.startswith("data:") and line[5:].strip() != "[DONE]":
                try:
                    obj = json.loads(line[5:])
                except ValueError:
                    continue
                if event == "hermes.tool.progress" and obj.get("status") == "running":
                    tools.append(obj.get("tool", ""))
                elif not event:
                    text += [(c.get("delta") or {}).get("content") or "" for c in obj.get("choices") or []]
    return "".join(text).strip(), tools


def proben(key):
    """the nightly probes: one journal line, the result in proben.json"""
    at = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    failed, secs = [], []
    for name, sentence, check in PROBES:
        t0 = time.monotonic()
        try:
            answer, tools = ask(key, sentence)
        except (OSError, ValueError) as e:
            why = f"HTTP {e.code}" if isinstance(e, urllib.error.HTTPError) else str(e)
            r = {"at": at, "n": 0, "unmeasured": f"Hermes antwortet nicht ({why})"}
            print("Bote-Proben: nicht gemessen, " + r["unmeasured"], flush=True)
            break
        secs.append(time.monotonic() - t0)
        why = ("keine Antwort" if not answer else "Fehlertext" if "⚠" in answer or "OUT-OF-BAND" in answer
               else f"zu langsam ({secs[-1]:.0f} s)" if secs[-1] > LIMIT else check(answer, tools))
        if why:
            failed.append({"name": name, "why": why})
    else:
        r = {"at": at, "n": len(PROBES), "ok": len(PROBES) - len(failed), "median": round(statistics.median(secs), 1),
             "max": round(max(secs), 1), "failed": failed}
        print(f"Bote-Proben: {r['ok']} von {r['n']} · Antwort Median {r['median']} s, längste {r['max']} s"
              + ("".join(f" · {f['name']}: {f['why']}" for f in failed)), flush=True)
    with open(PROBEN + ".tmp", "w") as f:
        json.dump(r, f, ensure_ascii=False)
    os.replace(PROBEN + ".tmp", PROBEN)


def probed():
    """(part, findings) of the last night's probes, or nothing before the first"""
    try:
        with open(PROBEN) as f:
            r = json.load(f)
        when = datetime.datetime.strptime(r["at"], "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=datetime.timezone.utc).astimezone().strftime("%d.%m. %H:%M")
    except (OSError, ValueError, KeyError):
        return "", []
    if not r.get("n"):
        return f"Proben nicht gemessen ({when})", [{"severity": "info", "unmeasured": True, "detail": "Die Proben des Boten: " + r.get("unmeasured", ""),
                                                    "fix": "laib logs bote"}]
    med = str(r["median"]).replace(".", ",")
    return f"Proben {r['ok']} von {r['n']} ({when}), Antwort meist {med} s", [
        {"severity": "gelb", "detail": f"Bote-Probe „{f['name']}“: {f['why']}", "fix": "templates/bote/README.md, „Proben“; laib logs bote"} for f in r.get("failed") or []]


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
    part, found = probed()
    if part:
        parts.append(part)
        findings += found
    status(" · ".join(parts) or "kein Kanal eingeschaltet", findings)


if __name__ == "__main__":
    if os.environ.get("PROBEN") and os.environ.get("BOTE_API_KEY"):
        proben(os.environ["BOTE_API_KEY"])
    main()
