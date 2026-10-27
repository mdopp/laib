#!/usr/bin/env python3
"""file-share's detail line for the Box page (#785), three measured parts:

- the space left on the share's disk: statvfs of the share, mounted read-only;
- Syncthing: the devices it knows besides the box, and how many are connected
  now, from its REST API with the key from its own config.xml (mounted
  read-only; the key goes into the request header and nowhere else);
- the network drive: of the members samba-members.py lists, how many have set
  their password, from samba-accounts.sh's status file ("<name> <epoch>", 0:
  none). The passwords FIFO beside it is never opened.

A part that cannot be measured says "nicht gemessen" and why; the others stay.
Only reads.

Environment: SHARE_DIR, SYNCTHING_URL, SYNCTHING_CONFIG, ACCOUNTS_DIR, STATUS_DIR.
"""
import datetime, json, os, re, urllib.error, urllib.request

SHARE = os.environ.get("SHARE_DIR", "/share")
URL = os.environ.get("SYNCTHING_URL", "http://127.0.0.1:8384").rstrip("/")
CONFIG = os.environ.get("SYNCTHING_CONFIG", "/syncthing/config.xml")
ACCOUNTS = os.environ.get("ACCOUNTS_DIR", "/samba-accounts")
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def size(b):
    """bytes in the words of a disk's label: 1,2 TB, 420,8 GB"""
    for unit, f in (("TB", 1e12), ("GB", 1e9), ("MB", 1e6)):
        if b >= f or unit == "MB":
            return f"{b / f:.1f}".replace(".", ",") + " " + unit


def space():
    st = os.statvfs(SHARE)
    return f"{size(st.f_bavail * st.f_frsize)} frei von {size(st.f_blocks * st.f_frsize)}"


def syncthing():
    with open(CONFIG) as f:
        m = re.search(r"<apikey>([^<]+)</apikey>", f.read())
    if not m:
        raise ValueError("kein API-Schlüssel in Syncthings config.xml")
    key = m.group(1).strip()

    def get(path):
        with urllib.request.urlopen(urllib.request.Request(URL + path, headers={"X-API-Key": key}), timeout=15) as r:
            return json.load(r)

    me = get("/rest/system/status")["myID"]
    devices = [d["deviceID"] for d in get("/rest/config/devices") if d.get("deviceID") != me]
    conns = get("/rest/system/connections").get("connections") or {}
    up = sum(1 for d in devices if (conns.get(d) or {}).get("connected"))
    if not devices:
        return "Syncthing: noch kein Gerät verbunden"
    return f"Syncthing: {up} von {len(devices)} Gerät{'en' if len(devices) > 1 else ''} verbunden"


def accounts():
    with open(os.path.join(ACCOUNTS, "status")) as f:
        rows = [l.split() for l in f if l.strip()]
    have = sum(1 for r in rows if len(r) == 2 and r[1].isdigit() and int(r[1]) > 0)
    return f"Netzlaufwerk: {have} von {len(rows)} Person{'en' if len(rows) != 1 else ''} mit Passwort"


def main():
    parts, findings = [], []

    def miss(what, e, fix):
        why = f"{what} nicht gemessen ({e})"
        parts.append(why)
        findings.append({"severity": "info", "unmeasured": True, "detail": "Dateifreigabe: " + why, "fix": fix})

    try:
        parts.append(space())
    except OSError as e:
        miss("Speicherplatz", e, "laib inspect file-share")
    try:
        parts.append(syncthing())
    except urllib.error.HTTPError as e:
        miss("Syncthing", f"HTTP {e.code}", "laib logs file-share")
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as e:
        miss("Syncthing", e, "laib inspect file-share")
    try:
        parts.append(accounts())
    except FileNotFoundError:
        pass   # samba-accounts.sh writes it once members exist: nothing to say yet
    except OSError as e:
        miss("Netzlaufwerk", e, "laib logs file-share")
    status(" · ".join(parts), findings)


if __name__ == "__main__":
    main()
