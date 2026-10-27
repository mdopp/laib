"""The wakes (#825): between Home Assistant and Whisper, it counts them and keeps a false one's audio.

The Voice PE woke on „Hey Joshua“ twice in ten minutes on its first day, and Whisper heard
„Ja, ich will push her backst.“ and „Auf Wiedersehen.“: room talk, nobody had called. The
model had only ever heard Piper's voices. To learn this household's rooms it needs their
sounds as negatives, and to judge its cutoff the owner needs numbers.

The device wakes on itself (microWakeWord) and streams what follows to Home Assistant, which
hands it to Whisper over Wyoming. This sits in that path, the one place that has the audio
and its transcript together: Home Assistant's Whisper entry points at it (WAKES_LISTEN,
127.0.0.1:10301, home-assistant's post-deploy prefers it when it answers), and every byte
goes on to Whisper (WHISPER, 127.0.0.1:10300) and back unchanged. A copy of the events is
read on the side; an event it cannot read stops the reading, never the forwarding.

For each transcript:

- a follow-up (a transcription within FOLLOWUP_SECONDS of the last one, the device listening
  on after Joshua asked back) is no wake and is not judged;
- „Fehlalarm“ (a short sentence with it) marks the wake before it, within FEHLALARM_SECONDS,
  as false: its audio, kept in memory until then, becomes a negative; the Fehlalarm itself is
  a wake with a command;
- otherwise it is a wake. It is addressed when its text starts with the persona's name (the
  phrase's words but a greeting) or with a command the pipeline handles (COMMANDS: the verbs
  and question words HA's intents and Frage answer); anything else, an empty or a
  hallucinated transcript too, is a wake without a command, and its audio is a negative.

The negatives go to /recordings/<wake id>/negativ/<time>.wav, 0600, the newest NEGATIVES_MAX
kept, the oldest deleted. The folder carries a CACHEDIR.TAG, so the nightly restic run
(--exclude-caches) leaves them out: household audio stays on the box. The trainer folds them
in as negatives on its next run (stimme/trainer); nothing here starts one. The counts per
day go to /micro/laib-wakes.json, which stimme-names writes into Stimme's status line.

Nothing it hears is printed: no transcript, no audio, only counts. The standard library only.
"""
import asyncio
import json
import os
import pathlib
import re
import sys
import time
import wave

COMMANDS = set("""
schalte schalt schalten mach mache macht stell stelle stellt setz setze setzt dreh drehe öffne öffnen schließe schließ schliesse
dimme dimm spiel spiele spielt stopp stop stoppe pausiere pause weiter nächstes nächster nächste zurück lauter leiser starte start
beende brich abbrechen erinnere erinner füge füg trag trage notiere zeig zeige sag sage erzähl erzähle lies ruf rufe kauf kaufe
aktiviere deaktiviere fahr fahre wie was wann wo wohin woher wer wen wem welche welcher welches welchen warum wieso weshalb
wieviel wieviele ist sind gibt gib hast kannst könntest würdest bitte licht timer wecker danke fehlalarm guten gute
turn switch set what how when where who which why play pause stop open close start is are tell please
""".split())
GREETINGS = {"hey", "hei", "hallo", "hi", "ok", "okay", "na", "he"}
SPOKEN = re.compile(r"[a-zäöüß]+")
MAX_SECONDS = 15   # of a negative: what follows the wake, not a whole conversation


def words(text: str) -> list:
    return SPOKEN.findall((text or "").lower())


def names(phrase: str) -> set:
    """the persona's name as Whisper may write it: „Hey Joshua“ -> joshua, joschua"""
    out = {w for w in words(phrase) if w not in GREETINGS}
    return out | {w.replace("sh", "sch") for w in out}


def addressed(text: str, called: set) -> bool:
    w = words(text)
    while w and w[0] in GREETINGS and len(w) > 1:
        w = w[1:]
    return bool(w) and (w[0] in called or w[0] in COMMANDS)


def fehlalarm(text: str) -> bool:
    w = words(text)
    return "fehlalarm" in w and len(w) <= 5


