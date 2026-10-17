#!/usr/bin/env python3
"""`werkstatt-autoloop` — pi works labelled tickets from inside the Werkstatt (docs/DESIGN.md).

Ported from solarisbay's autoloop (solarisbay#1398 B) with one addition,
the router (issue #8): a ticket pi could not finish gets the label named by
PI_AUTOLOOP_ESCALATE_LABEL (`needs-claude`), so the claude hand picks it up;
pi never tries the same ticket twice. The model comes from MODEL_URL (the
llama role's policy proxy, ADR 0011), whose /v1/models marks allowed presets.

Original docstring follows.

Division of labour, decided by the operator: **Claude cuts the tickets, Pi builds
them.** The loop only ever touches an issue that already carries an agreed label
(`pi:ready` by default), in a repository named in `PI_AUTOLOOP_REPOS`. No label,
no work — Qwen does not get to decide what counts as a ticket.

Four things carry the design:

  THE LOCK IS A GIT REF, NOT A LABEL. The Claude side of this loop claims issues
    with `refs/autoloop/claim/<issue>` created on `origin/main`'s tip, because
    `POST /git/refs` is the one primitive GitHub gives that is a genuine atomic
    create-if-not-exists: the loser gets HTTP 422 "Reference already exists"
    (mdopp/servicebay#2639, #2646). This loop takes the SAME ref, so a Python
    loop in a container and a TypeScript loop in a checkout cannot both grab the
    same ticket. Anything other than a clean create — a rival claim or a
    transport error — counts as "not mine" and this loop does not build: a claim
    you cannot prove you hold is not yours.

  THE LIMITS LIVE HERE, NOT IN THE PROMPT. A model cannot be asked to respect a
    time cap or to stop after a red gate. The cap is a subprocess timeout, the
    red gate ends the run instead of starting a repair loop, and the set of
    repositories is a list this file iterates — none of it is phrased as an
    instruction Pi could talk itself out of.

  IT NEVER MERGES. The first cut opens a pull request and stops; a human or the
    Claude side decides. A run that produced no change, or that hit the cap,
    opens a draft rather than nothing, because a protocol nobody can find is the
    same as no protocol.

  IT TAKES THE CARD FOR ITS RUN. A coding run wants a preset the household
    window does not allow, for up to an hour. So it takes the GPU lease under
    its own holder (`werkstatt`), the way laib's lease contract asks of every
    long-running consumer (laib ADR 0011, amendment 2026-10-14): POST the
    preset, renew every `renew_after`, DELETE at the end, and on start give
    back a window an earlier run left behind. A window somebody else holds is
    a plain note and no run: the ticket keeps its label and no attempt is
    counted. A lease that cannot be had for other reasons never stops the
    work; the proxy then takes the window itself, as for any request.

The GitHub token is the Werkstatt's (`WERKSTATT_GIT_TOKEN`), which the
entrypoint writes to a 0600 credential store on a tmpfs. Git
picks it up through the credential helper and this file reads the same store for
its REST calls, so the token reaches neither this process's argv nor Pi's
environment. It needs Issues *read*, Pull requests *write* and Contents *write*
(the claim ref and the branch push are both Contents).
"""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

DEFAULT_CREDENTIAL_STORE = "/run/laib/git-credentials"
# on the workspace volume: the attempt counter lives here and must survive a restart
DEFAULT_PROTOCOL_DIR = "/workspace/autoloop/protocol"
DEFAULT_WORKROOT = "/workspace/autoloop"
DEFAULT_API = "https://api.github.com"
DEFAULT_LABEL = "pi:ready"
DEFAULT_REPOS = "mdopp/solarisbay"
DEFAULT_INTERVAL_S = 300
DEFAULT_TIME_CAP_S = 3600

CLAIM_REF_PREFIX = "refs/autoloop/claim"
BRANCH_PREFIX = "pi"

# The provider id the pi extension registers (werkstatt/extensions/catalog.mjs,
# defined there once; the test checks both agree). `--model <provider>/<id>`
# pins a run to one preset of the router.
PROVIDER_ID = os.environ.get("PI_PROVIDER_ID") or "laib-llama"

