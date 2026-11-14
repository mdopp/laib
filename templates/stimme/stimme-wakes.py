"""The wakes (#825): between Home Assistant and Whisper, it counts them.

The Voice PE woke twice in ten minutes on its first day, and Whisper heard „Auf
Wiedersehen.“: room talk, nobody had called. To judge whether the wake word wakes too
often, the owner needs numbers.

The device wakes on itself (microWakeWord, „Hey Mycroft“ from its firmware) and streams
what follows to Home Assistant, which hands it to Whisper over Wyoming. This sits in that
path: Home Assistant's Whisper entry points at it (WAKES_LISTEN, 127.0.0.1:10301,
home-assistant's post-deploy prefers it when it answers), and every byte goes on to Whisper
(WHISPER, 127.0.0.1:10300) and back unchanged. A copy of the events is read on the side; an
event it cannot read stops the reading, never the forwarding.

For each transcript:

- a follow-up (a transcription within FOLLOWUP_SECONDS of the last one, the device listening
  on after the assistant asked back) is no wake and is not judged;
- „Fehlalarm“ (a short sentence with it) marks the wake before it, within FEHLALARM_SECONDS,
  as one without a command; the Fehlalarm itself is a wake with a command;
- otherwise it is a wake. It is addressed when its text starts with the wake word's name (the
  phrase's words but a greeting) or with a command the pipeline handles (COMMANDS: the verbs
  and question words HA's intents and Frage answer); anything else, an empty or a
  hallucinated transcript too, is a wake without a command.

Frage judges the sentences that reach it too (frage/gate.go): one nobody meant gets a tone instead of an
answer, and Frage says „ohne“ on SIGNAL_LISTEN (0.0.0.0:10302, for the frage pod; one line, nothing of the
text). The wake that transcript was, the last one and within FOLLOWUP_SECONDS, then counts as one without a
command, if it did not already.

The counts per day go to /micro/laib-wakes.json, which stimme-names writes into Stimme's
status line. Nothing it hears is kept or printed: no transcript, no audio, only counts. The
standard library only.
"""
import asyncio
import json
import os
import pathlib
import re
import sys
import time

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


def words(text: str) -> list:
    return SPOKEN.findall((text or "").lower())


def names(phrase: str) -> set:
    """the wake word's name as Whisper may write it: „Hey Mycroft“ -> mycroft"""
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
    """what the transcripts mean: counts, the last wake for a Fehlalarm"""

    def __init__(self, micro: pathlib.Path, phrase: str, followup: float = 30, window: float = 600, clock=time.time):
        self.counts = micro / "laib-wakes.json"
        self.called, self.followup, self.window, self.clock = names(phrase), followup, window, clock
        self.last_end, self.last = None, None   # the last transcription's time; the last wake (time, day, without)

    def heard(self, text: str) -> str:
        """befehl | ohne | fehlalarm | folge: what this transcript was, after counting it"""
        now = self.clock()
        follow = self.last_end is not None and now - self.last_end < self.followup
        self.last_end = now
        day = time.strftime("%Y-%m-%d", time.localtime(now))
        if fehlalarm(text):
            prev, self.last = self.last, None
            if prev and now - prev["at"] < self.window and not prev["ohne"]:
                self.count(prev["day"], ohne=1)
            if not follow:
                self.count(day, geweckt=1)
            return "fehlalarm"
        if follow:
            return "folge"
        ohne = not addressed(text, self.called)
        self.last = {"at": now, "day": day, "ohne": ohne}
        self.count(day, geweckt=1, ohne=int(ohne))
        return "ohne" if ohne else "befehl"

    def overheard(self) -> str:
        """Frage's „ohne“: the last transcript, if it was a wake (no follow-up) a moment ago, had no command"""
        prev = self.last
        if not prev or prev["at"] != self.last_end or self.clock() - prev["at"] > self.followup:
            return "keine Weckung dazu"
        if not prev["ohne"]:
            prev["ohne"] = True
            self.count(prev["day"], ohne=1)
        return "ohne"

    def count(self, day: str, geweckt: int = 0, ohne: int = 0) -> None:
        try:
            c = json.loads(self.counts.read_text())
        except (OSError, ValueError):
            c = {}
        tage = c.get("tage") or {}
        t = tage.setdefault(day, {"geweckt": 0, "ohne": 0})
        t["geweckt"], t["ohne"] = t.get("geweckt", 0) + geweckt, t.get("ohne", 0) + ohne
        c["tage"] = {d: tage[d] for d in sorted(tage)[-14:]}
        c.pop("gegenbeispiele", None)
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
        self.wakes = wakes

    def see(self, kind, data, payload) -> None:
        if kind == "transcript":
            print(f"stimme-wakes: {self.wakes.heard(str(data.get('text') or ''))}", flush=True)


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


def signals(wakes: Wakes):
    async def handle(reader, writer):
        try:
            if (await asyncio.wait_for(reader.readline(), 5)).strip() == b"ohne":
                print(f"stimme-wakes: Frage: {wakes.overheard()}", flush=True)
        except (asyncio.TimeoutError, OSError, ValueError):
            pass
        finally:
            writer.close()
    return handle


def hostport(s: str) -> tuple:
    host, _, port = s.rpartition(":")
    return host, int(port)


async def main() -> None:
    e = os.environ
    wakes = Wakes(pathlib.Path(e.get("MICRO", "/micro")), e["WAKE_PHRASE"])
    host, port = hostport(e.get("WAKES_LISTEN", "127.0.0.1:10301"))
    server = await asyncio.start_server(serve(wakes, hostport(e.get("WHISPER", "127.0.0.1:10300"))), host, port)
    sig = await asyncio.start_server(signals(wakes), *hostport(e.get("SIGNAL_LISTEN", "0.0.0.0:10302")))
    print(f"stimme-wakes: on {host}:{port}, Whisper behind it; Frage's signal on {e.get('SIGNAL_LISTEN', '0.0.0.0:10302')}", flush=True)
    async with server, sig:
        await asyncio.gather(server.serve_forever(), sig.serve_forever())


if __name__ == "__main__":
    if not os.environ.get("WAKE_PHRASE"):
        print("stimme-wakes: no WAKE_PHRASE (the wake word)", file=sys.stderr)
        sys.exit(2)
    asyncio.run(main())
