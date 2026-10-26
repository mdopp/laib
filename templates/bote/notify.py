#!/usr/bin/env python3
"""bote-notify (ADR 0022 §3, #177): a message for a person, delivered by the
Bote on that person's chat channel with Hermes' own sender.

POST /mitteilung {"an": "<person>", "text": "…"} with
`Authorization: Bearer <BOTE_NOTIFY_TOKEN>`. The person is looked up in
render/people/people.json (box.yml people, preferred channel first); an
unknown person is 404, never a free target. Delivery is
`hermes send --to <platform>:<target> --file -`, the next channel when one
fails, within one deadline under Home Assistant's 60 s. Size-capped,
rate-limited; the log names the person, the platform, the exit code and a
short prefix of the text, never the target, Hermes' output or the whole
text.

A new proposal is one line like any other message (#778): approvals happen
only in the Inbox, so there is no poll and no vote to count.
POST /ablage (#521) is the Bote's filing: the laib-ablage plugin in Hermes
sends a photo or PDF someone sent with "ablegen", with the same bearer, and
this writes it into Paperless' consume folder as Bote/<sender>/<title>.<ext>
(Paperless makes the folders tags). Hermes runs as uid 10000 and may not
write into the share; this container is the box's user, as Haus is.
Stdlib only: it runs in the stock Hermes image (templates/bote)."""
import base64, hmac, json, os, re, shutil, subprocess, sys, threading, time, urllib.request
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


@dataclass
class Config:
    token: str
    people: str = "/people/people.json"
    send: list = field(default_factory=lambda: ["hermes", "send"])
    hermes_src: str = "/hermes"            # the platform credentials only (post-deploy: bote/notify/.env), read-only
    hermes_home: str = "/tmp/hermes"       # a private copy: Hermes may write next to its config
    max_text: int = 1000
    max_body: int = 4096
    per_person: int = 5                    # per minute
    total: int = 20                        # per minute
    channel_timeout: float = 20            # one channel's hermes send
    deadline: float = 50                   # all channels together; HA's rest_command waits 60 s
    log: object = print
    consume: str = "/consume"              # Paperless' consume folder (#521); missing: /ablage answers 503
    max_file: int = 25 << 20               # one document, as Haus' upload (haus#10)
    owner: tuple = (1000, 1000)            # Paperless' user (paperless.container USERMAP_UID): it removes what it filed


class Limiter:
    def __init__(self):
        self.lock, self.seen = threading.Lock(), {}

    def allow(self, cfg, person):
        now = time.monotonic()
        with self.lock:
            for k in list(self.seen):
                self.seen[k] = [t for t in self.seen[k] if now - t < 60]
            if len(self.seen.get(person, [])) >= cfg.per_person or sum(map(len, self.seen.values())) >= cfg.total:
                return False
            self.seen.setdefault(person, []).append(now)
            return True


_home_lock = threading.Lock()


def prepare_home(cfg):
    """Hermes reads .env and config.yaml from HERMES_HOME. The copy is fresh per
    send (post-deploy may rewrite the source after the pod started) and atomic:
    a parallel send sees the old file or the new one, never half of one."""
    with _home_lock:
        os.makedirs(cfg.hermes_home, mode=0o700, exist_ok=True)
        for f in (".env", "config.yaml"):
            src, dst = os.path.join(cfg.hermes_src, f), os.path.join(cfg.hermes_home, f)
            if os.path.exists(src):
                tmp = dst + ".tmp"
                shutil.copyfile(src, tmp)
                os.chmod(tmp, 0o600)
                os.replace(tmp, dst)


def deliver(cfg, channels, text):
    """The first channel that takes it, in order and within the deadline; None when none did."""
    prepare_home(cfg)
    env = {k: v for k, v in os.environ.items() if k != "NOTIFY_TOKEN"}
    env["HERMES_HOME"] = cfg.hermes_home
    end = time.monotonic() + cfg.deadline
    for ch in channels:
        platform, left = ch.split(":")[0], end - time.monotonic()
        if left <= 0:
            cfg.log(f"mitteilung: {platform} nicht versucht, Frist abgelaufen")
            continue
        try:
            r = subprocess.run([*cfg.send, "--to", ch, "--file", "-"], input=text, text=True, env=env,
                               capture_output=True, timeout=min(cfg.channel_timeout, left))
            if r.returncode == 0:
                return ch
            cfg.log(f"mitteilung: {platform} lehnt ab (rc={r.returncode})")  # Hermes' output names the target
        except (OSError, subprocess.TimeoutExpired) as e:
            cfg.log(f"mitteilung: {platform} nicht erreichbar: {type(e).__name__}")
    return None