# A transient failure — no clone, the preset refused (409), pi not startable —
# keeps the label and is tried again on later passes; after this many
# attempts the ticket goes to Claude with the protocol (#44).
TRANSIENT_ATTEMPTS = 3

# The preset this loop works in. It is a coding session, so it asks for the
# coding preset — `/v1/models` is a catalogue of everything the router can
# serve since #1416 and no longer says which model is loaded.
CODING_PRESET = "qwen3.8-27b"

# The lease holder: the service's own name, never a ticket or a person.
LEASE_HOLDER = "werkstatt"
# How long a preparing window is waited for before the run goes ahead without it.
LEASE_WAIT_S = 1800

COMMIT_NAME = "Pi Autoloop"
COMMIT_EMAIL = "pi-autoloop@users.noreply.github.com"

# A ticket body is a prompt, not an archive: past a few pages it costs context
# without adding instruction.
BODY_MAX = 8000


def env(key: str, default: str = "") -> str:
    val = os.environ.get(key, "")
    return val if val else default


def config_from_env(environ=os.environ) -> dict:
    def _int(key: str, default: int) -> int:
        raw = environ.get(key, "")
        try:
            return int(raw) if raw else default
        except ValueError:
            return default

    return {
        "enabled": (environ.get("PI_AUTOLOOP_ENABLED", "") or "false").strip().lower()
        in ("1", "true", "yes", "on"),
        "repos": [
            r.strip()
            for r in (environ.get("PI_AUTOLOOP_REPOS") or DEFAULT_REPOS).split(",")
            if r.strip()
        ],
        "label": (environ.get("PI_AUTOLOOP_LABEL") or DEFAULT_LABEL).strip(),
        "escalate_label": (environ.get("PI_AUTOLOOP_ESCALATE_LABEL") or "needs-claude").strip(),
        "interval_s": _int("PI_AUTOLOOP_INTERVAL_S", DEFAULT_INTERVAL_S),
        "time_cap_s": _int("PI_AUTOLOOP_TIME_CAP_S", DEFAULT_TIME_CAP_S),
        "api": (environ.get("PI_AUTOLOOP_API_URL") or DEFAULT_API).rstrip("/"),
        "store": environ.get("WERKSTATT_GIT_TOKEN_FILE") or DEFAULT_CREDENTIAL_STORE,
        "protocol_dir": environ.get("PI_AUTOLOOP_PROTOCOL_DIR") or DEFAULT_PROTOCOL_DIR,
        "workroot": environ.get("PI_AUTOLOOP_WORKROOT") or DEFAULT_WORKROOT,
        # the llama role's policy proxy (ADR 0011): its /v1/models marks
        # allowed_in_mode; preset_to_use picks the coding preset when offered
        "models_url": (environ.get("MODEL_URL") or "http://host.containers.internal:11435/v1").rstrip("/") + "/models",
        # the GPU lease and its key (laib builtins ${MODEL_LEASE}, ${MODEL_KEY}); empty = no lease
        "lease_url": (environ.get("MODEL_LEASE") or "").strip(),
        "model_key": (environ.get("MODEL_KEY") or "").strip(),
    }


def jlog(level: str, tag: str, message: str, **args: object) -> None:
    sys.stdout.write(
        json.dumps({"level": level, "tag": tag, "message": message, "args": args})
        + "\n"
    )
    sys.stdout.flush()


# ── pure decisions (unit-tested in templates/tests) ──────────────────────────


def claim_ref(issue: int | str) -> str:
    return f"{CLAIM_REF_PREFIX}/{issue}"


def slug(title: str) -> str:
    s = re.sub(r"[^a-z0-9]+", "-", title.lower()).strip("-")
    return s[:40].strip("-") or "ticket"


def branch_name(issue: int | str, title: str) -> str:
    return f"{BRANCH_PREFIX}/{issue}-{slug(title)}"


def branch_marker(issue: int | str) -> str:
    """The `matching-refs` prefix that says "this issue was already worked".

    The claim ref is released at the end of a run, so it cannot be what stops
    the next tick from picking the same ticket up again — the pushed branch is.
    Matching on the issue number alone and not on the slug keeps that true when
    somebody edits the ticket title in between.
    """
    return f"heads/{BRANCH_PREFIX}/{issue}-"


