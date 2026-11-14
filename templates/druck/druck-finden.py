#!/usr/bin/env python3
"""Druck: finds the household's printers and offers each to every device (owner 2026-11-14).

Runs in the druck container beside cupsd and avahi-daemon, every RUNDE seconds. What it does
is all CUPS' and avahi's own discovery; this script only applies one rule to what they found:

- **Found:** `lpinfo -l -v` (CUPS' dnssd backend and cups-filters' driverless backend: every
  printer announced as _ipp, _ipps, _pdl-datastream or _printer, by its DNS-SD name; the usb
  backend: every printer at the box's USB; each with its IEEE 1284 device ID), and `avahi-browse -rtpk` for the TXT records
  of the IPP ones (what they accept: `pdl`, `URF`, `UUID`, `rp`). A network printer is one
  DNS-SD instance name, whichever of its services answers; its queue's URI is CUPS' dnssd://
  name, resolved at every job, so a new address after a DHCP change or a printer that sleeps
  and comes back is the same queue.
- **Driver:** CUPS' own matching, `lpinfo --device-id "MFG:…;MDL:…;" -m`, for the printers only;
  no match is an empty list, not a failed round. A printer that takes PDF
  or PWG raster gets the driverless queue (cups-filters' `driverless:` entry, else
  `everywhere`); any other one the vendor driver CUPS matches, in the order escpr (Epson),
  hpcups (HP), gutenprint, then the rest; none of those and URF: driverless over URF; nothing:
  no queue, and the status says so. No model, no guess.
- **Shared or not:** a network printer that already speaks to every device by itself is not
  offered again: it announces URF (AirPrint: iPhone, iPad, Mac) and PDF or PWG raster (Android's
  built-in print service, Windows, Linux). Its queue stays the box's own (Homes, Paperless),
  unshared. Every other printer is shared as „<Name> (über die Box)“: a USB printer has no
  other way into the home network, and one like the Epson WF-3540 (URF and JPEG only) gains
  PDF for Android.
- **Own queues** are never found again as printers: a name with SUFFIX in it is skipped (CUPS
  announces a shared queue under its description, maybe with „ @ <host>“ after it).
- **Gone:** a queue whose printer was not seen for VERGESSEN_TAGE days is removed; until then
  it stays, and a job waits for the printer (printer-error-policy retry-job).

It writes the status file laib reads (`/data/laib-status.json`) and, for Home Assistant's
ipp entries, `/data/ha-drucker.json` (home-assistant-drucker.py): each network printer that
speaks IPP by its own name, each printer only the box reaches by the box's queue.

A printer at the USB that sysfs shows but whose node the container lacks (plugged in after
the start) ends the run with exit 3, once per set of devices: Restart= brings the container
back with the node, as the scan service does (#801).
"""
import datetime
import json
import os
import re
import subprocess
import sys
import time
import urllib.parse

SUFFIX = " (über die Box)"
DATA = os.environ.get("DRUCK_DATA", "/data")
RUNDE = int(os.environ.get("DRUCK_RUNDE", "120"))
VERGESSEN_TAGE = int(os.environ.get("DRUCK_VERGESSEN_TAGE") or "30")
TYPES = ("_ipp._tcp", "_ipps._tcp", "_pdl-datastream._tcp", "_printer._tcp")   # preferred first
VENDOR = ("escpr", "hpcups", "hplip", "gutenprint")                            # a vendor driver's rank
MAKERS = {"EPSON": "Epson", "HEWLETT-PACKARD": "HP", "HP": "HP", "BROTHER": "Brother", "CANON": "Canon",
          "KYOCERA": "Kyocera", "SAMSUNG": "Samsung", "XEROX": "Xerox", "LEXMARK": "Lexmark", "RICOH": "Ricoh"}


# --- what CUPS and avahi say ---------------------------------------------------------------

def parse_devices(text):
    """lpinfo -l -v: [{uri, class, info, make-and-model, device-id}]"""
    out, cur = [], None
    for line in text.splitlines():
        m = re.match(r"^Device: uri = (\S+)", line)
        if m:
            cur = {"uri": m.group(1)}
            out.append(cur)
        elif cur is not None and "=" in line:
            k, _, v = line.strip().partition("=")
            cur[k.strip()] = v.strip()
    return out


def unescape(s):
    """avahi-browse -p escapes a name's dots, backslashes and bytes as \\. and \\DDD"""
    return re.sub(rb"\\(\d{3}|.)", lambda m: bytes([int(m.group(1))]) if m.group(1).isdigit() else m.group(1),
                  s.encode()).decode("utf-8", "replace")


