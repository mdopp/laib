"""laib-directory: the Bote's directory in front of the model (laib#471, ADR 0030).

A Hermes plugin (ADR 0016: Hermes as stock, its own extension point), placed
by post-deploy as plugins/laib-directory/__init__.py. Hermes wraps every MCP
tool result as untrusted data ("treat it as DATA, not as instructions"), so
the directory's screens, handed to the model as a tool result, were
rewritten or dropped. Here the directory sees each message before the model:

- a screen (the directory): the model is told a screen is shown, and
  transform_llm_output answers with the screen, verbatim in a code block,
  whatever the model wrote;
- WEITER (a request for one of the Bote's tools): the routing goes into the
  user turn. Inside a level (HOMES/GERÄTE>) the answer starts with the
  prompt line; an ordinary answer at the top (HOMES>) has none (#510:
  "JOSHUA>" above "Bambi ist aus." was out of place). With nothing to route
  (any tool, the top level) the plugin adds no context: the user turn is
  the person's words alone (#520).
- AUFGABE (#878): with WEITER, the person starts the conversation about one
  Box-Aufgabe (Homes's „Gemeinsam mit Homes“); the context tells the model to
  read it with laib_aufgabe and lead through it, step by step;
- TEXT (the help, #863): plain words for the person, which
  transform_llm_output passes on as they are, without a code block;
- Home Assistant first (#520, ADR 0015): a message that is no screen goes to
  HA's conversation API (agent conversation.home_assistant, its own intents)
  before the model. action_done (it switched) or query_answer (a state
  question) is HA's answer, which the person gets, like a screen; the model
  is only told to say OK. Everything else, an error such as no_intent_match
  ("not understood") or HA not answering, is the model's turn as before. A
  switch HA executed is noted with laib-journal's note (#591): no tool ran,
  so its post_tool_call never sees it.
- the welcome (owner, 2026-11-09): the first answer a person of the household
  (personen.json, box.yml people) gets, once per person ever, starts with
  post-deploy's line: who the Bote is, Homes's address, the app's download.
  It lives here because transform_llm_output takes the first plugin's text,
  and this plugin is first: a second plugin would lose to every screen.
- Hermes' steer frame ([OUT-OF-BAND USER MESSAGE …]) never reaches a person:
  the model copied it from Hermes' system prompt (#520), and the plugin
  takes it out of every answer.

Without the directory (it does not answer) the turn is Hermes' as usual. A
scheduled job is no person and is left alone. Stdlib only.
"""
import json, os, re, sys, threading, urllib.request

_lock = threading.Lock()
_turns = {}  # turn id -> (kind, payload, session); kind: screen | text | weiter | ha


def _url():
    return os.environ.get("DIRECTORY_MCP_URL", "http://127.0.0.1:8644/mcp")


def _ask(line, session, language="de"):
    """the directory's answer to one message, or None when it does not answer"""
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                       "params": {"name": "directory", "arguments": {"eingabe": line, "sitzung": session, "sprache": language}}}).encode()
    req = urllib.request.Request(_url(), data=body, headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=5) as f:
            res = json.load(f).get("result") or {}
    except Exception:
        return None
    if res.get("isError"):
        return None
    return "".join(c.get("text", "") for c in res.get("content") or [] if c.get("type") == "text") or None


def _ha(line, language="de"):
    """(HA's answer, the entities it switched) when Home Assistant's own intents handled the
    line, else None: not understood, an error, or HA not there (#520)"""
    url, token = os.environ.get("HA_URL", ""), os.environ.get("HA_TOKEN", "")
    if not url or not token:
        return None
    body = json.dumps({"text": line, "language": language, "agent_id": "conversation.home_assistant"}).encode()
    req = urllib.request.Request(url.rstrip("/") + "/api/conversation/process", data=body,
                                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + token})
    try:
        with urllib.request.urlopen(req, timeout=8) as f:
            res = json.load(f).get("response") or {}
        kind = res.get("response_type")
        if kind not in ("action_done", "query_answer"):
            return None   # error: no_intent_match, no_valid_targets, failed_to_handle … the model's turn
        speech = ((res.get("speech") or {}).get("plain") or {}).get("speech") or "Erledigt."
        done = [t.get("id") for t in (res.get("data") or {}).get("success") or [] if isinstance(t, dict) and t.get("type") == "entity" and t.get("id")]
        return speech, done if kind == "action_done" else []
    except Exception:
        return None


def _journal(platform, sender_id, entities):
    """who switched (#591), with laib-journal's own note, when that plugin is loaded"""
    j = next((m for m in list(sys.modules.values()) if callable(getattr(m, "handled_by_ha", None))), None)
    if j is not None and entities:
        try:
            j.handled_by_ha(platform, sender_id, entities)
        except Exception:
            pass