def token_from_credentials(text: str, host: str) -> str:
    """The token out of a git credential store, for the given host."""
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        parts = urllib.parse.urlsplit(line)
        if parts.hostname == host and parts.password:
            return urllib.parse.unquote(parts.password)
    return ""


def detect_gates(root: str | Path) -> list[list[str]]:
    """The target repo's own gates, read off the repository rather than guessed.

    Python and Node are both detected because both live on this box; the order
    is lint before tests, so a run that is going to fail fails cheaply.
    """
    root = Path(root)
    gates: list[list[str]] = []
    if (root / "ruff.toml").exists() or (root / ".ruff.toml").exists():
        gates.append(["ruff", "check", "."])
        gates.append(["ruff", "format", "--check", "."])
    if (root / "pytest.ini").exists() or (root / "tests").is_dir():
        gates.append(["pytest", "-q"])
    try:
        scripts = json.loads((root / "package.json").read_text(encoding="utf-8")).get(
            "scripts", {}
        )
    except (OSError, ValueError, AttributeError):
        scripts = {}
    if "lint" in scripts:
        gates.append(["npm", "run", "lint"])
    if "test" in scripts:
        gates.append(["npm", "test"])
    return gates


def summarise_events(lines: list[str]) -> dict:
    """What Pi did, from its `--mode json` event stream.

    Only the shapes documented in upstream's docs/json.md are read, and an
    unparseable line is counted rather than fatal — the stream is a report, and
    a report that can abort the run it reports on is worse than an incomplete
    one.
    """
    events = 0
    unparsed = 0
    tools: dict[str, int] = {}
    errors = 0
    last = ""
    for line in lines:
        line = line.strip()
        if not line:
            continue
        try:
            ev = json.loads(line)
        except ValueError:
            unparsed += 1
            continue
        if not isinstance(ev, dict):
            unparsed += 1
            continue
        events += 1
        kind = ev.get("type", "")
        last = kind or last
        if kind == "tool_execution_end":
            name = str(ev.get("toolName", "?"))
            tools[name] = tools.get(name, 0) + 1
            if ev.get("isError"):
                errors += 1
    return {
        "events": events,
        "unparsed": unparsed,
        "tools": tools,
        "tool_errors": errors,
        "last": last,
    }


def task_prompt(repo: str, issue: dict) -> str:
    body = (issue.get("body") or "").strip()[:BODY_MAX]
    return (
        f"Arbeite dieses Ticket aus {repo} ab.\n\n"
        f"#{issue.get('number')}: {issue.get('title', '')}\n\n"
        f"{body}\n\n"
        "Halte dich an die Konventionen des Repositories (AGENTS.md bzw. CLAUDE.md). "
        "Ändere nur Dateien in diesem Arbeitsverzeichnis. "
        "Committe und pushe nicht — das macht der Loop."
    )


def commit_subject(issue: dict) -> str:
    """A neutral subject. Deliberately not the target repo's conventional type:
    the loop cannot know the scope, and a wrong `feat(x):` merged by a human
    would move that repo's release version for the wrong reason. Klammern raus —
    ein Streu-`(...)` lässt release-please grün laufen und nichts schneiden."""
    title = re.sub(r"[()]", "", str(issue.get("title", ""))).strip()
    return f"pi: #{issue.get('number')} {title}"[:100]


def protocol_path(directory: str | Path, repo: str, issue: int | str) -> Path:
    return Path(directory) / f"{repo.replace('/', '-')}-{issue}.log"


def format_protocol(
    repo: str,
    issue: int,
    model: str,
    seconds: float,
    gates: list[dict],
    pi: dict,
    branch: str,
    pr_url: str,
    note: str = "",
) -> str:
    lines = [
        f"Pi-Autoloop — {repo}#{issue}",
        f"Modell:   {model or 'unbekannt'}",
        f"Dauer:    {int(seconds)} s",
        f"Zweig:    {branch}",
        f"PR:       {pr_url or '—'}",
    ]
    if note:
        lines.append(f"Hinweis:  {note}")
    lines.append("")
    lines.append("Pi:")
    lines.append(
        f"  {pi.get('events', 0)} Ereignisse, letztes '{pi.get('last', '—')}', "
        f"{pi.get('unparsed', 0)} unlesbar"
    )
    tools = pi.get("tools") or {}
    tool_text = ", ".join(f"{k}×{v}" for k, v in sorted(tools.items())) or "keine"
    lines.append(f"  Werkzeuge: {tool_text} ({pi.get('tool_errors', 0)} mit Fehler)")
    lines.append("")
    lines.append("Gates:")
    if not gates:
        lines.append("  keine erkannt")
    for g in gates:
        lines.append(f"  {g['status']:<12} {g['command']}")
    lines.append("")
    lines.append("Dieser Loop führt nichts zusammen.")
    return "\n".join(lines) + "\n"


