#!/usr/bin/env python3
"""Solaris off (#540, Phase 5 of #160; ADR 0015): Stimme takes the voice,
Solaris' units go, its data stays 30 days. Throw-away, like the migration
(ADR 0010). Run on the box (the host, not the Werkstatt) as core:

  python3 -I scripts/solaris-off.py          what is left; changes nothing
  python3 -I scripts/solaris-off.py --do     Solaris off, Stimme on; then what is left

--do, in this order, each step only when it has something to do (a second
run changes nothing):
  1. Solaris' units in the Quadlet root (solaris, solaris-*; a Laib service of
     that name is never touched) stopped, their files moved to a dated park
     dir ~/solaris-off/<date>/; its plain host units (solaris-*.service,
     .timer in ~/.config/systemd/user, e.g. solaris-radicale-rights.timer)
     disabled and moved there too; daemon-reload. The park dir gets
     rollback.sh, the way back.
  2. ignore.yml's lines that name them out, one commit in the state repo as
     the human who runs this (what `laib ignore` added, taken back the way
     `laib secret set` commits secrets.age: no kernel verb, no proposal; an
     agent's hand cannot run this, it needs core's shell on the host).
  3. Stimme's ports (10300 Whisper, 10200 Piper, 10400 openWakeWord) free,
     then `laib add stimme`.
  4. `laib apply home-assistant`: its post-deploy adds Stimme's Wyoming
     entries and repoints an Assist pipeline whose engine is gone (#540).

What stays the owner's step, listed with the reason: Home Assistant entries
that still point at Solaris (deleting an integration takes its entities,
and an automation that names one, with it: a person decides), a :8787 line
in HA's YAML or box.yml, and deleting Solaris' data after 30 days.

Exit codes: 0 nothing of Solaris left; 1 something left or a step red (the
output says what and who acts); 2 could not check (not on the host, the
state or HA's config entries unreadable). Reads HA's config entries from its
.storage file and prints only titles, domains, hosts and ports, never a
token or a password; nothing secret goes on argv.
"""
import datetime, json, os, re, shutil, socket, subprocess, sys, time
from pathlib import Path
from urllib.parse import urlsplit

E = os.environ
HOME = Path(E.get("HOME", "/var/home/core"))
Q = Path(E.get("Q", HOME / ".config/containers/systemd"))
UNITS = Path(E.get("UNITS", HOME / ".config/systemd/user"))
SB_PARK = Path(E.get("SB_PARK", HOME / "servicebay-units"))      # migration-day.sh's PARK
OFF = Path(E.get("OFF_DIR", HOME / "solaris-off"))
DATA = Path(E.get("DATA_DIR", "/mnt/data/stacks"))
LAIB_HOME = Path(E.get("LAIB_HOME", "/mnt/data/laib"))
STATE = Path(E.get("LAIB_STATE", LAIB_HOME / "state"))
LAIB = E.get("LAIB_BIN", "/var/opt/laib/laib")
CONTAINERENV = E.get("CONTAINERENV", "/run/.containerenv")
HA_CONFIG = DATA / "home-assistant" / "homeassistant"
STIMME_HOST = E.get("STIMME_HOST", "127.0.0.1")
STIMME_PORTS = dict(zip(("Whisper", "Piper", "openWakeWord"),   # ADR 0015, templates/stimme
                        (int(p) for p in E.get("STIMME_PORTS", "10300,10200,10400").split(","))))
SOLARIS_PORTS = {8787: "Solaris' facade (:8787)", 10203: "Solaris' speech (:10203)", 10700: "Solaris' Wyoming satellite (:10700)"}
TITLE = re.compile(r"\bsol(aris|ilos)?\b|openai[-_ ]streaming", re.I)
KEEP_DAYS = 30
QUADLET_EXT = (".kube", ".yml", ".yaml", ".container", ".pod", ".network", ".volume")
HOST_EXT = (".timer", ".path", ".socket", ".service")   # timers first: they start the services


def say(msg=""):
    print(msg, flush=True)


def is_solaris(name):
    base = name.split(".", 1)[0]
    return base == "solaris" or base.startswith("solaris-")


def managed():
    """the files Laib rendered (managed.json): never Solaris', never touched"""
    try:
        return set(json.loads((LAIB_HOME / "managed.json").read_text()) or [])
    except (OSError, ValueError):
        return set()


