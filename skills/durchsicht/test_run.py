"""durchsicht's script (#551, #839): the classifier and the review, without the runner.
The runner-level tests are operator/durchsicht551_test.go."""

import importlib.util
import json
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
import laibskill  # noqa: E402

spec = importlib.util.spec_from_file_location("durchsicht", os.path.join(HERE, "run.py"))
run = importlib.util.module_from_spec(spec)
spec.loader.exec_module(run)


class Fake:
    """A session whose laib answers status and logs from a day."""

    def __init__(self, day, state=None, status=None, extra=(), ignore=None):
        self.day, self.state, self.status, self.extra, self.calls = day, state, status, list(extra), []
        self.ignore = ignore  # ignore.yml as laib state parses it; None: no such file on main

    def call(self, tool, **args):
        self.calls.append((tool, args))
        if tool == "laib_status":
            if self.status is not None:
                return laibskill.Reply(self.status, True)
            return laibskill.Reply(json.dumps({"services": [{"name": n} for n in list(self.day) + self.extra]}))
        if tool == "laib_logs":
            svc = args["args"].split(" ")[0]
            if svc not in self.day:
                return laibskill.Reply("no such service", True)
            return laibskill.Reply(json.dumps(self.day[svc]))
        if tool == "laib_state":
            if self.ignore is None:
                return laibskill.Reply("laib: ignore.yml is not a file on main (laib state lists them; secrets.age is never shown)", True)
            if isinstance(self.ignore, str):
                return laibskill.Reply(self.ignore, True)
            return laibskill.Reply(json.dumps({"path": "ignore.yml", "content": "", "redacted": [], "data": self.ignore}))
        return laibskill.Reply("ok")


def line(unit, msg, i=0):
    return f"2026-10-21T03:{i % 60:02d}:00+02:00 kraftwerk {unit}[{1000 + i}]: {msg}"


def day(timeouts):
    adguard = "\n".join(line("adguard", f"2026/10/21 03:00:{i % 60:02d}.{i:06d} [error] dnsproxy: upstream 192.168.178.1:53 failed: read udp 10.88.0.4:{40000 + i}->192.168.178.1:53: i/o timeout", i) for i in range(timeouts))
    radicale = "\n".join(line("radicale", f"[2026-10-21 03:00:{i % 60:02d} +0200] [INFO] Successful login: 'cdopp'", i) for i in range(50))
    once = line("immich-server", "[Nest] 7  - 10/21/2026, 4:00:00 AM   ERROR [Api:DatabaseRepository] database connection lost")
    return {"adguard": {"adguard.service": adguard}, "radicale": {"radicale.service": radicale}, "immich": {"immich-server.service": once}}