def gates_are_red(gates: list[dict]) -> bool:
    return any(g["status"] == "rot" for g in gates)


# ── the wire ────────────────────────────────────────────────────────────────


def http_json(
    url: str,
    method: str = "GET",
    token: str = "",
    payload: dict | None = None,
    timeout: float = 30.0,
):
    """`(status, decoded body)`. Status 0 means GitHub did not answer."""
    data = json.dumps(payload).encode("utf-8") if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/vnd.github+json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, _decode(resp.read())
    except urllib.error.HTTPError as e:
        try:
            raw = e.read()
        except OSError:
            raw = b""
        return e.code, _decode(raw)
    except (urllib.error.URLError, TimeoutError, OSError) as e:
        return 0, {"message": str(e)}


def _decode(raw: bytes):
    try:
        return json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, ValueError):
        return None


def acquire_claim(api: str, repo: str, issue: int, sha: str, token: str, http) -> bool:
    """The cross-instance lock. Only a clean create grants it (fail closed)."""
    if not re.fullmatch(r"[0-9a-f]{40}", sha or ""):
        return False
    status, body = http(
        f"{api}/repos/{repo}/git/refs",
        "POST",
        token,
        {"ref": claim_ref(issue), "sha": sha},
    )
    if status in (200, 201):
        return True
    jlog(
        "info",
        "pi-autoloop:claim",
        "not ours",
        repo=repo,
        issue=issue,
        status=status,
        detail=str((body or {}).get("message", ""))[:200],
    )
    return False


def release_claim(api: str, repo: str, issue: int, token: str, http) -> None:
    http(f"{api}/repos/{repo}/git/{claim_ref(issue)}", "DELETE", token)


def default_branch_sha(api: str, repo: str, token: str, http) -> tuple[str, str]:
    status, body = http(f"{api}/repos/{repo}", "GET", token)
    branch = (body or {}).get("default_branch", "main") if status == 200 else "main"
    status, body = http(f"{api}/repos/{repo}/git/ref/heads/{branch}", "GET", token)
    if status != 200 or not isinstance(body, dict):
        return branch, ""
    return branch, str((body.get("object") or {}).get("sha", ""))


def open_tickets(api: str, repo: str, label: str, token: str, http) -> list[dict]:
    query = urllib.parse.urlencode(
        {"labels": label, "state": "open", "per_page": "50", "sort": "created"}
    )
    status, body = http(f"{api}/repos/{repo}/issues?{query}", "GET", token)
    if status != 200 or not isinstance(body, list):
        jlog(
            "warn",
            "pi-autoloop:poll",
            "issue list unavailable",
            repo=repo,
            status=status,
        )
        return []
    return sorted(
        (i for i in body if isinstance(i, dict) and "pull_request" not in i),
        key=lambda i: i.get("number", 0),
    )


def already_worked(api: str, repo: str, issue: int, token: str, http) -> bool:
    status, body = http(
        f"{api}/repos/{repo}/git/matching-refs/{branch_marker(issue)}", "GET", token
    )
    return status == 200 and isinstance(body, list) and len(body) > 0


def preset_to_use(url: str, http) -> str:
    """The preset this run asks the router for, or "" when it cannot be asked.

    The coding preset when the router offers it; otherwise the first preset it
    does offer, so a box whose presets were renamed still works tickets instead
    of sending a model name nothing answers to.
    """
    status, body = http(url, "GET")
    if status != 200 or not isinstance(body, dict):
        return ""
    ids = [
        str(entry.get("id", ""))
        for entry in (body.get("data") or [])
        if isinstance(entry, dict) and entry.get("id")
    ]
    if CODING_PRESET in ids:
        return CODING_PRESET
    return ids[0] if ids else ""