def quadlet_left():
    """Solaris' entries in the Quadlet root that no Laib service claims"""
    if not Q.is_dir():
        return []
    mine = managed()
    return sorted(p for p in Q.iterdir() if is_solaris(p.name) and not (STATE / "services" / p.name.split(".", 1)[0]).is_dir()
                  and str(p) not in mine)


def host_left():
    """Solaris' plain host units in ~/.config/systemd/user (not the .wants links: disable removes those)"""
    if not UNITS.is_dir():
        return []
    mine = managed()
    out = [p for p in UNITS.iterdir() if p.is_file() and is_solaris(p.name) and p.suffix in HOST_EXT and str(p) not in mine]
    return sorted(out, key=lambda p: (HOST_EXT.index(p.suffix), p.name))


def ignore_lines():
    """ignore.yml's lines that name a Solaris unit; None when ignore.yml is unreadable"""
    p = STATE / "ignore.yml"
    if not p.exists():
        return []
    try:
        lines = p.read_text().splitlines()
    except OSError:
        return None
    return [l for l in lines if (m := re.match(r"^\s*-\s*[\"']?([^\s\"'#]+)", l)) and is_solaris(m.group(1))]


def answers(port):
    try:
        socket.create_connection((STIMME_HOST, port), timeout=2).close()
        return True
    except OSError:
        return False


def lan_ip():
    try:
        m = re.search(r"(?m)^lanIp:\s*[\"']?([0-9a-fA-F.:]+)", (STATE / "box.yml").read_text())
        return m.group(1) if m else ""
    except OSError:
        return ""


def where(data):
    """(host, port, shown) of an entry's server: its host/port fields or the first URL in its data"""
    host, port = data.get("host"), data.get("port")
    if host and port:
        try:
            return str(host), int(port), f"{host}:{port}"
        except (TypeError, ValueError):
            pass
    for v in data.values():
        if isinstance(v, str) and re.match(r"https?://", v):
            u = urlsplit(v)
            try:
                p = u.port or (443 if u.scheme == "https" else 80)
            except ValueError:
                continue
            return u.hostname or "", p, f"{u.scheme}://{u.hostname}:{p}{u.path}"   # no user, no password, no query
    return "", 0, ""


def solaris_entries(entries, lan=""):
    """HA's config entries that still point at Solaris: [(title, domain, where, why)]. Stimme's ports
    (Wyoming 10300, 10200, 10400 on this box) are Stimme's now, whatever the entry's title says."""
    local = {"127.0.0.1", "localhost", "::1", "0.0.0.0", "host.containers.internal"} | ({lan} if lan else set())
    out = []
    for e in entries:
        d = e.get("data") if isinstance(e.get("data"), dict) else {}
        title, domain = str(e.get("title") or ""), str(e.get("domain") or "")
        host, port, shown = where(d)
        why = []
        if host in local and port in SOLARIS_PORTS:
            why.append(SOLARIS_PORTS[port] + ", gone")
        elif domain == "wyoming" and host in local and port not in STIMME_PORTS.values():
            why.append(f"Wyoming on :{port}, which nobody serves (Stimme's: {', '.join(map(str, STIMME_PORTS.values()))})")
        if TITLE.search(title) or TITLE.search(domain) or "solaris" in domain or "solilos" in domain:
            why.append("Solaris' own integration")
        if why:
            out.append((title, domain, shown, "; ".join(why)))
    return out


def ha_entries():
    """HA's config entries from its storage, or None when unreadable"""
    try:
        return json.loads((HA_CONFIG / ".storage" / "core.config_entries").read_text())["data"]["entries"]
    except (OSError, ValueError, KeyError, TypeError):
        return None


def yaml_8787():
    """lines in HA's YAML and in box.yml that name :8787 (Solaris' notify, the old notify.url): file:line"""
    files = [HA_CONFIG / "configuration.yaml", *sorted((HA_CONFIG / "packages").glob("*.yaml")), STATE / "box.yml"]
    out = []
    for f in files:
        try:
            for n, line in enumerate(f.read_text(errors="replace").splitlines(), 1):
                if re.search(r"(127\.0\.0\.1|localhost|host\.containers\.internal):8787\b", line) and not line.lstrip().startswith("#"):
                    out.append(f"{f}:{n}")
        except OSError:
            continue
    return out


def park_dirs():
    return sorted(p for p in OFF.glob("20??-??-??") if p.is_dir()) if OFF.is_dir() else []


