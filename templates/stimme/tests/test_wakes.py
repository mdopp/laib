"""#825: the Voice PE woke on „Hey Joshua“ twice in ten minutes, Whisper heard „Ja, ich will push
her backst.“ and „Auf Wiedersehen.“: room talk. stimme-wakes sits between Home Assistant and
Whisper, forwards every byte, counts the wakes per day, keeps the audio of one that was not
addressed to Joshua as a negative for the next training, and „Fehlalarm“ marks the wake before
it. Household audio: 0600, out of the backups, never printed."""
import asyncio, importlib.util, io, json, os, stat, tempfile, time, unittest, wave
from contextlib import redirect_stdout
from pathlib import Path

T = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("stimme_wakes", T / "stimme-wakes.py")
wakes = importlib.util.module_from_spec(spec)
spec.loader.exec_module(wakes)

FMT = (16000, 2, 1)
PCM = b"\x10\x27" * 16000   # one second


class Clock:
    def __init__(self):
        self.t = time.mktime((2026, 10, 27, 18, 39, 0, 0, 0, -1))

    def __call__(self):
        return self.t


class Judge(unittest.TestCase):
    def setUp(self):
        d = Path(tempfile.mkdtemp())
        self.rec, self.micro, self.clock = d / "recordings", d / "micro", Clock()
        self.micro.mkdir()
        self.w = wakes.Wakes(self.rec, self.micro, "hey_joshua", "Hey Joshua", keep=3, clock=self.clock)

    def heard(self, text, later=120):
        self.clock.t += later
        return self.w.heard(text, PCM, FMT)

    def negatives(self):
        return sorted((self.rec / "hey_joshua" / "negativ").glob("*.wav"))

    def today(self):
        return json.loads((self.micro / "laib-wakes.json").read_text())["tage"]["2026-10-27"]

    def test_the_two_false_wakes_from_the_box_are_negatives(self):
        self.assertEqual(self.heard("Ja, ich will push her backst."), "ohne")
        self.assertEqual(self.heard("Auf Wiedersehen."), "ohne")
        self.assertEqual(self.today(), {"geweckt": 2, "ohne": 2})
        self.assertEqual(len(self.negatives()), 2)
        with wave.open(str(self.negatives()[0])) as w:
            self.assertEqual((w.getframerate(), w.getsampwidth(), w.getnchannels(), w.getnframes()), (16000, 2, 1, 16000))

    def test_a_command_or_the_name_is_addressed_and_kept_nowhere(self):
        for text in ("Schalte das Sofalicht aus.", "Mach Bambi an", "Wie wird das Wetter morgen?", "Joshua, wie spät ist es?",
                     "Hey Joschua, stell einen Timer", "Turn off the lights"):
            self.assertEqual(self.heard(text), "befehl", text)
        self.assertEqual(self.negatives(), [])
        self.assertEqual(self.today(), {"geweckt": 6, "ohne": 0})

    def test_nothing_heard_is_a_wake_without_a_command(self):
        self.assertEqual(self.heard(""), "ohne")
        self.assertEqual(self.heard("Untertitel der Amara.org-Community"), "ohne")

    def test_a_follow_up_is_no_wake(self):
        self.heard("Mach das Licht an")
        self.assertEqual(self.heard("Ja", later=8), "folge", "the device listened on after Joshua asked back")
        self.assertEqual(self.today(), {"geweckt": 1, "ohne": 0})
        self.assertEqual(self.negatives(), [])

    def test_fehlalarm_marks_the_wake_before_it(self):
        self.heard("Wie bitte ist das")   # a wake Whisper made a question of: counted as addressed
        self.assertEqual(self.negatives(), [])
        self.assertEqual(self.heard("Fehlalarm.", later=40), "fehlalarm")
        self.assertEqual(len(self.negatives()), 1, "the wake before became a negative")
        self.assertEqual(self.today(), {"geweckt": 2, "ohne": 1})
        self.assertEqual(self.heard("Das war ein Fehlalarm"), "fehlalarm")
        self.assertEqual(len(self.negatives()), 1, "the Fehlalarm itself is never a negative, nor marked twice")

    def test_fehlalarm_as_the_follow_up_of_the_false_wake(self):
        self.heard("Wer hat das gesagt")
        self.assertEqual(self.heard("Fehlalarm", later=6), "fehlalarm")
        self.assertEqual(len(self.negatives()), 1)
        self.assertEqual(self.today(), {"geweckt": 1, "ohne": 1})

    def test_a_late_fehlalarm_marks_nothing(self):
        self.heard("Wie geht das")
        self.heard("Fehlalarm", later=3600)
        self.assertEqual(self.negatives(), [])

    def test_capped_and_rotated_private_and_out_of_the_backups(self):
        for _ in range(5):
            self.heard("Auf Wiedersehen.")
        neg = self.negatives()
        self.assertEqual(len(neg), 3, "the newest NEGATIVES_MAX stay")
        self.assertEqual(json.loads((self.micro / "laib-wakes.json").read_text())["gegenbeispiele"], 3)
        for f in neg:
            self.assertEqual(stat.S_IMODE(f.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(neg[0].parent.stat().st_mode), 0o700)
        tag = (neg[0].parent / "CACHEDIR.TAG").read_text()
        self.assertTrue(tag.startswith("Signature: 8a477f597d28d172789f06886806bc55"), "restic --exclude-caches reads this signature")
        self.assertEqual(self.today(), {"geweckt": 5, "ohne": 5}, "the counts stay what happened")

    def test_the_days_are_kept_two_weeks(self):
        for _ in range(20):
            self.heard("Mach das Licht an", later=86400)
        self.assertEqual(len(json.loads((self.micro / "laib-wakes.json").read_text())["tage"]), 14)


class Forward(unittest.TestCase):
    """every byte to Whisper and back unchanged; the transcript and the audio read on the side"""

    def test_a_transcription_passes_and_is_judged(self):
        d = Path(tempfile.mkdtemp())
        w = wakes.Wakes(d / "rec", d / "micro", "hey_joshua", "Hey Joshua")
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
        neg = list((d / "rec" / "hey_joshua" / "negativ").glob("*.wav"))
        self.assertEqual(len(neg), 1)
        with wave.open(str(neg[0])) as f:
            self.assertEqual(f.readframes(f.getnframes()), PCM)
        self.assertNotIn("Wiedersehen", out.getvalue(), "what the household said is never printed")

    def test_an_event_it_cannot_read_is_still_forwarded(self):
        e = wakes.Events()
        with self.assertRaises(ValueError):
            e.feed(b"not json\n")

    def test_the_unit_and_the_contract(self):
        u = (T / "stimme-wakes.container").read_text()
        self.assertIn("Environment=WAKES_LISTEN=127.0.0.1:10301", u)
        self.assertIn("Environment=WHISPER=127.0.0.1:10300", u)
        self.assertIn('Environment="WAKE_PHRASE=${PERSONA_PHRASE}"', u)
        self.assertIn("Volume=${DATA_DIR}/stimme/recordings:/recordings:z", u)
        self.assertIn("{port: 10301, lan: false}", (T / "laib.yml").read_text())
        self.assertNotIn("print(text", (T / "stimme-wakes.py").read_text())


if __name__ == "__main__":
    unittest.main()