def take_lease(cfg: dict, preset: str, ttl_s: int, http, sleep=time.sleep) -> tuple[str, int]:
    """`(note, renew_after)`: take the card for this run.

    A note means somebody else holds the window: no run this pass. An empty
    note with renew_after 0 means no lease (none configured, or it failed for
    another reason); the run goes ahead and the proxy takes the window itself.
    """
    url = cfg.get("lease_url", "")
    if not url or not preset:
        return "", 0
    status, body = http(url, "POST", cfg.get("model_key", ""), {"model": preset, "ttl_s": ttl_s, "holder": LEASE_HOLDER})
    body = body if isinstance(body, dict) else {}
    if status == 409:
        until = body.get("expires_at")
        when = time.strftime(" bis %H:%M", time.localtime(until)) if isinstance(until, (int, float)) else ""
        return f"Grafikkarte belegt von {body.get('holder') or 'jemand anderem'}{when}; das Ticket bleibt für den nächsten Durchlauf", 0
    if status == 202:
        waited = 0.0
        while waited < LEASE_WAIT_S:
            pause = body.get("retry_after") if isinstance(body.get("retry_after"), (int, float)) else 30
            sleep(pause)
            waited += pause
            status, body = http(url, "GET")
            body = body if isinstance(body, dict) else {}
            if status != 200:
                return "", 0
            if body.get("state") == "ready":
                return "", int(body.get("renew_after") or 0)
        return "", 0
    if status == 200 and body.get("state") == "ready":
        return "", int(body.get("renew_after") or 0)
    jlog("warn", "pi-autoloop:lease", "no lease; the proxy takes the window itself", status=status, reason=body.get("reason") or body.get("message"))
    return "", 0


class renewing:
    """Renew the run's lease every renew_after seconds; give it back at the end."""

    def __init__(self, cfg: dict, preset: str, ttl_s: int, renew_after: float, http):
        self.cfg, self.preset, self.ttl_s, self.renew_after, self.http = cfg, preset, ttl_s, renew_after, http
        self.stop = threading.Event()
        self.thread = threading.Thread(target=self._loop, daemon=True)

    def _loop(self) -> None:
        while not self.stop.wait(self.renew_after):
            self.http(self.cfg["lease_url"], "POST", self.cfg.get("model_key", ""),
                      {"model": self.preset, "ttl_s": self.ttl_s, "holder": LEASE_HOLDER})

    def __enter__(self):
        if self.renew_after > 0:
            self.thread.start()
        return self

    def __exit__(self, *exc) -> None:
        self.stop.set()
        if self.thread.is_alive():
            self.thread.join(timeout=5)
        if self.cfg.get("lease_url"):
            self.http(self.cfg["lease_url"], "DELETE", self.cfg.get("model_key", ""), {"holder": LEASE_HOLDER})


def release_own_lease(cfg: dict, http) -> None:
    """On start: a window an earlier run of this loop left behind is given back."""
    if not cfg.get("lease_url"):
        return
    status, body = http(cfg["lease_url"], "GET")
    if status == 200 and isinstance(body, dict) and body.get("holder") == LEASE_HOLDER and body.get("state") == "ready":
        http(cfg["lease_url"], "DELETE", cfg.get("model_key", ""), {"holder": LEASE_HOLDER})


def refusal_note(lines: list[str], preset: str) -> str:
    """A sentence for the protocol when the run died on a refused preset.

    The gate takes the mode itself now (#1435), so what still reaches here is a
    refusal it could not resolve — a window somebody else holds. Pi reports that
    as a provider error and then changes nothing, which in the protocol is
    indistinguishable from a model that found nothing to do, so the refusal has
    to be named where the operator reads it.
    """
    for line in lines:
        low = line.lower()
        if "409" not in low or ("allowed" not in low and "mode" not in low):
            continue
        # The body reaches this file inside Pi's own JSON event, so the router's
        # quotes arrive escaped as often as not.
        found = re.search(r'\\?"mode\\?"\s*:\s*\\?"([^"\\]+)', line)
        mode = found.group(1) if found else "Haushalt"
        return (
            f"Modell {preset} ist im Modus {mode} nicht erlaubt und der Modus "
            "war nicht zu bekommen — die Modell-Kachel in Solaris zeigt, wer "
            "die Grafikkarte gerade hält und bis wann. Der Loop nimmt das "
            "Ticket beim nächsten Durchgang von selbst wieder auf."
        )
    return ""