def status():
    """what is left of Solaris; (left, unknown)"""
    left, unknown = 0, 0
    say("=== what is left of Solaris (#540)")
    q = quadlet_left()
    for p in q:
        say(f"  left   {p}: in the Quadlet root (--do stops and parks it)")
    h = host_left()
    for p in h:
        say(f"  left   {p}: a host unit (--do disables and parks it)")
    lines = ignore_lines()
    if lines is None:
        say(f"  ?      {STATE / 'ignore.yml'} is not readable")
        unknown += 1
    for l in lines or []:
        say(f"  left   ignore.yml: {l.strip()} (--do takes the line out)")
    left += len(q) + len(h) + len(lines or [])
    parked = sorted(p.name for p in SB_PARK.glob("solaris*")) if SB_PARK.is_dir() else []
    if parked:
        say(f"  info   parked on migration day in {SB_PARK} (migration-day.sh's rollback): {', '.join(parked)}")
    if not (STATE / "services").is_dir():
        say(f"  ?      no state at {STATE}")
        unknown += 1
    elif not (STATE / "services" / "stimme").is_dir():
        say("  left   Stimme is not Laib's (--do: laib add stimme)")
        left += 1
    else:
        quiet = [f"{n} :{p}" for n, p in STIMME_PORTS.items() if not answers(p)]
        say("  ok     Stimme is Laib's" + (f"; not answering: {', '.join(quiet)} (laib status stimme)" if quiet else ""))
        left += bool(quiet)
    if (STATE / "services" / "home-assistant").is_dir():
        entries = ha_entries()
        if entries is None:
            say(f"  ?      Home Assistant's config entries are not readable ({HA_CONFIG / '.storage/core.config_entries'})")
            unknown += 1
        else:
            for title, domain, at, why in solaris_entries(entries, lan_ip()):
                say(f"  left   Home Assistant: \"{title}\" ({domain}{', ' + at if at else ''}): {why}; the owner deletes it in HA")
                left += 1
    for ref in yaml_8787():
        say(f"  left   {ref}: names :8787, Solaris' port; the owner edits it")
        left += 1
    data = sorted(p for p in DATA.glob("solaris*") if p.is_dir()) if DATA.is_dir() else []
    days = park_dirs()
    if data:
        since = datetime.date.fromisoformat(days[0].name) if days else None
        when = f"from {since + datetime.timedelta(days=KEEP_DAYS)}" if since else f"{KEEP_DAYS} days after --do"
        say(f"  info   Solaris' data stays; deleting it is the owner's step, {when}:")
        for p in data:
            say(f"           podman unshare rm -rf {p}")
    for d in days:
        if (d / "rollback.sh").exists():
            say(f"  info   rollback (within the {KEEP_DAYS} days): bash {d / 'rollback.sh'}")
    return left, unknown


def run(*argv, check=False):
    r = subprocess.run(list(argv), capture_output=True, text=True)
    if check and r.returncode:
        raise RuntimeError(f"{' '.join(argv[:4])}: {(r.stderr or r.stdout).strip()[-400:]}")
    return r


def laib(*args):
    """a laib verb, streamed; the apply lock (rc 4, the converge timer) is waited for"""
    for _ in range(60):
        rc = subprocess.run([LAIB, *args]).returncode
        if rc != 4:
            return rc
        say(f"laib {args[0]}: the lock is busy, trying again")
        time.sleep(float(E.get("LAIB_RETRY_SLEEP", "3")))
    return 4


def units_off():
    q, h = quadlet_left(), host_left()
    if not q and not h:
        say("1. Solaris' units: none left")
        return
    park = OFF / datetime.date.today().isoformat()
    (park / "host").mkdir(parents=True, exist_ok=True)
    back_q, back_h, start, enable = [], [], [], []
    for p in q:   # a .kube, .container or .pod is a unit: stopped first, while systemd still knows it
        unit = {".kube": p.stem + ".service", ".container": p.stem + ".service", ".pod": p.stem + "-pod.service"}.get(p.suffix)
        if not unit:
            continue
        if run("systemctl", "--user", "is-active", "--quiet", unit).returncode == 0:
            start.append(unit)
        run("systemctl", "--user", "stop", unit)
    for p in h:
        if run("systemctl", "--user", "is-enabled", "--quiet", p.name).returncode == 0:
            enable.append(p.name)
        run("systemctl", "--user", "disable", "--now", p.name)
    for p in q:
        shutil.move(str(p), str(park / p.name))
        back_q.append(p.name)
    for p in h:
        shutil.move(str(p), str(park / "host" / p.name))
        back_h.append(p.name)
    run("systemctl", "--user", "daemon-reload", check=True)
    rb = park / "rollback.sh"
    old = rb.read_text() if rb.exists() else ""
    lines = ["#!/bin/bash", f"# solaris-off.py (#540): Solaris back as it ran, within {KEEP_DAYS} days. Stimme first: it holds the ports.",
             "set -e", f"{LAIB} remove stimme || true"]
    lines += [f"mv {park / n} {Q}/" for n in back_q] + [f"mv {park / 'host' / n} {UNITS}/" for n in back_h]
    lines += ["systemctl --user daemon-reload"] + [f"systemctl --user enable --now {u}" for u in enable]
    lines += [f"systemctl --user start {u}" for u in start]
    lines += [f"{LAIB} ignore {n}" for n in back_q if n.endswith(QUADLET_EXT)]
    rb.write_text((old + "\n" if old else "") + "\n".join(lines) + "\n")
    rb.chmod(0o755)
    say(f"1. Solaris' units off and parked in {park}: {', '.join(back_q + back_h)}")
    say(f"   rollback: bash {rb}")