class Wakes:
    """what the transcripts mean: counts, negatives, the last wake for a Fehlalarm"""

    def __init__(self, recordings: pathlib.Path, micro: pathlib.Path, wid: str, phrase: str, keep: int = 200,
                 followup: float = 30, window: float = 600, clock=time.time):
        self.neg, self.counts = recordings / wid / "negativ", micro / "laib-wakes.json"
        self.called, self.keep, self.followup, self.window, self.clock = names(phrase), keep, followup, window, clock
        self.last_end, self.last = None, None   # the last transcription's time; the last wake (time, day, audio, fmt, without)

    def heard(self, text: str, pcm: bytes, fmt: tuple) -> str:
        """befehl | ohne | fehlalarm | folge: what this transcript was, after counting it"""
        now = self.clock()
        follow = self.last_end is not None and now - self.last_end < self.followup
        self.last_end = now
        day = time.strftime("%Y-%m-%d", time.localtime(now))
        if fehlalarm(text):
            prev, self.last = self.last, None
            if prev and now - prev["at"] < self.window and not prev["ohne"]:
                self.save(prev["pcm"], prev["fmt"], prev["at"])
                self.count(prev["day"], ohne=1)
            if not follow:
                self.count(day, geweckt=1)
            return "fehlalarm"
        if follow:
            return "folge"
        ohne = not addressed(text, self.called)
        self.last = {"at": now, "day": day, "pcm": pcm, "fmt": fmt, "ohne": ohne}
        if ohne:
            self.save(pcm, fmt, now)
        self.count(day, geweckt=1, ohne=int(ohne))
        return "ohne" if ohne else "befehl"

    def save(self, pcm: bytes, fmt: tuple, at: float) -> None:
        rate, width, channels = fmt
        if not pcm or not rate or not width or not channels:
            return
        self.neg.mkdir(mode=0o700, parents=True, exist_ok=True)
        tag = self.neg / "CACHEDIR.TAG"
        if not tag.exists():   # restic --exclude-caches: the household's audio stays out of the backups
            tag.write_text("Signature: 8a477f597d28d172789f06886806bc55\n# laib: household audio, kept on the box only (#825)\n")
        name = time.strftime("%Y%m%dT%H%M%S", time.gmtime(at)) + f"{int(at * 1000) % 1000:03d}.wav"
        tmp = self.neg / (name + ".tmp")
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "wb") as f, wave.open(f, "wb") as w:
            w.setnchannels(channels)
            w.setsampwidth(width)
            w.setframerate(rate)
            w.writeframes(pcm)
        tmp.replace(self.neg / name)
        for old in sorted(self.neg.glob("*.wav"))[:-self.keep or None]:
            old.unlink()

    def count(self, day: str, geweckt: int = 0, ohne: int = 0) -> None:
        try:
            c = json.loads(self.counts.read_text())
        except (OSError, ValueError):
            c = {}
        tage = c.get("tage") or {}
        t = tage.setdefault(day, {"geweckt": 0, "ohne": 0})
        t["geweckt"], t["ohne"] = t.get("geweckt", 0) + geweckt, t.get("ohne", 0) + ohne
        c["tage"] = {d: tage[d] for d in sorted(tage)[-14:]}
        c["gegenbeispiele"] = len(list(self.neg.glob("*.wav"))) if self.neg.is_dir() else 0
        c.setdefault("seit", time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(self.clock())))
        tmp = self.counts.with_name(self.counts.name + ".tmp")
        tmp.write_text(json.dumps(c, ensure_ascii=False) + "\n")
        tmp.replace(self.counts)


class Events:
    """Wyoming's framing: a JSON line, then data_length bytes of more data, then payload_length bytes"""

    def __init__(self):
        self.buf, self.head = b"", None

    def feed(self, data: bytes) -> list:
        self.buf += data
        out = []
        while True:
            if self.head is None:
                i = self.buf.find(b"\n")
                if i < 0:
                    if len(self.buf) > 1 << 20:
                        raise ValueError("no event line in 1 MiB")
                    return out
                self.head, self.buf = json.loads(self.buf[:i]), self.buf[i + 1:]
            dl, pl = int(self.head.get("data_length") or 0), int(self.head.get("payload_length") or 0)
            if len(self.buf) < dl + pl:
                return out
            d = dict(self.head.get("data") or {})
            if dl:
                d.update(json.loads(self.buf[:dl]))
            out.append((self.head.get("type"), d, self.buf[dl:dl + pl]))
            self.buf, self.head = self.buf[dl + pl:], None


class Run:
    """one connection's transcriptions, from both directions' events"""

    def __init__(self, wakes: Wakes):
        self.wakes, self.pcm, self.fmt, self.cap = wakes, bytearray(), (0, 0, 0), 0

    def see(self, kind, data, payload) -> None:
        if kind == "audio-start":
            self.pcm = bytearray()
            self.fmt = (int(data.get("rate") or 0), int(data.get("width") or 0), int(data.get("channels") or 0))
            self.cap = MAX_SECONDS * self.fmt[0] * self.fmt[1] * self.fmt[2]
        elif kind == "audio-chunk":
            if len(self.pcm) < self.cap:
                self.pcm += payload[:self.cap - len(self.pcm)]
        elif kind == "transcript":
            said = self.wakes.heard(str(data.get("text") or ""), bytes(self.pcm), self.fmt)
            self.pcm = bytearray()
            print(f"stimme-wakes: {said}", flush=True)


async def pump(src, dst, events: Events, run: Run) -> None:
    reading = True
    try:
        while data := await src.read(65536):
            dst.write(data)
            await dst.drain()
            if reading:
                try:
                    for ev in events.feed(data):
                        run.see(*ev)
                except (ValueError, TypeError, KeyError, OSError) as err:
                    reading = False   # forwarding goes on; this connection is not judged
                    print(f"stimme-wakes: an event it cannot read ({type(err).__name__}), this connection is only forwarded", flush=True)
    finally:
        dst.close()


def serve(wakes: Wakes, whisper: tuple):
    async def handle(reader, writer):
        try:
            ur, uw = await asyncio.open_connection(*whisper)
        except OSError:
            writer.close()   # Whisper is away (the GPU lease, a restart): Home Assistant sees it as before
            return
        run = Run(wakes)
        await asyncio.gather(pump(reader, uw, Events(), run), pump(ur, writer, Events(), run), return_exceptions=True)
    return handle


def hostport(s: str) -> tuple:
    host, _, port = s.rpartition(":")
    return host, int(port)


async def main() -> None:
    e = os.environ
    wakes = Wakes(pathlib.Path(e.get("RECORDINGS", "/recordings")), pathlib.Path(e.get("MICRO", "/micro")), e["WAKE_ID"],
                  e.get("WAKE_PHRASE", ""), keep=int(e.get("NEGATIVES_MAX") or 200))
    host, port = hostport(e.get("WAKES_LISTEN", "127.0.0.1:10301"))
    server = await asyncio.start_server(serve(wakes, hostport(e.get("WHISPER", "127.0.0.1:10300"))), host, port)
    print(f"stimme-wakes: on {host}:{port}, Whisper behind it", flush=True)
    async with server:
        await server.serve_forever()


if __name__ == "__main__":
    if not os.environ.get("WAKE_ID"):
        print("stimme-wakes: no WAKE_ID (the house persona's wake word)", file=sys.stderr)
        sys.exit(2)
    asyncio.run(main())