class LogLinePatterns(unittest.TestCase):
    # the classifier (#551 point 7): the level a line names decides, else its
    # words; numbers, ids, paths and timestamps are not part of a pattern
    CASES = [
        ("[2026-10-21 03:11:19 +0200] [1/Thread-7 (process_request_thread)] [INFO] Successful login: 'cdopp'", "", False),
        ('INFO:     10.88.0.3:50111 - "POST /v1/audio/transcriptions HTTP/1.1" 500 Internal Server Error', "", False),
        ("[2026-10-21 03:11:19,120] [INFO] [paperless.consumer] Consuming error-report.pdf", "", False),
        ("srv  update_slots: id  0 | task 4711 | prompt done, n_past = 812", "", False),
        ("2026/10/21 03:11:19.123456 [error] dnsproxy: exchanging with 192.168.178.1:53 over udp: i/o timeout", "[error] dnsproxy: exchanging with <ip> over udp: i/o timeout", True),
        ("[03:11:19] [ERR] [42] MediaBrowser.Providers.Music.AudioFileProber: End of file: /media/cdopp/music/8f3a9c2e41b7.1.aac", "[ERR] [<n>] MediaBrowser.Providers.Music.AudioFileProber: End of file: <pfad>", True),
        ("[03:11:19] [WRN] [12] Jellyfin.Server: slow response", "", False),
        ('{"level":"error","ts":1729560679.1,"logger":"http.handlers.reverse_proxy","msg":"aborting with incomplete response","request":{"remote_ip":"192.168.178.20"},"error":"reading: context canceled"}', "http.handlers.reverse_proxy: aborting with incomplete response (reading: context canceled)", True),
        ("2026-10-21 03:11:19.552 ERROR (MainThread) [homeassistant.components.websocket_api.http.connection] [140234] Unauthorized: Bote", "ERROR (MainThread) [homeassistant.components.websocket_api.http.connection] [<n>] Unauthorized: Bote", True),
        ("AttributeError: 'NoneType' object has no attribute 'speech'", "AttributeError: 'NoneType' object has no attribute 'speech'", True),
        ("sse: write tcp 127.0.0.1:8650->127.0.0.1:51234: write: transport endpoint is not connected", "sse: write tcp <ip>-><ip>: write: transport endpoint is not connected", True),
        ("thumbnail 0b5c7f2e-9d41-4c3a-b8e6-1f2a3b4c5d6e failed after 3 attempts", "thumbnail <id> failed after <n> attempts", True),
        ("    at Sharp.toFile (/usr/src/app/node_modules/sharp/lib/output.js:90:5)", "", False),
        # Go's json matched the keys without case, and a key of another type made it no JSON line
        ('{"Level":"ERROR","Msg":"disk full"}', "disk full", True),
        ('{"level":"info","msg":"ok","error":{"x":1}}', "", False),
        ("abc123def is ok", "", False),
    ]

    def test_patterns(self):
        for msg, pattern, real in self.CASES:
            with self.subTest(msg=msg):
                got, is_real = run.log_pattern(msg)
                self.assertEqual(is_real, real)
                if real:
                    self.assertEqual(got, pattern)

    def test_unicode_is_not_a_word_or_digit(self):
        # Go's \\d and \\w are ASCII: an Arabic-Indic digit is no number
        self.assertEqual(run.log_pattern("ERROR ٣ Ärger at 12")[0], "ERROR ٣ Ärger at <n>")


class Review(unittest.TestCase):
    def test_new_then_recurring_then_nothing_then_growing(self):
        s = Fake(day(40))
        r = run.run(s)
        self.assertEqual(r["outcome"], "")
        self.assertIn("neu, adguard.service, 40×", r["context"])
        self.assertIn("Rauschen (stderr ohne Fehler, nur gezählt): radicale.service 50.", r["context"])
        self.assertNotIn("database connection lost", r["context"])  # once is no pattern yet
        self.assertEqual([c for c in s.calls if c[0] == "laib_logs"][0][1], {"args": "adguard --since 24h --lines 5000 --priority warning"})

        r = run.run(Fake(day(40), state=r["state"]))
        self.assertIn("wiederkehrend, immich-server.service, 1×", r["context"])
        self.assertNotIn("adguard.service", r["context"])
        self.assertIn("Schon gemeldet und nicht gewachsen: 1 Muster", r["context"])

        r = run.run(Fake(day(40), state=r["state"]))
        self.assertEqual((r["outcome"], r["why"]), ("unchanged", "keine neuen Fehlermuster"))

        r = run.run(Fake(day(200), state=r["state"]))
        self.assertIn("wächst (vorher 40×), adguard.service, 200×", r["context"])

    def test_unreadable_status_and_logs_are_said(self):
        r = run.run(Fake({}, status="laib away\nmore"))
        self.assertEqual(r["context"], "Protokoll-Durchsicht: laib status nicht lesbar (laib away). Schreib das in die Notiz.")
        self.assertIsNone(r["state"])  # the last review stays the memory
        r = run.run(Fake(day(1), extra=["nas"]))
        self.assertIn("Logs nicht lesbar: nas.", r["context"])

    def test_a_full_unit_is_at_least(self):
        many = "\n".join(line("caddy", f'{{"level":"error","logger":"x","msg":"boom {i}"}}', i) for i in range(5000))
        r = run.run(Fake({"caddy": {"caddy.service": many}}))
        self.assertIn("caddy.service, mindestens 5000×: x: boom <n>", r["context"])