# ── the box side ────────────────────────────────────────────────────────────


def git(args: list[str], cwd: str | Path, timeout: float = 600.0):
    return subprocess.run(
        ["git", *args],
        cwd=str(cwd),
        capture_output=True,
        text=True,
        timeout=timeout,
        check=False,
    )


def clone(repo: str, branch: str, dest: Path) -> bool:
    if dest.exists():
        shutil.rmtree(dest, ignore_errors=True)
    dest.parent.mkdir(parents=True, exist_ok=True)
    res = git(
        [
            "clone",
            "--depth",
            "50",
            "--branch",
            branch,
            f"https://github.com/{repo}.git",
            str(dest),
        ],
        cwd=dest.parent,
    )
    if res.returncode != 0:
        jlog(
            "error",
            "pi-autoloop:clone",
            "clone failed",
            repo=repo,
            detail=res.stderr[-400:],
        )
    return res.returncode == 0


def run_pi(
    prompt: str, model: str, cwd: Path, time_cap_s: int
) -> tuple[dict, bool, str]:
    """`(event summary, hit the time cap, refusal note)`."""
    cmd = ["pi", "--mode", "json"]
    if model:
        cmd += ["--model", f"{PROVIDER_ID}/{model}"]
    cmd.append(prompt)
    try:
        res = subprocess.run(
            cmd,
            cwd=str(cwd),
            stdin=subprocess.DEVNULL,  # pi reads a piped stdin as more prompt; an open one holds the run (#261)
            capture_output=True,
            text=True,
            timeout=time_cap_s,
            check=False,
        )
        lines = (res.stdout + res.stderr).splitlines()
        return (
            summarise_events(res.stdout.splitlines()),
            False,
            refusal_note(lines, model),
        )
    except subprocess.TimeoutExpired as e:
        out = e.stdout or ""
        if isinstance(out, bytes):
            out = out.decode("utf-8", "replace")
        return summarise_events(out.splitlines()), True, ""
    except OSError as e:
        jlog("error", "pi-autoloop:pi", "pi could not be started", detail=str(e))
        summary = summarise_events([])
        summary["started"] = False  # transient: the image or the PATH, not the ticket (#44)
        return summary, True, ""


def run_gates(cwd: Path) -> list[dict]:
    """Run what the repo declares. A gate whose tool this image does not carry
    is reported as skipped, never as green: the container has git, node and a
    bare python3, so `ruff`/`pytest` are frequently simply absent, and calling
    that a pass would be the one failure mode nobody would notice."""
    results = []
    for cmd in detect_gates(cwd):
        if shutil.which(cmd[0]) is None:
            results.append(
                {"command": " ".join(cmd), "status": "übersprungen", "detail": ""}
            )
            continue
        try:
            res = subprocess.run(
                cmd, cwd=str(cwd), capture_output=True, text=True, timeout=1800
            )
            ok = res.returncode == 0
            detail = "" if ok else (res.stdout + res.stderr)[-600:]
        except (subprocess.TimeoutExpired, OSError) as e:
            ok, detail = False, str(e)[:600]
        results.append(
            {
                "command": " ".join(cmd),
                "status": "grün" if ok else "rot",
                "detail": detail,
            }
        )
    return results


def commit_and_push(cwd: Path, branch: str, subject: str, issue: int) -> bool:
    git(["checkout", "-B", branch], cwd)
    git(["add", "-A"], cwd)
    if git(["diff", "--cached", "--quiet"], cwd).returncode == 0:
        return False
    message = f"{subject}\n\nRefs #{issue}\n\nWorked by Pi in the Werkstatt.\n"
    res = git(
        [
            "-c",
            f"user.name={COMMIT_NAME}",
            "-c",
            f"user.email={COMMIT_EMAIL}",
            "commit",
            "-m",
            message,
        ],
        cwd,
    )
    if res.returncode != 0:
        jlog("error", "pi-autoloop:commit", "commit failed", detail=res.stderr[-400:])
        return False
    res = git(["push", "origin", f"HEAD:refs/heads/{branch}"], cwd)
    if res.returncode != 0:
        jlog("error", "pi-autoloop:push", "push failed", detail=res.stderr[-400:])
        return False
    return True


