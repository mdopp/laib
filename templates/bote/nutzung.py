"""laib-nutzung: what the Bote is used for, anonymously (laib#266).

A Hermes plugin (ADR 0016: Hermes as stock, its own extension point), placed
by post-deploy as plugins/laib-nutzung/__init__.py. It counts per event and
UTC day, never who: tool calls per tool, turns, and turns the Bote could not
help with. The one thing with words in it is the sentence of such a turn, sent
to usage-metrics' /miss without its sender, kept there 30 days.

The model marks a turn it could not help with by starting its answer with
MARK (the SOUL asks it to); transform_llm_output strips the mark before the
answer is sent. Hooks only touch memory; a daemon thread flushes every
INTERVAL seconds. usage-metrics not answering drops that batch and logs once.
"""
import datetime, json, os, re, threading, time, urllib.error, urllib.request

INTERVAL = 300
MARK = "[unbeantwortet]"
URL = ""
TOKEN = ""
_lock = threading.Lock()
_counts = {}  # (event, day) -> count
_misses = []  # (text, day)
_marked = set()  # turn ids whose answer carried MARK
_warned = False


def _day():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d")


def _count(event):
    with _lock:
        key = (event[:128], _day())
        _counts[key] = _counts.get(key, 0) + 1


def on_tool(tool_name="", **_):
    name = str(tool_name or "")
    # the SOUL sends every message to the persona router first: its count is the turn count
    if not name or name.startswith("mcp__persona__"):
        return
    name = name.removeprefix("mcp__").replace("__", ".").lower()
    name = re.sub(r"[^a-z0-9._-]", "_", name).strip("._-")
    if name:
        _count("werkzeug." + name)


def on_output(response_text=None, turn_id=None, session_id=None, **_):
    if not isinstance(response_text, str) or MARK not in response_text:
        return None
    with _lock:
        if len(_marked) < 1000:
            _marked.add(turn_id or session_id)
    # an empty return would leave the text, mark and all, as it is
    return response_text.replace(MARK, "").strip() or "Dabei kann ich leider nicht helfen."


def _sentence(message):
    if isinstance(message, list):
        message = " ".join(p.get("text", "") for p in message if isinstance(p, dict) and p.get("type") == "text")
    if not isinstance(message, str):
        return ""
    # Hermes prefixes a backfill with the channel's context, a shared chat's
    # message with [sender], and some with [timestamp]: none of that is the sentence
    message = message.rsplit("[New message]\n", 1)[-1]
    message = re.sub(r"^(\s*\[[^\]\n]{0,80}\])+", "", message)
    message = " ".join("".join(c if c.isprintable() else " " for c in message).split())
    return message[:280]


def on_turn(user_message=None, turn_id=None, session_id=None, platform="", **_):
    with _lock:
        key = turn_id or session_id
        marked = key in _marked
        _marked.discard(key)
    if platform == "cron":  # a scheduled job, not someone asking
        return
    _count("zug")
    if marked:
        _count("unbeantwortet")
        text = _sentence(user_message)
        with _lock:
            if text and len(_misses) < 200:
                _misses.append((text, _day()))


def _post(path, body):
    req = urllib.request.Request(URL.rstrip("/") + path, data=json.dumps(body).encode(), method="POST",
                                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + TOKEN})
    try:
        urllib.request.urlopen(req, timeout=5).close()
        return True
    except urllib.error.HTTPError as e:
        return e.code == 429  # /miss's daily cap: dropped by design, usage-metrics answered
    except Exception:
        return False


def flush():
    global _warned
    with _lock:
        counts, misses = dict(_counts), list(_misses)
        _counts.clear()
        _misses.clear()
    sent = [(path, body) for path, body in
            [("/ingest", {"app": "bote", "event": e, "day": d, "count": n}) for (e, d), n in counts.items()] +
            [("/miss", {"source": "bote", "text": t, "day": d}) for t, d in misses]]
    for path, body in sent:
        if not _post(path, body):
            if not _warned:
                print("[laib-nutzung] usage-metrics antwortet nicht (%s): Zählungen verworfen" % URL, flush=True)
                _warned = True
            return
    if sent:
        _warned = False


def _loop():
    while True:
        time.sleep(INTERVAL)
        try:
            flush()
        except Exception:
            pass


def register(ctx):
    global URL, TOKEN
    URL, TOKEN = os.environ.get("USAGE_METRICS_URL", ""), os.environ.get("USAGE_METRICS_TOKEN", "")
    if not URL or not TOKEN:
        return
    ctx.register_hook("post_tool_call", on_tool)
    ctx.register_hook("transform_llm_output", on_output)
    ctx.register_hook("post_llm_call", on_turn)
    threading.Thread(target=_loop, name="laib-nutzung", daemon=True).start()
