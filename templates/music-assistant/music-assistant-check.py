#!/usr/bin/env python3
"""music-assistant's detail line for the Box page (#785): the speakers Music
Assistant knows, how many play now, and its titles, measured through MA's
command API (POST /api, as home-assistant-check.py's music()) with the token
post-deploy minted (HA_MUSIC_ASSISTANT_TOKEN). Only reads. Without the token,
or with MA refusing or silent, the line is "nicht gemessen" and why.

Environment: MA_URL, MA_TOKEN, STATUS_DIR.
"""
import datetime, json, os, urllib.error, urllib.request

URL = os.environ.get("MA_URL", "http://127.0.0.1:8095").rstrip("/")
TOKEN = os.environ.get("MA_TOKEN", "")
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def unmeasured(why, fix):
    status("nicht gemessen: " + why, [{"severity": "info", "unmeasured": True, "detail": "Music Assistant nicht gemessen: " + why, "fix": fix}])


def n(x):
    return f"{int(x or 0):,}".replace(",", ".")


def cmd(command, **args):
    req = urllib.request.Request(URL + "/api", data=json.dumps({"message_id": "laib-check", "command": command, "args": args}).encode(),
                                 headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"}, method="POST")
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.load(r)


def main():
    if not TOKEN:
        return unmeasured("noch kein Zugang", "laib apply music-assistant: post-deploy legt den Zugang an (#526)")
    try:
        players, tracks = cmd("players/all"), cmd("music/tracks/count")
    except urllib.error.HTTPError as e:
        return unmeasured(f"Music Assistant lässt den Zugang nicht herein (HTTP {e.code})", "laib apply music-assistant (#526)")
    except (OSError, ValueError) as e:
        return unmeasured(f"Music Assistant antwortet nicht ({e})", "laib inspect music-assistant")
    if not isinstance(players, list) or isinstance(tracks, bool) or not isinstance(tracks, int):
        return unmeasured("Music Assistant antwortet unerwartet", "laib logs music-assistant")
    playing = sum(1 for p in players if isinstance(p, dict) and p.get("state") == "playing")
    detail = f"{len(players)} Lautsprecher"
    if playing:
        detail += f", {playing} spiel{'en' if playing > 1 else 't'} gerade"
    status(detail + f" · {n(tracks)} Titel")


if __name__ == "__main__":
    main()
