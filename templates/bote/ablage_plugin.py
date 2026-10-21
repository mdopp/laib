"""laib-ablage: a photo or a PDF sent to the Bote with "ablegen" or
"archivieren" goes into Paperless (laib#521), by code, not by the model.

A Hermes plugin (ADR 0016: Hermes as stock, its own extension point),
placed by post-deploy as plugins/laib-ablage/__init__.py while Paperless
answers. Two of Hermes' gateway hooks:

- pre_gateway_dispatch sees each incoming message with its attachments
  (MessageEvent.media_urls: files Hermes cached on its disk). A message with
  an attachment and one of the words is marked: its text gets
  [laib-ablage:<id>] in front, the files wait under that id. This hook runs
  before Hermes' own check of who may talk to it, so it only marks.
- post_gateway_admission runs for a message that passed that check. A
  marked one is filed: each file goes to bote-notify's POST /ablage (the
  pod's loopback, its bearer), which writes it into Paperless' consume
  folder as Bote/<sender>/<title>.<ext>; Paperless makes the folders tags.
  The message is then handled: the person gets "Liegt in Paperless, Titel
  …" and the model never sees it. Hermes itself (uid 10000) cannot write
  into the share; bote-notify runs as the box's user, as Haus does.

The title is what the person wrote besides the word ("Stromrechnung
ablegen"), else bote-notify makes one from the sender and the time.
Stdlib only.
"""
import asyncio, base64, json, os, re, threading, urllib.error, urllib.request, uuid

WORDS = re.compile(r"\b(ablegen|archivieren)\b", re.I)
MARK = re.compile(r"\[laib-ablage:([0-9a-f]{32})\]\s*")
FILLER = re.compile(r"\b(bitte|in\s+paperless)\b", re.I)
MAX_FILE = 25 << 20  # as Haus' upload (haus#10)
_lock = threading.Lock()
_waiting = {}  # id -> the cached files; cleared when it grows (a message refused by Hermes never comes back)


def _url():
    return os.environ.get("ABLAGE_URL", "http://127.0.0.1:8645/ablage")


def on_dispatch(event=None, **_):
    media = list(getattr(event, "media_urls", None) or [])
    text = getattr(event, "text", None) or ""
    if not media or not WORDS.search(text) or MARK.search(text):
        return None
    key = uuid.uuid4().hex
    with _lock:
        if len(_waiting) > 200:
            _waiting.clear()
        _waiting[key] = media
    return {"action": "rewrite", "text": "[laib-ablage:%s] %s" % (key, text)}


def title_of(text):
    """the person's words without the mark, the word and the filler; "" when nothing is left"""
    t = MARK.sub("", text or "")
    t = WORDS.sub(" ", t)
    t = FILLER.sub(" ", t)
    t = re.sub(r"[\s:;,.!?\"'„“”]+", " ", t).strip()
    return t[:80].strip()


def _send(path, title, source):
    with open(path, "rb") as f:
        data = f.read(MAX_FILE + 1)
    if len(data) > MAX_FILE:
        return None, "zu groß (höchstens 25 MB)"
    body = json.dumps({"titel": title, "plattform": source.get("platform") or "", "absender": source.get("user_id") or "",
                       "name": source.get("user_name") or "", "daten": base64.b64encode(data).decode()}).encode()
    req = urllib.request.Request(_url(), data=body, method="POST", headers={
        "Content-Type": "application/json", "Authorization": "Bearer " + os.environ.get("NOTIFY_TOKEN", "")})
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            return json.load(r).get("titel"), None
    except urllib.error.HTTPError as e:
        try:
            return None, json.load(e).get("fehler") or str(e.code)
        except ValueError:
            return None, str(e.code)
    except (OSError, ValueError) as e:
        return None, "die Ablage antwortet nicht (%s)" % type(e).__name__


def file_all(media, text, source):
    """files each attachment; the reply for the person"""
    title = title_of(text)
    done, failed = [], []
    for i, path in enumerate(media):
        t = title if len(media) == 1 or not title else "%s %d" % (title, i + 1)
        got, why = _send(path, t, source)
        (done.append(got) if got else failed.append(why))
    if done and not failed:
        if len(done) == 1:
            return "Liegt in Paperless, Titel „%s“. Die Texterkennung braucht ein paar Minuten." % done[0]
        return "Liegen in Paperless: %s. Die Texterkennung braucht ein paar Minuten." % ", ".join("„%s“" % d for d in done)
    if done:
        return "Liegt in Paperless: %s. Nicht abgelegt: %s." % (", ".join("„%s“" % d for d in done), "; ".join(failed))
    return "Das konnte ich nicht ablegen: %s." % "; ".join(dict.fromkeys(failed))


async def on_admitted(text="", source=None, **_):
    m = MARK.search(text or "")
    if not m:
        return None
    with _lock:
        media = _waiting.pop(m.group(1), None)
    if not media:
        return {"action": "handled", "reply": "Das konnte ich nicht ablegen: die Datei ist nicht mehr da. Schick sie bitte noch einmal."}
    reply = await asyncio.to_thread(file_all, media, text, source or {})
    return {"action": "handled", "reply": reply}


def register(ctx):
    ctx.register_hook("pre_gateway_dispatch", on_dispatch)
    ctx.register_hook("post_gateway_admission", on_admitted)