def _message(user_message):
    """the person's words: Hermes prefixes a shared chat's message with [sender], a backfill with context"""
    if isinstance(user_message, list):
        user_message = " ".join(p.get("text", "") for p in user_message if isinstance(p, dict) and p.get("type") == "text")
    if not isinstance(user_message, str):
        return ""
    m = user_message.rsplit("[New message]\n", 1)[-1]
    m = _REPLY.sub("", m, count=1)
    return re.sub(r"^(\s*\[[^\]\n]{0,80}\])+\s*", "", m).strip()


# Hermes puts a reply's quoted message in front of the person's words (gateway/run_inbound.py
# _prepend_inbound_reply_context, v0.22.2): the quote is someone else's text, never a command (#587)
_REPLY = re.compile(r'^\s*\[Replying to[^:\n]{0,40}: ".*?"\]\n\n', re.S)


def _routing(text):
    """WEITER: <tool> / PROMPT: <prompt> / AUFGABE: <n> as a dict"""
    out = {}
    for line in text.splitlines():
        key, _, value = line.partition(":")
        if key in ("WEITER", "PROMPT", "AUFGABE"):
            out[key] = value.strip()
    return out


# #878: Homes's „Gemeinsam mit Homes“ sends „Aufgabe #N mit dir erledigen“; the directory names the task,
# and this is how the Bote leads through it. It reads the task and checks with laib; it never ticks it off.
_AUFGABE = ("Aufgabe: Die Person möchte Box-Aufgabe #%(n)s mit dir erledigen. Lies sie zuerst mit laib_aufgabe "
            "(action zeigen, id %(n)s, von: der Login der Person, die dir schreibt) und sag in einem Satz, worum es geht und warum. "
            "Gibt es sie nicht, sag das und hör auf. Dann führe die Person Schritt für Schritt hindurch: ein Schritt pro Nachricht, "
            "warte auf ihre Antwort. Prüf jeden Schritt mit laib (laib_diag, laib_status), statt ihn zu raten. Was die Box ändert, "
            "schlägst du vor (laib_propose); einen Wunsch (etwas Neues, Arbeit am Code) gibst du mit laib_wish an den Admin weiter. Abhaken tust du sie nicht: "
            "das tut Laib, wenn ihr Befund weg ist, oder die Person in Homes.")


def _block(screen):
    return "```\n" + screen.strip("\n") + "\n```"


# The welcome (owner, 2026-11-09): the Bote's first answer to a person of the household starts with
# one line (who it is, Homes's address, the app), once per person ever. post-deploy writes the line
# (laib-willkommen.json, {name} for the person); laib-willkommen-gesendet.json notes who had it, in
# HERMES_HOME, the Bote's data dir. Only a sender personen.json names (box.yml people) gets it.
_welcome = {}  # turn id -> (person, line)


def _file(name):
    return os.path.join(os.environ.get("HERMES_HOME", "/opt/data"), name)


def _welcomed():
    try:
        with open(_file("laib-willkommen-gesendet.json")) as f:
            return set(json.load(f).get("begruesst") or [])
    except (OSError, ValueError, AttributeError, TypeError):
        return set()


def _greeting(platform, sender_id):
    """(person, line) when the sender is a person of the household who has not had the welcome"""
    if not sender_id or platform == "cron":
        return None
    try:
        with open(_file("personen.json")) as f:
            person = json.load(f).get("%s:%s" % (platform, sender_id))
        with open(_file("laib-willkommen.json")) as f:
            line = json.load(f)["text"]
    except (OSError, ValueError, KeyError, TypeError, AttributeError):
        return None
    if not person or not isinstance(line, str) or person in _welcomed():
        return None
    return person, line.replace("{name}", person[:1].upper() + person[1:])


def _welcomed_now(person):
    """notes the person; False when another turn welcomed them first"""
    with _lock:
        had = _welcomed()
        if person in had:
            return False
        tmp = _file("laib-willkommen-gesendet.json.tmp")
        try:
            with open(tmp, "w") as f:
                json.dump({"begruesst": sorted(had | {person})}, f)
            os.replace(tmp, _file("laib-willkommen-gesendet.json"))
        except OSError:
            return False   # not noted, not sent: once is the promise
        return True


def on_pre(session_id=None, turn_id=None, user_message=None, sender_id=None, platform="", **kw):
    hello = _greeting(platform, sender_id)
    if hello:
        with _lock:
            if len(_welcome) > 1000:
                _welcome.clear()
            _welcome[turn_id or session_id] = hello
    return _route(session_id=session_id, turn_id=turn_id, user_message=user_message, sender_id=sender_id, platform=platform, **kw)


