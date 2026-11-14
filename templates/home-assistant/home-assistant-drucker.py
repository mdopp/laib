"""The household's printers in Home Assistant (owner 2026-11-14): „Die gefundenen Drucker auch
automatisch in Home Assistant darstellen“.

Home Assistant's ipp integration finds IPP printers through zeroconf, but a discovery becomes a
config entry only once someone confirms it. This gives every printer the druck service found an
ipp entry, so its state and ink levels are in Home Assistant (and in Homes, which reads the ipp
platform):

- DRUCKER is druck's /data/ha-drucker.json (druck-finden.py `ha_list`): a network printer that
  speaks IPP by its own name, address and path (the direct entry reports its ink); a printer only
  the box reaches (USB, or one without IPP) by the box's queue, 127.0.0.1:631/printers/<queue>.
  The box's re-share of a network printer („… (über die Box)“) is never on that list: no printer
  goes in twice.
- A printer whose uuid an ipp entry has already (also one a human ignored: source „ignore“) or
  whose host and path one has, is left alone. A discovery of it waiting to be confirmed is
  confirmed; otherwise a flow is started with its host, port and path. A printer that does not
  answer leaves no flow behind. Home Assistant aborts a printer it has itself.
- Nothing is ever deleted: an entry whose printer is gone stays, the owner removes it.

It runs inside the Home Assistant container (home-assistant-drucker.service, `podman exec -i …
python3 -`, every 15 minutes) with HA_NAMES_TOKEN, the user „Laib-Namen“'s (an administrator:
config flows are admin-only); the existing entries are read from HA's own .storage. The stdlib only.
"""
import json
import os
import sys
import urllib.error
import urllib.request

STORAGE = os.environ.get("HA_ENTRIES", "/config/.storage/core.config_entries")


def norm(uuid):
    return (uuid or "").lower().removeprefix("urn:uuid:").strip()


def entries_of(storage):
    """the ipp entries of core.config_entries: [{unique_id, source, host, base_path}]"""
    out = []
    for e in (storage.get("data") or {}).get("entries") or []:
        if e.get("domain") == "ipp":
            d = e.get("data") or {}
            out.append({"unique_id": norm(e.get("unique_id")), "source": e.get("source"),
                        "host": (d.get("host") or "").lower(), "base_path": d.get("base_path") or ""})
    return out


def plan(drucker, entries, flows):
    """-> [(action, printer, flow_id)]: confirm a waiting discovery, or start a flow; nothing for one there"""
    have = {e["unique_id"] for e in entries if e["unique_id"]}
    where = {(e["host"], e["base_path"]) for e in entries}
    waiting = {norm((f.get("context") or {}).get("unique_id")): f["flow_id"] for f in flows
               if f.get("handler") == "ipp" and (f.get("context") or {}).get("source") == "zeroconf"}
    out = []
    for p in drucker:
        u = norm(p.get("uuid"))
        if u and u in have or (p["host"].lower(), p["pfad"]) in where:
            continue
        out.append(("bestaetigen", p, waiting[u]) if u and u in waiting else ("anlegen", p, None))
    return out


class HA:
    def __init__(self, base, token):
        self.base, self.token = base.rstrip("/"), token

    def __call__(self, method, path, body=None):
        req = urllib.request.Request(self.base + path, method=method, data=None if body is None else json.dumps(body).encode(),
                                     headers={"Authorization": "Bearer " + self.token, "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                raw = r.read()
        except urllib.error.HTTPError as e:
            if e.code in (401, 403):
                raise PermissionError(f"Home Assistant refused the token ({e.code})") from e
            raise RuntimeError(f"{method} {path}: {e.code}") from e
        return json.loads(raw) if raw else None


def act(ha, action, p, flow):
    if action == "anlegen":
        flow = ha("POST", "/api/config/config_entries/flow", {"handler": "ipp", "show_advanced_options": False})["flow_id"]
        r = ha("POST", f"/api/config/config_entries/flow/{flow}", {"host": p["host"], "port": p["port"], "base_path": p["pfad"],
                                                                  "ssl": bool(p.get("tls")), "verify_ssl": False})
    else:
        r = ha("POST", f"/api/config/config_entries/flow/{flow}", {})
    if r.get("type") == "form":   # the printer did not answer: no flow left waiting
        ha("DELETE", f"/api/config/config_entries/flow/{flow}")
        return f"{p['name']}: nicht erreicht ({(r.get('errors') or {}).get('base', 'Formular')}), beim nächsten Lauf wieder"
    if r.get("type") == "abort" and r.get("reason") == "already_in_progress":
        return f"{p['name']}: wartet in Home Assistant auf eine Bestätigung"
    if r.get("type") == "abort":
        return f"{p['name']}: Home Assistant hat ihn schon ({r.get('reason')})"
    return f"{p['name']}: in Home Assistant eingetragen"


def main(drucker, token, ha, storage):
    if drucker is None:
        print("drucker: no print server (druck) on this box: nothing to add")
        return 0
    if not token:
        print("drucker: no HA_NAMES_TOKEN yet (laib apply home-assistant mints it): no printer added")
        return 0
    try:
        # HA's REST has no list of flows in progress (GET …/flow answers 405, 2026-11-14): a printer
        # whose discovery waits is started anew; HA then aborts with already_in_progress
        todo = plan(drucker, entries_of(storage), [])
        for action, p, flow in todo:
            print("drucker: " + act(ha, action, p, flow))
    except PermissionError as err:
        print(f"drucker: {err}: laib apply home-assistant mints a new one", file=sys.stderr)
        return 1
    except OSError as err:   # Home Assistant restarting: the next run adds them
        print(f"drucker: Home Assistant does not answer ({type(err).__name__}): no change")
        return 0
    except (RuntimeError, KeyError, ValueError) as err:
        print(f"drucker: NOT done: {type(err).__name__}: {err}", file=sys.stderr)
        return 1
    if not todo:
        print(f"drucker: as set ({len(drucker)} printers)")
    return 0


if __name__ == "__main__":
    raw = os.environ.get("DRUCKER", "").strip()
    try:
        with open(STORAGE, encoding="utf-8") as f:
            st = json.load(f)
    except (OSError, ValueError):
        st = {}
    raise SystemExit(main(json.loads(raw) if raw else None, os.environ.get("HA_TOKEN", ""),
                          HA(os.environ.get("HA_URL", "http://127.0.0.1:8123"), os.environ.get("HA_TOKEN", "")), st))