def open_pull_request(
    api: str,
    repo: str,
    issue: int,
    title: str,
    branch: str,
    base: str,
    draft: bool,
    token: str,
    http,
) -> tuple[int, str]:
    body = (
        f"Bearbeitet von Pi in der Werkstatt auf der Box.\n\n"
        f"Refs #{issue}\n\n"
        "Der Loop führt nichts zusammen — Zusammenführen entscheidet ein Mensch "
        "oder die Claude-Seite. Das Protokoll steht als Kommentar an diesem PR.\n"
    )
    status, payload = http(
        f"{api}/repos/{repo}/pulls",
        "POST",
        token,
        {
            "title": title,
            "head": branch,
            "base": base,
            "body": body,
            "draft": draft,
        },
    )
    if status not in (200, 201) or not isinstance(payload, dict):
        jlog("error", "pi-autoloop:pr", "PR not opened", repo=repo, status=status)
        return 0, ""
    return int(payload.get("number", 0)), str(payload.get("html_url", ""))


def write_protocol(directory: str, repo: str, issue: int, text: str) -> None:
    path = protocol_path(directory, repo, issue)
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
    except OSError as e:
        jlog(
            "warn",
            "pi-autoloop:protocol",
            "could not write",
            path=str(path),
            error=str(e),
        )


def attempts_path(directory: str | Path, repo: str, issue: int | str) -> Path:
    return protocol_path(directory, repo, issue).with_suffix(".attempts")


def count_attempt(directory: str | Path, repo: str, issue: int | str) -> int:
    """One more transient attempt for this ticket; returns the new count."""
    path = attempts_path(directory, repo, issue)
    try:
        n = int(path.read_text(encoding="utf-8").strip() or "0")
    except (OSError, ValueError):
        n = 0
    n += 1
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(str(n), encoding="utf-8")
    except OSError:
        pass
    return n