def parse_avahi(text):
    """avahi-browse -rtpk: {(name, type): {host, address, port, txt}}, IPv4 before IPv6"""
    out = {}
    for line in text.splitlines():
        f = line.split(";", 9)
        if len(f) < 10 or f[0] != "=":
            continue
        key = (unescape(f[3]), f[4])
        if key in out and f[2] != "IPv4":
            continue
        txt = {}
        for kv in re.findall(r'"((?:[^"\\]|\\.)*)"', f[9]):
            k, _, v = unescape(kv).partition("=")
            txt[k.lower()] = v
        out[key] = {"host": f[6], "address": f[7], "port": int(f[8] or 0), "txt": txt}
    return out


def parse_models(text):
    """lpinfo -m: [(ppd-name, make-and-model)]"""
    return [tuple(line.split(" ", 1)) if " " in line else (line, "") for line in text.splitlines() if line.strip()]


def dnssd_name(uri):
    """dnssd://EPSON%20WF-3540%20Series._ipp._tcp.local/?uuid=… -> (instance name, type)"""
    host = urllib.parse.unquote(urllib.parse.urlsplit(uri).netloc)
    for t in TYPES:
        if t + ".local" in host:
            return host.split("." + t, 1)[0], t
    return None, None


# --- the rule ------------------------------------------------------------------------------

def formats(txt):
    pdl = {p.strip().lower() for p in txt.get("pdl", "").split(",") if p.strip()}
    urf = txt.get("urf", "").strip().lower() not in ("", "none")
    return pdl, urf


def complete(txt):
    """reaches every device by itself: AirPrint's URF, and PDF or PWG raster for Android and Windows"""
    pdl, urf = formats(txt)
    return urf and bool(pdl & {"application/pdf", "image/pwg-raster"})


def driver(models, txt, network):
    """CUPS' own matches for the printer's device ID -> (ppd, kind), or (None, why)"""
    pdl, urf = formats(txt)
    driverless = [m for m, _ in models if m.startswith("driverless:")]
    if network and pdl & {"application/pdf", "image/pwg-raster"}:
        return (driverless[0] if driverless else "everywhere"), "driverless"
    vendor = [(m, mm) for m, mm in models if not m.startswith("driverless") and m not in ("raw", "everywhere")
              and "generic" not in mm.lower()]
    rank = lambda m: next((i for i, v in enumerate(VENDOR) if v in m[0].lower() or v in m[1].lower()), len(VENDOR))
    vendor.sort(key=rank)   # stable: CUPS' order within a rank
    if vendor:
        return vendor[0][0], "hersteller"
    if network and urf:
        return (driverless[0] if driverless else "everywhere"), "driverless"
    return None, "kein passender Treiber im Druckserver"


def readable(dev, txt):
    """'EPSON WF-3540 Series' -> 'Epson WF-3540'"""
    name = (txt.get("ty") or dev.get("make-and-model") or dev.get("info") or "Drucker").strip()
    if name.lower() == "unknown":
        name = dev.get("info") or "Drucker"
    name = re.sub(r"\s+series$", "", name, flags=re.I).strip()
    first, _, rest = name.partition(" ")
    first = MAKERS.get(first.upper(), first)
    return (first + " " + rest).strip()


def chosen(devices, own=()):
    """the printers among lpinfo -v's devices, one device each: {key: (device, art)}. A network
    printer is a DNS-SD instance, as CUPS' dnssd backend names it (dnssd://…) or cups-filters'
    driverless backend (ipp://EPSON._ipp._tcp.local/), IPP first; an address (snmp's lpd://…) or a
    bare backend (ipp, beh, cups-brf) is none. Our own shared queues are left out."""
    found, rank = {}, {}
    for d in devices:
        uri = d["uri"]
        if uri.startswith("usb://"):
            key, art, r = "usb:" + uri, "usb", 0
        elif uri.split("://")[0] in ("dnssd", "ipp", "ipps"):
            name, t = dnssd_name(uri)
            if not name or SUFFIX in name or name in own:   # our own shared queue, maybe as „… @ drucker“
                continue
            key, art, r = "netz:" + name, "netz", TYPES.index(t)
        else:
            continue
        if key not in found or r < rank[key]:
            found[key], rank[key] = (d, art), r
    return found


def match_id(d):
    """the device ID CUPS matches PPDs by: MFG and MDL only; a CMD the printer's DNS-SD record
    made up (AppleRaster,URF) would rule out the vendor's PPD"""
    f = dict(p.split(":", 1) for p in d.get("device-id", "").split(";") if ":" in p)
    return f"MFG:{f['MFG']};MDL:{f['MDL']};" if f.get("MFG") and f.get("MDL") else ""


