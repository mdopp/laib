"""#825: the Voice PE woke twice in ten minutes, Whisper heard „Ja, ich will push her backst.“
and „Auf Wiedersehen.“: room talk. stimme-wakes sits between Home Assistant and Whisper,
forwards every byte, counts the wakes per day and those without a command, and „Fehlalarm“
marks the wake before it. Nothing it hears is kept or printed."""
import asyncio, importlib.util, io, json, tempfile, time, unittest
from contextlib import redirect_stdout
from pathlib import Path

T = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("stimme_wakes", T / "stimme-wakes.py")
wakes = importlib.util.module_from_spec(spec)
spec.loader.exec_module(wakes)

PCM = b"\x10\x27" * 16000   # one second


class Clock:
    def __init__(self):
        self.t = time.mktime((2026, 10, 27, 18, 39, 0, 0, 0, -1))

    def __call__(self):
        return self.t


class Judge(unittest.TestCase):
    def setUp(self):
        d = Path(tempfile.mkdtemp())
        self.micro, self.clock = d / "micro", Clock()
        self.micro.mkdir()
        self.w = wakes.Wakes(self.micro, "Hey Mycroft", clock=self.clock)

    def heard(self, text, later=120):
        self.clock.t += later
        return self.w.heard(text)

    def today(self):
        return json.loads((self.micro / "laib-wakes.json").read_text())["tage"]["2026-10-27"]

    def test_the_two_false_wakes_from_the_box_are_counted_without_a_command(self):
        self.assertEqual(self.heard("Ja, ich will push her backst."), "ohne")
        self.assertEqual(self.heard("Auf Wiedersehen."), "ohne")
        self.assertEqual(self.today(), {"geweckt": 2, "ohne": 2})

    def test_a_command_or_the_name_is_addressed_and_kept_nowhere(self):
        for text in ("Schalte das Sofalicht aus.", "Mach Bambi an", "Wie wird das Wetter morgen?", "Mycroft, wie spät ist es?",
                     "Hey Mycroft, stell einen Timer", "Turn off the lights"):
            self.assertEqual(self.heard(text), "befehl", text)
        self.assertEqual(self.today(), {"geweckt": 6, "ohne": 0})

    def test_nothing_heard_is_a_wake_without_a_command(self):
        self.assertEqual(self.heard(""), "ohne")
        self.assertEqual(self.heard("Untertitel der Amara.org-Community"), "ohne")

    def test_a_follow_up_is_no_wake(self):
        self.heard("Mach das Licht an")
        self.assertEqual(self.heard("Ja", later=8), "folge", "the device listened on after the assistant asked back")
        self.assertEqual(self.today(), {"geweckt": 1, "ohne": 0})

    def test_fehlalarm_marks_the_wake_before_it(self):
        self.heard("Wie bitte ist das")   # a wake Whisper made a question of: counted as addressed
        self.assertEqual(self.heard("Fehlalarm.", later=40), "fehlalarm")
        self.assertEqual(self.today(), {"geweckt": 2, "ohne": 1}, "the wake before is one without a command")
        self.assertEqual(self.heard("Das war ein Fehlalarm"), "fehlalarm")
        self.assertEqual(self.today(), {"geweckt": 3, "ohne": 1}, "the Fehlalarm itself is never marked, nor the wake twice")

    def test_fehlalarm_as_the_follow_up_of_the_false_wake(self):
        self.heard("Wer hat das gesagt")
        self.assertEqual(self.heard("Fehlalarm", later=6), "fehlalarm")
        self.assertEqual(self.today(), {"geweckt": 1, "ohne": 1})

    def test_a_late_fehlalarm_marks_nothing(self):
        self.heard("Wie geht das")
        self.heard("Fehlalarm", later=3600)
        self.assertEqual(self.today(), {"geweckt": 2, "ohne": 0})

    # Ablauf L-47
    def test_frage_marks_the_wake_it_answered_with_a_tone(self):
        self.assertEqual(self.heard("Was macht der denn da?"), "befehl", "starts with a question word: counted addressed")
        self.clock.t += 3
        self.assertEqual(self.w.overheard(), "ohne")
        self.assertEqual(self.today(), {"geweckt": 1, "ohne": 1})
        self.assertEqual(self.w.overheard(), "ohne")
        self.assertEqual(self.today(), {"geweckt": 1, "ohne": 1}, "a wake counts once")
        self.heard("Auch ein netter Schlaffler.")    # counted without a command already
        self.w.overheard()
        self.assertEqual(self.today(), {"geweckt": 2, "ohne": 2})

    def test_frage_marks_no_follow_up_and_nothing_late(self):
        self.heard("Mach das Licht an")
        self.heard("Und noch was", later=8)       # a follow-up: the wake before stays a command
        self.assertEqual(self.w.overheard(), "keine Weckung dazu")
        self.heard("Wie spät ist es")
        self.clock.t += 600
        self.assertEqual(self.w.overheard(), "keine Weckung dazu")
        self.assertEqual(self.today(), {"geweckt": 2, "ohne": 0})

    def test_the_days_are_kept_two_weeks(self):
        for _ in range(20):
            self.heard("Mach das Licht an", later=86400)
        self.assertEqual(len(json.loads((self.micro / "laib-wakes.json").read_text())["tage"]), 14)