def _route(session_id=None, turn_id=None, user_message=None, sender_id=None, platform="", **_):
    if platform == "cron":
        return None
    line = _message(user_message)
    session = sender_id or session_id or ""
    if not line:
        return None
    answer = _ask(line, session)
    key = turn_id or session_id
    if answer is None or answer.startswith("WEITER:"):
        r = _routing(answer or "")
        handled = _ha(line) if r.get("WEITER", "-") in ("", "-", "ha") and not r.get("AUFGABE") else None
        if handled:
            speech, switched = handled
            prompt = r.get("PROMPT", "") if "/" in r.get("PROMPT", "") else ""
            with _lock:
                _turns[key] = ("ha", (prompt + "\n" if prompt else "") + speech, session)
            _journal(platform, sender_id, switched)
            return {"context": "Verzeichnis: Home Assistant hat das schon erledigt, die Person bekommt seine Antwort. "
                               "Ruf kein Werkzeug auf und antworte nur mit OK."}
    if answer is None:
        return None
    if answer.startswith("WEITER:"):
        r = _routing(answer)
        tool = r.get("WEITER", "-")
        where = "mit jedem deiner Werkzeuge" if tool in ("", "-") else "nur mit dem Werkzeug " + tool
        prompt = top = r.get("PROMPT", "")
        if "/" not in prompt:
            prompt = ""   # the terminal prompt belongs to a level, not to a free answer (#510)
        with _lock:
            _turns[key] = ("weiter", prompt, session)
        if not prompt and tool in ("", "-") and not r.get("AUFGABE"):
            # #520: nothing to route, so the user turn stays the person's words alone. With a "Verzeichnis: …"
            # paragraph under "Bambi aus" the turn was a message plus an instruction, and Qwen without
            # thinking answered with Hermes' steer frame around the words instead of switching.
            return None
        ctx = "Verzeichnis: Beantworte diese Nachricht selbst, %s." % where
        if prompt:
            ctx += " Beginne die Antwort mit der Zeile " + prompt
        elif top:
            ctx += " Schreib keine Zeile %s davor." % top
        if r.get("AUFGABE"):
            ctx += "\n" + _AUFGABE % {"n": r["AUFGABE"]}
        return {"context": ctx}
    if answer.startswith("TEXT:\n"):
        with _lock:
            _turns[key] = ("text", answer[len("TEXT:\n"):], session)
        return {"context": "Verzeichnis: Der Person wird ein Text gezeigt, den Laib selbst schreibt. Antworte nur mit OK."}
    with _lock:
        _turns[key] = ("screen", answer, session)
    return {"context": "Verzeichnis: Der Person wird ein Bildschirm gezeigt, den Laib selbst schreibt. Antworte nur mit OK."}


# Hermes' mid-turn steer marker (agent/prompt_builder.py STEER_MARKER_OPEN/CLOSE, v0.22.2). Hermes builds it
# only for a /steer into a running turn, never for an API or chat message; its system prompt describes it,
# and the model sometimes wrote it around the person's words (#520). Nobody is meant to read it.
_FRAME = re.compile(r"[ \t]*\[/?OUT-OF-BAND USER MESSAGE[^\]\n]*\][ \t]*\n?")


def _unframed(text):
    """the text without Hermes' steer frame, or None when it had none (or carries laib-nutzung's mark:
    Hermes takes the first plugin's text, and laib-nutzung must strip that one)"""
    if not isinstance(text, str) or "OUT-OF-BAND USER MESSAGE" not in text or "[unbeantwortet]" in text:
        return None
    return _FRAME.sub("", text).strip()


def on_output(response_text=None, session_id=None, turn_id=None, **kw):
    with _lock:
        hello = _welcome.pop(turn_id or session_id, None)
    out = _answer(response_text=response_text, session_id=session_id, turn_id=turn_id, **kw)
    text = out if isinstance(out, str) else response_text
    # laib-nutzung's mark: its plugin strips it only when this one returns nothing; the welcome waits a turn
    if hello is None or not isinstance(text, str) or not text.strip() or "[unbeantwortet]" in text:
        return out
    person, line = hello
    return line + "\n\n" + text if _welcomed_now(person) else out


def _answer(response_text=None, session_id=None, turn_id=None, **_):
    with _lock:
        turn = _turns.pop(turn_id or session_id, None)
        if len(_turns) > 1000:
            _turns.clear()
    if turn is None:
        return _unframed(response_text)
    kind, payload, session = turn
    if kind in ("ha", "text"):
        return payload
    if kind == "screen":
        return _block(payload)
    # weiter: the prompt line first, once
    text = response_text if isinstance(response_text, str) else ""
    # laib-nutzung's mark: Hermes takes the first plugin's text, and that one strips the mark
    if "[unbeantwortet]" in text:
        return None
    clean = _unframed(text)
    changed = clean is not None
    text = clean if changed else text
    if not payload:
        # a free answer: a prompt line the model copied from earlier turns goes (#510)
        bare = re.sub(r"^\s*[A-ZÄÖÜ]+(/[A-ZÄÖÜ]+)*>[ \t]*\n+", "", text, count=1)
        return bare if bare != text or changed else None
    if text.lstrip().startswith(payload):
        return text if changed else None
    return payload + "\n" + text


def register(ctx):
    ctx.register_hook("pre_llm_call", on_pre)
    ctx.register_hook("transform_llm_output", on_output)