def ignore_off():
    lines = ignore_lines()
    if lines is None:
        raise RuntimeError(f"{STATE / 'ignore.yml'} is not readable")
    if not lines:
        say("2. ignore.yml: names no Solaris unit")
        return
    if quadlet_left():
        raise RuntimeError("Solaris' units are still in the Quadlet root: ignore.yml keeps its lines (laib plan would call them unmanaged)")
    p = STATE / "ignore.yml"
    keep = [l for l in p.read_text().splitlines() if l not in lines]
    tmp = p.with_suffix(".yml.tmp")
    tmp.write_text("\n".join(keep) + "\n")
    os.replace(tmp, p)
    git = lambda *a, check=True: run("git", "-C", str(STATE), *a, check=check)
    if git("diff", "--quiet", "HEAD", "--", "ignore.yml", check=False).returncode:
        actor = E.get("LAIB_ACTOR") or "human:" + (E.get("USER") or "core")
        git("-c", "user.name=laib", "-c", "user.email=laib@localhost", "commit", "-q", "-m",
            "solaris off (#540): ignore.yml no longer names Solaris' units", "--author", f"{actor} <{actor}@laib>", "--", "ignore.yml")
    say(f"2. ignore.yml: {', '.join(l.strip().lstrip('- ') for l in lines)} out (a commit in the state repo)")


def stimme_on():
    if (STATE / "services" / "stimme").is_dir():
        say("3. Stimme is Laib's already")
    else:
        busy = [f"{n} :{p}" for n, p in STIMME_PORTS.items() if answers(p)]
        if busy:
            raise RuntimeError(f"Stimme's ports still answer ({', '.join(busy)}): something else serves them; "
                               "ss -ltnp says what. No laib add stimme")
        say("3. laib add stimme")
        if laib("add", "stimme"):
            raise RuntimeError("laib add stimme is red: laib status stimme, laib logs stimme")
    if (STATE / "services" / "home-assistant").is_dir():
        say("4. laib apply home-assistant: Stimme's Wyoming entries, the Assist pipelines")
        if laib("apply", "home-assistant"):
            raise RuntimeError("laib apply home-assistant is red: laib logs home-assistant")
    else:
        say("4. no Home Assistant in the state: nothing to wire")


def main(argv):
    if argv not in ([], ["--do"]):
        say(__doc__.split("\n\n")[1])
        return 2
    if os.path.exists(CONTAINERENV):
        say("solaris-off: runs on the host as core, not in a container (the Werkstatt): nothing checked")
        return 2
    red = False
    actor = E.get("LAIB_ACTOR") or "human:" + (E.get("USER") or "core")
    if argv == ["--do"] and not actor.startswith("human:"):
        say(f"solaris-off --do: only a human takes Solaris off (LAIB_ACTOR is {actor}); nothing changed")
        return 1
    if argv == ["--do"]:
        try:
            units_off()
            ignore_off()
            stimme_on()
        except (RuntimeError, OSError) as e:
            say(f"RED: {e}")
            red = True
        say()
    left, unknown = status()
    if red or left:
        say(f"\nsolaris-off: {'a step is red' if red else f'{left} left'}; what and who acts is above")
        return 1
    if unknown:
        say("\nsolaris-off: not everything could be checked (?), see above")
        return 2
    say("\nsolaris-off: nothing of Solaris is left; the voice is Stimme's")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
