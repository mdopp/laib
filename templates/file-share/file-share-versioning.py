#!/usr/bin/env python3
"""Syncthing keeps what another device deleted or overwrote for 30 days
(haus#174): every folder without versioning gets Syncthing's simple
versioning, cleanoutDays 30, and so do Syncthing's folder defaults, so a
folder added later in its web page or accepted from a device starts with it.
A folder whose versioning someone set stays as set. Idempotent: a folder
that has versioning is never written, so a second run changes nothing.

Syncthing keeps a version only when a change arrives from another device
(it moves the old file into the folder's .stversions). A file deleted on the
box itself, over Samba or in File Browser, is not kept by it.

Environment: SYNCTHING_URL, SYNCTHING_CONFIG (config.xml, mounted read-only;
the API key goes into the request header and nowhere else).
"""
import json, os, re, sys, urllib.error, urllib.parse, urllib.request

URL = os.environ.get("SYNCTHING_URL", "http://127.0.0.1:8384").rstrip("/")
CONFIG = os.environ.get("SYNCTHING_CONFIG", "/syncthing/config.xml")
DAYS = "30"
SIMPLE = {"type": "simple", "params": {"keep": "5", "cleanoutDays": DAYS}, "cleanupIntervalS": 3600, "fsPath": "", "fsType": "basic"}


def main():
    with open(CONFIG) as f:
        m = re.search(r"<apikey>([^<]+)</apikey>", f.read())
    if not m:
        sys.exit("kein API-Schlüssel in Syncthings config.xml")
    key = m.group(1).strip()

    def call(path, body=None):
        req = urllib.request.Request(URL + path, headers={"X-API-Key": key, "Content-Type": "application/json"},
                                     data=None if body is None else json.dumps(body).encode(), method="GET" if body is None else "PATCH")
        with urllib.request.urlopen(req, timeout=30) as r:
            raw = r.read()
            return json.loads(raw) if raw.strip() else None

    def unversioned(cfg):
        return not ((cfg or {}).get("versioning") or {}).get("type")

    try:
        if unversioned(call("/rest/config/defaults/folder")):
            call("/rest/config/defaults/folder", {"versioning": SIMPLE})
            print(f"neue Ordner: einfache Versionierung, {DAYS} Tage")
        for folder in call("/rest/config/folders") or []:
            if unversioned(folder):
                call("/rest/config/folders/" + urllib.parse.quote(folder["id"], safe=""), {"versioning": SIMPLE})
                print(f"Ordner {folder.get('label') or folder['id']}: einfache Versionierung, {DAYS} Tage")
    except urllib.error.HTTPError as e:
        sys.exit(f"Syncthing antwortet mit HTTP {e.code}")
    except (OSError, ValueError, KeyError, TypeError) as e:
        sys.exit(f"Syncthing nicht erreicht: {e}")


if __name__ == "__main__":
    main()