# #903: the container runtime's network layer, as the box writes it
PASTA = [
    "2026-10-29T05:25:00+00:00 atHome-Server pasta[2914928]: Flow 0 (TCP connection (spliced)): ERROR on epoll_ctl()",
    "2026-10-29T05:25:01+00:00 atHome-Server pasta[3452444]: : Transport endpoint is not connected",
    "2026-10-29T05:25:01+00:00 atHome-Server pasta[3452444]: Flow 5 (TCP connection (spliced)): shutdown() on HOST",
    "2026-10-30T01:15:02+00:00 atHome-Server conmon[2413150]: conmon 9a0522c5e760d6b2947c <nwarn>: Failed to open cgroups file: /sys/fs/cgroup/user.slice/x/memory.events",
]
# #901: the one PTR the FritzBox drops
PTR = '2026/10/29 21:00:{:02d}.000000 [error] dnsproxy: exchange failed upstream=192.168.178.1:53 question=";1.255.16.172.in-addr.arpa. IN PTR" duration=2.00s err="read udp 10.88.0.4:40000->192.168.178.1:53: i/o timeout"'
HARMLESS = {"logs": [{"service": "adguard", "match": r"in-addr\.arpa\. IN PTR.*i/o timeout", "reason": "die FritzBox beantwortet PTR außerhalb ihres Netzes nicht; DNS lief (#901)"}]}


class NotTheServicesOwnError(unittest.TestCase):
    def test_runtime_lines_alone_are_no_news(self):
        # #915: pasta and conmon lines in a green service's unit: no wish, no note
        s = Fake({"bote": {"bote-pod.service": "\n".join(PASTA * 20)}})
        r = run.run(s)
        self.assertEqual((r["outcome"], r["why"]), ("unchanged", "keine neuen Fehlermuster"))
        self.assertEqual(r["state"], {})

    def test_runtime_lines_are_counted_for_the_runtime_next_to_real_news(self):
        day = {"bote": {"bote-pod.service": "\n".join(PASTA * 5 + [line("bote-notify", "ERROR signal send failed", i) for i in range(3)])}}
        r = run.run(Fake(day))
        self.assertIn("neu, bote-pod.service, 3×: ERROR signal send failed", r["context"])
        self.assertNotIn("epoll_ctl", r["context"])
        self.assertIn("Container-Laufzeit (pasta, conmon; nicht der Dienst, nur gezählt): bote-pod.service 20.", r["context"])

    def test_a_shape_marked_harmless_is_not_counted_a_new_one_is(self):
        ptr = "\n".join(line("adguard", PTR.format(i), i) for i in range(40))
        r = run.run(Fake({"adguard": {"adguard.service": ptr}}, ignore=HARMLESS))
        self.assertEqual((r["outcome"], r["why"]), ("unchanged", "keine neuen Fehlermuster"))
        self.assertEqual(r["state"], {})

        other = "\n".join(line("adguard", f"2026/10/29 21:00:{i:02d}.000000 [error] dnsproxy: upstream 192.168.178.1:53 failed: i/o timeout", i) for i in range(5))
        r = run.run(Fake({"adguard": {"adguard.service": ptr + "\n" + other}}, ignore=HARMLESS))
        self.assertIn("neu, adguard.service, 5×", r["context"])
        self.assertNotIn("in-addr.arpa", r["context"])
        self.assertIn("Als harmlos vermerkt (ignore.yml, nur gezählt): adguard.service 40 (die FritzBox beantwortet PTR", r["context"])

    def test_a_harmless_mark_is_the_services_own(self):
        ptr = "\n".join(line("caddy", PTR.format(i), i) for i in range(5))
        r = run.run(Fake({"caddy": {"caddy.service": ptr}}, ignore=HARMLESS))
        self.assertIn("neu, caddy.service, 5×", r["context"])

    def test_an_unreadable_mark_is_said_and_nothing_is_hidden(self):
        ptr = "\n".join(line("adguard", PTR.format(i), i) for i in range(5))
        r = run.run(Fake({"adguard": {"adguard.service": ptr}}, ignore="laib: git show main:ignore.yml: exit 128"))
        self.assertIn("neu, adguard.service, 5×", r["context"])
        self.assertIn("ignore.yml nicht lesbar", r["context"])
        bad = {"logs": [{"service": "adguard", "match": "(", "reason": "x"}, {"service": "adguard", "match": "IN PTR"}]}
        r = run.run(Fake({"adguard": {"adguard.service": ptr}}, ignore=bad))
        self.assertIn("neu, adguard.service, 5×", r["context"])
        self.assertIn("ignore.yml: 2 Einträge unter logs ohne gültiges Muster oder Grund", r["context"])


if __name__ == "__main__":
    unittest.main()