class Forward(unittest.TestCase):
    """every byte to Whisper and back unchanged; the transcript read on the side"""

    def test_a_transcription_passes_and_is_judged(self):
        d = Path(tempfile.mkdtemp())
        w = wakes.Wakes(d / "micro", "Hey Mycroft")
        (d / "micro").mkdir()

        def ev(kind, data=None, payload=b""):
            raw = json.dumps(data or {}).encode()
            head = {"type": kind, "data_length": len(raw), "payload_length": len(payload), "version": "1.5.4"}
            return json.dumps(head).encode() + b"\n" + raw + payload

        client = ev("transcribe", {"language": "de"}) + ev("audio-start", {"rate": 16000, "width": 2, "channels": 1}) + \
            b"".join(ev("audio-chunk", {"rate": 16000, "width": 2, "channels": 1}, PCM[i:i + 2048]) for i in range(0, len(PCM), 2048)) + ev("audio-stop")
        answer = ev("transcript", {"text": "Auf Wiedersehen."})
        got = {}

        async def whisper(reader, writer):
            got["whisper"] = await reader.readexactly(len(client))
            writer.write(answer)
            await writer.drain()
            writer.close()

        async def run():
            ws = await asyncio.start_server(whisper, "127.0.0.1", 0)
            proxy = await asyncio.start_server(wakes.serve(w, ws.sockets[0].getsockname()[:2]), "127.0.0.1", 0)
            r, wr = await asyncio.open_connection(*proxy.sockets[0].getsockname()[:2])
            for i in range(0, len(client), 777):   # events split across reads
                wr.write(client[i:i + 777])
            await wr.drain()
            got["ha"] = await asyncio.wait_for(r.read(), 10)
            wr.close()
            ws.close()
            proxy.close()

        out = io.StringIO()
        with redirect_stdout(out):
            asyncio.run(run())
        self.assertEqual((got["whisper"], got["ha"]), (client, answer), "a byte changed on the way")
        self.assertEqual(json.loads((d / "micro" / "laib-wakes.json").read_text())["tage"].popitem()[1], {"geweckt": 1, "ohne": 1})
        self.assertNotIn("Wiedersehen", out.getvalue(), "what the household said is never printed")

    # Ablauf L-47
    def test_frages_signal_is_one_line_and_nothing_else(self):
        d = Path(tempfile.mkdtemp())
        (d / "micro").mkdir()
        w = wakes.Wakes(d / "micro", "Hey Mycroft")
        w.heard("Was macht der denn da?")

        async def run():
            sig = await asyncio.start_server(wakes.signals(w), "127.0.0.1", 0)
            for line in (b"irgendwas\n", b"ohne\n"):
                r, wr = await asyncio.open_connection(*sig.sockets[0].getsockname()[:2])
                wr.write(line)
                await wr.drain()
                await asyncio.wait_for(r.read(), 5)   # the server closes once it read the line
                wr.close()
            sig.close()

        out = io.StringIO()
        with redirect_stdout(out):
            asyncio.run(run())
        self.assertEqual(json.loads((d / "micro" / "laib-wakes.json").read_text())["tage"].popitem()[1], {"geweckt": 1, "ohne": 1})
        self.assertEqual(out.getvalue().strip(), "stimme-wakes: Frage: ohne")

    def test_an_event_it_cannot_read_is_still_forwarded(self):
        e = wakes.Events()
        with self.assertRaises(ValueError):
            e.feed(b"not json\n")

    def test_the_unit_and_the_contract(self):
        u = (T / "stimme-wakes.container").read_text()
        self.assertIn("Environment=WAKES_LISTEN=127.0.0.1:10301", u)
        self.assertIn("Environment=WHISPER=127.0.0.1:10300", u)
        self.assertIn('Environment="WAKE_PHRASE=Hey Mycroft"', u)
        self.assertNotIn("/recordings", u, "no audio is kept")
        self.assertIn("{port: 10301, lan: false}", (T / "laib.yml").read_text())
        self.assertIn("Environment=SIGNAL_LISTEN=0.0.0.0:10302", u, "Frage's pod reaches it at host.containers.internal")
        self.assertIn("{port: 10302, lan: false}", (T / "laib.yml").read_text(), "never the LAN")
        self.assertNotIn("print(text", (T / "stimme-wakes.py").read_text())


if __name__ == "__main__":
    unittest.main()