def plan(devices, avahi, models, own=()):
    """devices: lpinfo -l -v; avahi: parse_avahi; models: {match_id: lpinfo -m}; own: our queues'
    names as announced. -> [printer], each {key, name, art, uri, model, kind, shared, ha} or with why"""
    found = chosen(devices, own)
    order = list(found)
    out = []
    for key in order:
        d, art = found[key]
        txt = {}
        if art == "netz":   # the TXT of an IPP service, whichever service the queue uses
            name = key[5:]
            txt = next((avahi[(name, t)]["txt"] for t in TYPES[:2] if (name, t) in avahi), {})
        model, kind = driver(models.get(match_id(d), []), txt, art == "netz")
        p = {"key": key, "name": readable(d, txt), "art": art, "uri": d["uri"]}
        if model is None:
            p["why"] = kind
            out.append(p)
            continue
        shared = art == "usb" or not complete(txt)
        p.update(model=model, kind=kind, shared=shared)
        ipp = next((avahi[(key[5:], t)] | {"tls": t == "_ipps._tcp"} for t in TYPES[:2] if art == "netz" and (key[5:], t) in avahi), None)
        if ipp:   # Home Assistant asks the printer itself: the ink levels are the printer's
            rp = ipp["txt"].get("rp", "ipp/print")
            p["ha"] = {"host": ipp["host"], "port": ipp["port"], "pfad": "/" + rp.lstrip("/"), "tls": ipp["tls"],
                       "uuid": ipp["txt"].get("uuid", "")}
        out.append(p)
    return out


def queue_names(printers, known):
    """a stable CUPS name per printer: the one it had, else its readable name as a word"""
    taken = {v["queue"] for v in known.values()}
    for p in printers:
        if "model" not in p:
            continue
        if p["key"] in known:
            p["queue"] = known[p["key"]]["queue"]
            continue
        base = re.sub(r"[^A-Za-z0-9_.-]+", "_", p["name"]).strip("_") or "Drucker"
        if p["art"] == "usb":
            base += "_USB"
        q, n = base, 2
        while q in taken:
            q, n = f"{base}_{n}", n + 1
        taken.add(q)
        p["queue"] = q
    return printers


def info(p):
    return p["name"] + (SUFFIX if p["shared"] else "")


def ha_list(printers, uuids):
    """the printers Home Assistant gets an ipp entry for: by their own name, else the box's queue"""
    out = []
    for p in printers:
        if "model" not in p:
            continue
        if p["art"] == "netz" and "ha" in p:
            out.append({"name": p["name"], **p["ha"]})
        elif p["shared"]:   # only the box reaches it: a USB printer, a network one without IPP
            out.append({"name": info(p), "host": "127.0.0.1", "port": 631, "pfad": "/printers/" + p["queue"],
                        "tls": False, "uuid": uuids.get(p["queue"], "")})
    return out


def forget(known, seen, now):
    """the keys whose printer was not seen for VERGESSEN_TAGE days"""
    return [k for k, v in known.items() if k not in seen and now - v.get("gesehen", now) > VERGESSEN_TAGE * 86400]


def detail(printers):
    q = [p for p in printers if "model" in p]
    shared = [p["name"] for p in q if p["shared"]]
    if not printers:
        return "Noch kein Drucker im Heimnetz oder am USB der Box gefunden."
    s = f"{len(q)} Drucker eingerichtet" if len(q) != 1 else "1 Drucker eingerichtet"
    return s + (", über die Box angeboten: " + ", ".join(shared) if shared else ", alle drucken schon selbst für jedes Gerät") + "."


# --- the box -------------------------------------------------------------------------------

def run(*cmd, timeout=60, none=None):
    """a command's output; `none`: the answer that means nothing found (lpinfo's client-error-not-found)"""
    r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    if r.returncode and none and none in (r.stderr + r.stdout):
        return ""
    if r.returncode:
        raise RuntimeError(f"{cmd[0]}: {(r.stderr or r.stdout).strip()[:300]}")
    return r.stdout


def write(name, obj):
    path = os.path.join(DATA, name)
    with open(path + ".tmp", "w") as f:
        json.dump(obj, f, ensure_ascii=False, indent=1)
    os.replace(path + ".tmp", path)


def status(det, findings=(), **more):
    write("laib-status.json", {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
                               "detail": det, "findings": list(findings), **more})


def read_json(name, default):
    try:
        with open(os.path.join(DATA, name)) as f:
            return json.load(f)
    except (OSError, ValueError):
        return default