def work_ticket(cfg: dict, repo: str, issue: dict, token: str, base: str, http) -> None:
    number = int(issue["number"])
    started = time.monotonic()
    dest = Path(cfg["workroot"]) / repo / str(number)
    branch = branch_name(number, str(issue.get("title", "")))
    model = preset_to_use(cfg["models_url"], http)
    ttl_s = cfg["time_cap_s"] + 600
    held, renew_after = take_lease(cfg, model, ttl_s, http)
    if held:
        # somebody else has the card: no run, no attempt counted, the label stays
        jlog("info", "pi-autoloop:lease", held, repo=repo, issue=number, model=model)
        return
    gates: list[dict] = []
    pi: dict = summarise_events([])
    pr_url = ""
    note = ""
    transient = False

    if not clone(repo, base, dest):
        note = "Klon fehlgeschlagen"
        transient = True
    else:
        with renewing(cfg, model, ttl_s, renew_after, http) if renew_after else _no_lease():
            pi, capped, refused = run_pi(
                task_prompt(repo, issue), model, dest, cfg["time_cap_s"]
            )
        if capped:
            note = "Zeitlimit erreicht — Pi abgebrochen"
        if refused:
            note = refused
            transient = True  # the mode was not to be had; next pass it may be
            jlog(
                "warn", "pi-autoloop:pi", refused, repo=repo, issue=number, model=model
            )
        if pi.get("started") is False:
            transient = True  # pi could not be started at all
        gates = run_gates(dest)
        pushed = commit_and_push(dest, branch, commit_subject(issue), number)
        if not pushed:
            note = note or "keine Änderung im Arbeitsverzeichnis"
        else:
            draft = capped or gates_are_red(gates)
            pr_number, pr_url = open_pull_request(
                cfg["api"],
                repo,
                number,
                f"Pi #{number}: {issue.get('title', '')}"[:100],
                branch,
                base,
                draft,
                token,
                http,
            )
            protocol = format_protocol(
                repo,
                number,
                model,
                time.monotonic() - started,
                gates,
                pi,
                branch,
                pr_url,
                note,
            )
            if pr_number:
                http(
                    f"{cfg['api']}/repos/{repo}/issues/{pr_number}/comments",
                    "POST",
                    token,
                    {"body": f"```\n{protocol}```"},
                )
            write_protocol(cfg["protocol_dir"], repo, number, protocol)
            jlog(
                "info",
                "pi-autoloop:done",
                "pull request opened",
                repo=repo,
                issue=number,
                pr=pr_number,
                draft=draft,
                model=model,
            )
            return

    protocol = format_protocol(
        repo, number, model, time.monotonic() - started, gates, pi, branch, pr_url, note
    )
    write_protocol(cfg["protocol_dir"], repo, number, protocol)
    tries = 1
    if transient:
        # a transient failure keeps the label: the ticket is taken again next
        # pass, up to TRANSIENT_ATTEMPTS; only then it goes to Claude (#44)
        tries = count_attempt(cfg["protocol_dir"], repo, number)
        if tries < TRANSIENT_ATTEMPTS:
            jlog(
                "warn",
                "pi-autoloop:retry",
                "transient failure; the ticket keeps its label",
                repo=repo,
                issue=number,
                attempt=tries,
                of=TRANSIENT_ATTEMPTS,
                note=note,
            )
            return
    # The router (issue #8): pi had its attempt(s); hand the ticket to the
    # claude hand with the protocol attached, and take pi's label off so this
    # loop never retries it.
    http(
        f"{cfg['api']}/repos/{repo}/issues/{number}/comments",
        "POST",
        token,
        {"body": f"pi konnte das Ticket nicht abschließen (Versuch {tries} von {TRANSIENT_ATTEMPTS}); an Claude übergeben.\n\n```\n" + protocol + "```"},
    )
    http(f"{cfg['api']}/repos/{repo}/issues/{number}/labels", "POST", token, {"labels": [cfg["escalate_label"]]})
    http(f"{cfg['api']}/repos/{repo}/issues/{number}/labels/{cfg['label']}", "DELETE", token)
    jlog(
        "info",
        "pi-autoloop:done",
        "no pull request; escalated",
        repo=repo,
        issue=number,
        label=cfg["escalate_label"],
        note=note,
    )


class _no_lease:
    def __enter__(self):
        return self

    def __exit__(self, *exc) -> None:
        return None


def tick(cfg: dict, token: str, http=http_json) -> int:
    """One pass over the configured repositories. Returns the tickets worked."""
    worked = 0
    for repo in cfg["repos"]:
        base, sha = default_branch_sha(cfg["api"], repo, token, http)
        if not sha:
            jlog("warn", "pi-autoloop:poll", "no claim target", repo=repo)
            continue
        for issue in open_tickets(cfg["api"], repo, cfg["label"], token, http):
            number = int(issue["number"])
            if already_worked(cfg["api"], repo, number, token, http):
                continue
            if not acquire_claim(cfg["api"], repo, number, sha, token, http):
                continue
            try:
                work_ticket(cfg, repo, issue, token, base, http)
                worked += 1
            finally:
                release_claim(cfg["api"], repo, number, token, http)
            # One ticket per pass: the time cap is per ticket, and a pass that
            # drained a whole label would hold the box for a whole night.
            return worked
    return worked


def main() -> int:
    cfg = config_from_env()
    release_own_lease(cfg, http_json)
    while True:
        if not cfg["enabled"]:
            jlog(
                "info",
                "pi-autoloop",
                "switched off; set PI_AUTOLOOP_ENABLED=true to let Pi work tickets",
            )
            time.sleep(max(cfg["interval_s"], 60))
            continue
        try:
            token = Path(cfg["store"]).read_text(encoding="utf-8")
        except OSError:
            token = ""
        token = token_from_credentials(token, "github.com")
        if not token:
            jlog(
                "warn",
                "pi-autoloop",
                "no git credential for github.com; set the template value PI_GIT_TOKEN",
                store=cfg["store"],
            )
        else:
            try:
                tick(cfg, token)
            except Exception as e:  # noqa: BLE001 — a bad ticket must not end the loop
                jlog("error", "pi-autoloop", "pass failed", error=str(e)[:400])
        time.sleep(max(cfg["interval_s"], 30))


if __name__ == "__main__":
    sys.exit(main())
