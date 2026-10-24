"""laib-journal: who switched in Home Assistant through the Bote (laib#591).

A Hermes plugin (ADR 0016: Hermes as stock, its own extension point), placed
by post-deploy as plugins/laib-journal/__init__.py while the ha tool or the
Automat is wired. HA's logbook names the token's user („Bote“); this writes
the person into Laib's journal, as home-assistant's README („Who switched“)
defines the line:

    laib_note kind=watch text="ha person=<id> service=<d>.<s> entity=<id,…> context=<id>"

pre_llm_call learns who wrote the turn (Hermes' sender id: a Signal number,
a Telegram chat id) per session; post_tool_call, after a tool that switched
(an HA intent HA answers action_done with entities, the Automat's
automation_schalten), maps that id to a box.yml people name through
personen.json (pre-start writes it) or `-`, reads HA's context id from the
entity's fresh state and notes the line with the Bote's own token. The note
runs in a thread: it never delays, and never fails, the switch.
"""
import datetime, json, os, threading, urllib.request

FRESH = 30  # seconds: a state older than this was not changed by this call
_lock = threading.Lock()
_senders = {}  # session id -> "platform:sender id"
_warned = False


def _spawn(fn, *args):
    threading.Thread(target=fn, args=args, name="laib-journal", daemon=True).start()


def on_pre(session_id=None, sender_id=None, platform="", **_):
    if not session_id:
        return None
    with _lock:
        if len(_senders) > 1000:
            _senders.clear()
        _senders[session_id] = "%s:%s" % (platform, sender_id) if sender_id and platform != "cron" else ""
    return None


def _person(session_id):
    with _lock:
        key = _senders.get(session_id or "", "")
    if not key:
        return "-"
    try:
        with open(os.path.join(os.environ.get("HERMES_HOME", "/opt/data"), "personen.json")) as f:
            return json.load(f).get(key) or "-"
    except (OSError, ValueError, AttributeError):
        return "-"


def _payload(result):
    """the tool's answer: Hermes wraps an MCP result as {"result": <text>}, HA's text is JSON"""
    try:
        res = json.loads(result) if isinstance(result, str) else result
        if isinstance(res, dict) and "result" in res and not res.get("error"):
            res = res["result"]
            return json.loads(res) if isinstance(res, str) and res[:1] == "{" else res
    except (TypeError, ValueError):
        pass
    return None


def switched(tool_name, args, result, status):
    """(service, [entity ids]) for a tool call that switched something, else None"""
    name = str(tool_name or "")
    res = _payload(result)
    if status not in (None, "ok") or res is None:
        return None
    if name.startswith("mcp__ha__Hass") and isinstance(res, dict) and res.get("response_type") == "action_done":
        ids = [t.get("id") for t in (res.get("data") or {}).get("success") or [] if isinstance(t, dict) and t.get("type") == "entity" and t.get("id")]
        return ("intent." + name.removeprefix("mcp__ha__"), ids) if ids else None
    if name == "mcp__automat__automation_schalten" and isinstance(res, str) and res.startswith("automation."):
        entity, _, svc = res.partition(": ")
        return ("automation." + svc, [entity]) if svc in ("turn_on", "turn_off") else None
    return None


def _context(entities):
    """HA's context id of this call: the first entity whose state changed just now, else -"""
    url, token = os.environ.get("HA_URL", ""), os.environ.get("HA_TOKEN", "")
    now = datetime.datetime.now(datetime.timezone.utc)
    for e in entities if url and token else []:
        try:
            req = urllib.request.Request(url.rstrip("/") + "/api/states/" + e, headers={"Authorization": "Bearer " + token})
            with urllib.request.urlopen(req, timeout=3) as f:
                st = json.load(f)
            age = now - datetime.datetime.fromisoformat(st["last_updated"])
            if age.total_seconds() <= FRESH and st["context"]["id"]:
                return st["context"]["id"]
        except Exception:
            continue
    return "-"


def note(person, service, entities):
    global _warned
    text = "ha person=%s service=%s entity=%s context=%s" % (person, service, ",".join(entities), _context(entities))
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                       "params": {"name": "laib_note", "arguments": {"kind": "watch", "text": text}}}).encode()
    req = urllib.request.Request(os.environ.get("LAIB_MCP_URL", ""), data=body, headers={
        "Content-Type": "application/json", "Authorization": "Bearer " + os.environ.get("LAIB_TOKEN", "")})
    try:
        with urllib.request.urlopen(req, timeout=10) as f:
            res = json.load(f).get("result") or {}
        if res.get("isError"):
            raise RuntimeError("".join(c.get("text", "") for c in res.get("content") or []))
        _warned = False
    except Exception as e:
        if not _warned:
            print("[laib-journal] Laib nimmt die Notiz nicht an (%s): %s" % (e, text), flush=True)
            _warned = True


def on_tool(tool_name="", args=None, result=None, session_id=None, status=None, **_):
    try:
        hit = switched(tool_name, args, result, status)
        if hit:
            _spawn(note, _person(session_id), *hit)
    except Exception:
        pass
    return None


def register(ctx):
    if not os.environ.get("LAIB_TOKEN") or not os.environ.get("LAIB_MCP_URL"):
        return
    ctx.register_hook("pre_llm_call", on_pre)
    ctx.register_hook("post_tool_call", on_tool)