def signal_env(cfg):
    """SIGNAL_HTTP_URL and SIGNAL_ACCOUNT from the platform .env; empty without Signal."""
    env = {}
    try:
        with open(os.path.join(cfg.hermes_src, ".env")) as f:
            for l in f:
                k, _, v = l.strip().partition("=")
                if k in ("SIGNAL_HTTP_URL", "SIGNAL_ACCOUNT"):
                    env[k] = v
    except OSError:
        pass
    return env.get("SIGNAL_HTTP_URL", ""), env.get("SIGNAL_ACCOUNT", "")


# what Paperless files and the Bote takes, by its first bytes, with the extension it gets
KINDS = [(b"%PDF-", ".pdf"), (b"\xff\xd8\xff", ".jpg"), (b"\x89PNG\r\n\x1a\n", ".png"), (b"GIF8", ".gif")]
_file_lock = threading.Lock()


def kind(data):
    if data[:4] == b"RIFF" and data[8:12] == b"WEBP":
        return ".webp"
    return next((ext for magic, ext in KINDS if data.startswith(magic)), None)


def plain(name, fallback):
    """a name as a folder or file name: letters, digits, space and ._- (anything else '_'), never hidden, at most 80"""
    name = re.sub(r"[^\w .-]", "_", name or "").strip(" ._-")[:80].strip(" ._-")
    return name or fallback


def sender(cfg, platform, who, name):
    """the person from people.json whose channel this is, else the chat's name for them"""
    try:
        with open(cfg.people) as f:
            for person, channels in json.load(f).items():
                if f"{platform}:{who}" in channels:
                    return person.capitalize()
    except (OSError, ValueError, AttributeError):
        pass
    return plain(name, "Unbekannt")


def file_in(cfg, data, title, person):
    """writes data into the consume folder, whole or not at all: staged under ._* (Paperless
    ignores it), then renamed to a free name; the title it got"""
    ext = kind(data)
    if ext is None:
        raise ValueError("nur Fotos (JPG, PNG, GIF, WebP) und PDFs")
    if not os.path.isdir(cfg.consume):
        raise FileNotFoundError(cfg.consume)
    folder = os.path.join(cfg.consume, "Bote", person)
    with _file_lock:
        for d in (os.path.join(cfg.consume, "Bote"), folder):
            if not os.path.isdir(d):
                os.mkdir(d, 0o755)
                try:
                    os.chown(d, *cfg.owner)
                except OSError:
                    pass
        stage = os.path.join(folder, "._bote-%d.part" % time.monotonic_ns())
        with open(stage, "wb") as f:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        try:
            os.chown(stage, *cfg.owner)
        except OSError:
            pass
        for n in range(1, 100):
            got = title if n == 1 else f"{title} ({n})"
            final = os.path.join(folder, got + ext)
            if not os.path.exists(final):
                os.rename(stage, final)
                return got
        os.remove(stage)
        raise FileExistsError(title)


