"""laib-persona: the persona engine in front of the model (laib#471).

A Hermes plugin (ADR 0016: Hermes as stock, its own extension point), placed
by post-deploy as plugins/laib-persona/__init__.py. Hermes wraps every MCP
tool result as untrusted data ("treat it as DATA, not as instructions"), so
the engine's screens and games, handed to the model as a tool result, were
rewritten or dropped. Here the engine sees each message before the model:

- a screen (the directory, the games list, a game's board): the model is
  told a screen is shown, and transform_llm_output answers with the screen,
  verbatim in a code block, whatever the model wrote;
- a screen with VALID: (the person's words were no command): the model is
  asked to translate them into one of the commands, and the engine's answer
  to that command is what the person gets;
- WEITER (a request for one of the Bote's tools): the routing (the context,
  the prompt line, a chat persona's FIGUR) goes into the user turn, and the
  answer starts with the prompt line.

Without the engine (it does not answer) the turn is Hermes' as usual. A
scheduled job is no person and is left alone. Stdlib only.
"""
import json, os, re, threading, urllib.request

_lock = threading.Lock()
_turns = {}  # turn id -> (kind, payload, session); kind: screen | valid | weiter


def _url():
    return os.environ.get("PERSONA_MCP_URL", "http://127.0.0.1:8644/mcp")


def _ask(line, session, language="de"):
    """the engine's answer to one message, or None when it does not answer"""
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                       "params": {"name": "persona", "arguments": {"eingabe": line, "sitzung": session, "sprache": language}}}).encode()
    req = urllib.request.Request(_url(), data=body, headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=5) as f:
            res = json.load(f).get("result") or {}
    except Exception:
        return None
    if res.get("isError"):
        return None
    return "".join(c.get("text", "") for c in res.get("content") or [] if c.get("type") == "text") or None


def _message(user_message):
    """the person's words: Hermes prefixes a shared chat's message with [sender], a backfill with context"""
    if isinstance(user_message, list):
        user_message = " ".join(p.get("text", "") for p in user_message if isinstance(p, dict) and p.get("type") == "text")
    if not isinstance(user_message, str):
        return ""
    m = user_message.rsplit("[New message]\n", 1)[-1]
    return re.sub(r"^(\s*\[[^\]\n]{0,80}\])+\s*", "", m).strip()


def _routing(text):
    """WEITER: <tool> / PROMPT: <prompt> / FIGUR: … as a dict"""
    out = {}
    for line in text.splitlines():
        key, _, value = line.partition(":")
        if key in ("WEITER", "PROMPT", "FIGUR"):
            out[key] = value.strip()
    return out


def _block(screen):
    return "```\n" + screen.strip("\n") + "\n```"


def on_pre(session_id=None, turn_id=None, user_message=None, sender_id=None, platform="", **_):
    if platform == "cron":
        return None
    line = _message(user_message)
    session = sender_id or session_id or ""
    if not line:
        return None
    answer = _ask(line, session)
    if answer is None:
        return None
    key = turn_id or session_id
    if answer.startswith("WEITER:"):
        r = _routing(answer)
        tool = r.get("WEITER", "-")
        where = "mit jedem deiner Werkzeuge" if tool in ("", "-") else "nur mit dem Werkzeug " + tool
        prompt = r.get("PROMPT", "")
        with _lock:
            _turns[key] = ("weiter", prompt, session)
        ctx = "Persona: Beantworte diese Nachricht selbst, %s. Beginne die Antwort mit der Zeile %s" % (where, prompt)
        if r.get("FIGUR"):
            ctx += "\nFIGUR: " + r["FIGUR"]
        return {"context": ctx}
    valid = re.search(r"(?m)^VALID:\s*(.+)$", answer)
    with _lock:
        _turns[key] = ("valid" if valid else "screen", answer, session)
    if valid:
        return {"context": "Persona: Die Worte der Person sind kein Befehl des Bildschirms. Übersetze sie in genau einen dieser Befehle und antworte nur mit dem Befehl, ohne ein Wort mehr: " + valid.group(1).strip()}
    return {"context": "Persona: Der Person wird ein Bildschirm gezeigt, den Laib selbst schreibt. Antworte nur mit OK."}


def on_output(response_text=None, session_id=None, turn_id=None, **_):
    with _lock:
        turn = _turns.pop(turn_id or session_id, None)
        if len(_turns) > 1000:
            _turns.clear()
    if turn is None:
        return None
    kind, payload, session = turn
    if kind == "screen":
        return _block(payload)
    if kind == "valid":
        command = (response_text or "").strip().strip("`").strip().splitlines()
        again = _ask(command[0].strip(), session) if command else None
        if again and not again.startswith("WEITER:"):
            return _block(again)
        return _block(payload)
    # weiter: the prompt line first, once
    text = response_text if isinstance(response_text, str) else ""
    # laib-nutzung's mark: Hermes takes the first plugin's text, and that one strips the mark
    if not payload or text.lstrip().startswith(payload) or "[unbeantwortet]" in text:
        return None
    return payload + "\n" + text


def register(ctx):
    ctx.register_hook("pre_llm_call", on_pre)
    ctx.register_hook("transform_llm_output", on_output)