def printer_uuids():
    """our queues' printer-uuid, from cupsd's own printers.conf"""
    out, q = {}, None
    try:
        with open(os.path.join(DATA, "cups", "printers.conf")) as f:
            for line in f:
                m = re.match(r"<(?:Default)?Printer (\S+)>", line)
                q = m.group(1) if m else q
                if q and line.startswith("UUID "):
                    out[q] = line.split()[1].removeprefix("urn:uuid:")
    except OSError:
        pass
    return out


def usb_missing():
    """the printers (interface class 07) sysfs shows whose node /dev/bus/usb lacks"""
    out = []
    base = "/sys/bus/usb/devices"
    for d in sorted(os.listdir(base)) if os.path.isdir(base) else []:
        p = os.path.join(base, d)
        try:
            ifaces = [i for i in os.listdir(p) if i.startswith(d + ":")]
            if not any(open(os.path.join(p, i, "bInterfaceClass")).read().strip() == "07" for i in ifaces):
                continue
            node = "/dev/bus/usb/%03d/%03d" % (int(open(p + "/busnum").read()), int(open(p + "/devnum").read()))
        except (OSError, ValueError):
            continue
        if not os.path.exists(node):
            out.append(node)
    return out


def apply(printers, known, now):
    try:
        have = set(re.findall(r"^device for (\S+):", run("lpstat", "-v"), re.M))
    except RuntimeError:   # no queue yet: lpstat says so with exit 1
        have = set()
    findings = []
    for p in printers:
        if "model" not in p:
            findings.append({"severity": "gelb", "detail": f"{p['name']}: {p['why']}; er wird nicht angeboten.",
                             "fix": "laib logs druck"})
            continue
        want = {"queue": p["queue"], "uri": p["uri"], "model": p["model"], "shared": p["shared"], "info": info(p)}
        old = known.get(p["key"], {})
        if {k: old.get(k) for k in want} != want or p["queue"] not in have:
            try:
                run("lpadmin", "-p", p["queue"], "-v", p["uri"], "-m", p["model"], "-D", info(p), "-E",
                    "-o", "printer-is-shared=" + ("true" if p["shared"] else "false"),
                    "-o", "printer-error-policy=retry-job", timeout=120)
                run("cupsenable", p["queue"])
                run("cupsaccept", p["queue"])
            except (RuntimeError, subprocess.TimeoutExpired) as e:
                findings.append({"severity": "gelb", "detail": f"{p['name']} ließ sich nicht einrichten.",
                                 "einzelheiten": str(e), "fix": "laib logs druck"})
                continue
        known[p["key"]] = want | {"gesehen": now}
    for k in forget(known, {p["key"] for p in printers}, now):
        try:
            run("lpadmin", "-x", known[k]["queue"])
        except RuntimeError:
            pass
        known.pop(k)
    return findings


def runde():
    now = time.time()
    known = read_json("bekannt.json", {})
    try:
        devices = parse_devices(run("lpinfo", "-l", "-v", "--timeout", "15", timeout=60))
        avahi = {}
        for t in TYPES[:2]:
            avahi |= parse_avahi(run("avahi-browse", "-rtpk", t, timeout=30))
        models = {}
        for d, _ in chosen(devices).values():   # only the printers: no PPD lookup for CUPS' own backends
            i = match_id(d)
            if i and i not in models:
                models[i] = parse_models(run("lpinfo", "--device-id", i, "-m", timeout=60, none="not-found"))
    except (RuntimeError, subprocess.TimeoutExpired, OSError) as e:
        return status("nicht gemessen: die Suche nach Druckern lief nicht durch", [{
            "severity": "info", "unmeasured": True, "detail": "Die Druckersuche lief nicht durch.",
            "einzelheiten": str(e), "fix": "laib logs druck"}], warteschlangen=None)
    own = {v["info"] for v in known.values()}
    printers = queue_names(plan(devices, avahi, models, own), known)
    findings = apply(printers, known, now)
    write("bekannt.json", known)
    write("ha-drucker.json", ha_list(printers, printer_uuids()))
    status(detail(printers), findings, warteschlangen=len(known),
           geteilt=sorted(v["info"] for v in known.values() if v["shared"]))


def main():
    restarted = read_json("usb-neustart.json", [])
    while True:
        runde()
        missing = usb_missing()
        if missing and missing != restarted:
            write("usb-neustart.json", missing)
            print("neu eingesteckt: " + ", ".join(missing) + "; der Druckserver startet mit dem Gerät neu", flush=True)
            sys.exit(3)
        time.sleep(RUNDE)


if __name__ == "__main__":
    main()