class Handler(BaseHTTPRequestHandler):
    timeout = 10  # seconds a client may take to send its request

    def reply(self, code, body):
        b = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        """The pod's health (#184): bote-notify is in every Bote; with Signal
        (SIGNAL_HTTP_URL in its .env) signal-cli's daemon must answer /api/v1/check too (#469)."""
        if self.path != "/health":
            return self.reply(404, {"fehler": "unbekannter Pfad"})
        url = signal_env(self.server.cfg)[0]
        if url:
            try:
                urllib.request.urlopen(url.rstrip("/") + "/api/v1/check", timeout=5).close()
            except (OSError, ValueError):
                return self.reply(503, {"fehler": "signal-cli antwortet nicht"})
        return self.reply(200, {"ok": True})

    def do_POST(self):
        cfg = self.server.cfg
        if self.path not in ("/mitteilung", "/ablage"):
            return self.reply(404, {"fehler": "unbekannter Pfad"})
        got = self.headers.get("Authorization", "")
        if not cfg.token or not hmac.compare_digest(got.encode(), ("Bearer " + cfg.token).encode()):
            return self.reply(401, {"fehler": "kein gültiger Token"})
        if self.path == "/ablage":
            return self.ablage(cfg)
        try:
            n = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            n = -1
        if n < 0 or n > cfg.max_body:
            return self.reply(413, {"fehler": f"höchstens {cfg.max_body} Bytes"})
        try:
            msg = json.loads(self.rfile.read(n) or b"null")
            an, text = msg["an"], msg["text"]
        except (ValueError, TypeError, KeyError):
            return self.reply(400, {"fehler": "erwartet: {an, text}"})
        if not isinstance(an, str) or not isinstance(text, str) or not text.strip():
            return self.reply(400, {"fehler": "an und text sind Text, text nicht leer"})
        if "MEDIA:" in text:  # Hermes' attachment marker, case-sensitive as Hermes reads it
            return self.reply(400, {"fehler": "MEDIA: ist kein Text (Hermes hängte eine Datei an)"})
        person = an.strip().lower()
        try:
            with open(cfg.people) as f:
                channels = json.load(f).get(person)
        except (OSError, ValueError):
            channels = None
        if not channels:
            cfg.log(f"mitteilung: an={person[:20]!r} unbekannt")
            return self.reply(404, {"fehler": "unbekannte Person"})
        if not self.server.limiter.allow(cfg, person):
            cfg.log(f"mitteilung: an={person} zu viele")
            return self.reply(429, {"fehler": "zu viele Mitteilungen, später noch einmal"})
        if len(text) > cfg.max_text:
            text = text[:cfg.max_text - 1] + "…"
        ch = deliver(cfg, channels, text)
        kanal = ch.split(":")[0] if ch else ""
        cfg.log(f"mitteilung: an={person} kanal={kanal or '-'} {'ok' if ch else 'fehlgeschlagen'} text={text[:12]!r}…")
        if not ch:
            return self.reply(502, {"fehler": "kein Kanal hat die Mitteilung angenommen"})
        return self.reply(200, {"ok": True, "kanal": kanal})

    def ablage(self, cfg):
        """{titel, plattform, absender, name, daten: base64} → {ok, titel} (#521)"""
        try:
            n = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            n = -1
        if n < 0 or n > cfg.max_file * 4 // 3 + 4096:
            return self.reply(413, {"fehler": "zu groß (höchstens 25 MB)"})
        try:
            msg = json.loads(self.rfile.read(n) or b"null")
            data = base64.b64decode(msg["daten"], validate=True)
            platform, who = str(msg.get("plattform", "")), str(msg.get("absender", ""))
        except (ValueError, TypeError, KeyError):
            return self.reply(400, {"fehler": "erwartet: {titel, plattform, absender, name, daten}"})
        if not data or len(data) > cfg.max_file:
            return self.reply(413, {"fehler": "leer oder zu groß (höchstens 25 MB)"})
        person = sender(cfg, platform, who, str(msg.get("name", "")))
        title = plain(str(msg.get("titel", "")), "Dokument von %s %s" % (person, time.strftime("%Y-%m-%d %H-%M")))
        try:
            got = file_in(cfg, data, title, person)
        except ValueError as e:
            return self.reply(415, {"fehler": str(e)})
        except FileNotFoundError:
            cfg.log("ablage: kein Einwurf-Ordner %s" % cfg.consume)
            return self.reply(503, {"fehler": "Paperless' Einwurf-Ordner fehlt"})
        except OSError as e:
            cfg.log(f"ablage: von={person} nicht geschrieben: {type(e).__name__}")
            return self.reply(500, {"fehler": "der Einwurf-Ordner nimmt nichts an"})
        cfg.log(f"ablage: von={person} {len(data)} Bytes abgelegt")  # never the title: it may say what the letter is
        return self.reply(200, {"ok": True, "titel": got})

    def log_message(self, *a):
        pass  # one line per message from do_POST, no request lines


def server(cfg, addr):
    s = ThreadingHTTPServer(addr, Handler)
    s.cfg, s.limiter = cfg, Limiter()
    return s


def main():
    host, _, port = os.environ.get("NOTIFY_LISTEN", "0.0.0.0:8645").rpartition(":")
    cfg = Config(token=os.environ.get("NOTIFY_TOKEN", ""), hermes_src=os.environ.get("NOTIFY_HERMES", "/hermes"),
                 consume=os.environ.get("NOTIFY_CONSUME", "/consume"), log=lambda m: print(m, flush=True))
    if not cfg.token:
        print("bote-notify: NOTIFY_TOKEN ist leer, jede Mitteilung wird abgelehnt", file=sys.stderr, flush=True)
    print(f"bote-notify: {host}:{port}", flush=True)
    server(cfg, (host, int(port))).serve_forever()


if __name__ == "__main__":
    main()
