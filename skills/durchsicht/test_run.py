"""durchsicht's script (#551, #839): the classifier and the review, without the runner.
The runner-level tests are operator/durchsicht551_test.go; the planned stops
it asks Laib for (laib planned, #978) are cmd/laib/planned978_test.go's, the
same journals as the #975 cases here, whose windows these fakes answer."""

import importlib.util
import json
import os
import re
import sys
import unittest
from datetime import timedelta

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
import laibskill  # noqa: E402

spec = importlib.util.spec_from_file_location("durchsicht", os.path.join(HERE, "run.py"))
run = importlib.util.module_from_spec(spec)
spec.loader.exec_module(run)
from lib import logzeilen  # noqa: E402


class Fake:
    """A session whose laib answers status and logs from a day."""

    def __init__(self, day, state=None, status=None, extra=(), ignore=None, planned=None, now="2026-10-21T06:30:00+02:00", red=()):
        self.day, self.state, self.status, self.extra, self.calls, self.red = day, state, status, list(extra), [], red
        self.ignore = ignore  # ignore.yml as laib state parses it; None: no such file on main
        self.planned, self.now = planned or {}, now  # #978: laib planned's windows per service, or its error text

    def call(self, tool, **args):
        self.calls.append((tool, args))
        if tool == "laib_status":
            if self.status is not None:
                return laibskill.Reply(self.status, True)
            return laibskill.Reply(json.dumps({"services": [{"name": n, "state": "rot" if n in self.red else "grün"} for n in list(self.day) + self.extra]}))
        if tool == "laib_planned":
            if isinstance(self.planned, str):
                return laibskill.Reply(self.planned, True)
            return laibskill.Reply(json.dumps({"service": args["service"], "depends": [], "windows": self.planned.get(args["service"], []), "findings": []}))
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
                got, is_real = logzeilen.pattern(msg)
                self.assertEqual(is_real, real)
                if real:
                    self.assertEqual(got, pattern)

    def test_unicode_is_not_a_word_or_digit(self):
        # Go's \\d and \\w are ASCII: an Arabic-Indic digit is no number
        self.assertEqual(logzeilen.pattern("ERROR ٣ Ärger at 12")[0], "ERROR ٣ Ärger at <n>")


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


# #975: the wishes the review filed for errors a person found expected,
# over or resolved (wishes #304, #305, #268, #294), in the box's shapes
def at(t, ident, msg):
    return f"{t}+00:00 atHome-Server {ident}: {msg}"


def win(svc, label, stop, start):
    """A window as laib planned answers it: from two minutes before the stop to three after the start."""
    t0, t1 = laibskill.when(stop + "Z"), laibskill.when(start + "Z")
    f = "%Y-%m-%dT%H:%M:%SZ"
    return {"service": svc, "label": label, "stop": t0.strftime(f), "start": t1.strftime(f),
            "from": (t0 - timedelta(minutes=2)).strftime(f), "to": (t1 + timedelta(minutes=3)).strftime(f)}


REFUSED = "2026/10/31 10:08:49 haus: HA state_changed: dial tcp 169.254.1.2:8123: connect: connection refused"
# Home Assistant's three applies of 2026-10-31 (#305), as cmd/laib/planned978_test.go computes them
HA_APPLIES = [win("home-assistant", "Anwenden", "2026-10-31T09:08:47", "2026-10-31T09:08:57"), win("home-assistant", "Anwenden", "2026-10-31T09:11:03", "2026-10-31T09:11:10"),
              win("home-assistant", "Anwenden", "2026-10-31T11:11:22", "2026-10-31T11:11:27")]


def haus(*times):
    return {"haus": {"haus.service": "\n".join(at(t, "systemd-haus[1339921]", REFUSED) for t in times)}}


def notes(fake):
    return [a["text"] for tool, a in fake.calls if tool == "laib_note"]


class CheckedLikeAPerson(unittest.TestCase):
    def test_305_haus_refused_while_home_assistant_was_applied_is_expected(self):
        day = haus("2026-10-31T09:08:49", "2026-10-31T09:08:53", "2026-10-31T09:09:01", "2026-10-31T09:11:05", "2026-10-31T09:11:09", "2026-10-31T11:11:25", "2026-10-31T11:11:30")
        s = Fake(day, planned={"haus": HA_APPLIES}, now="2026-11-01T06:30:00+01:00")
        r = run.run(s)
        self.assertEqual(r["outcome"], "unchanged")
        self.assertEqual(r["state"], {})  # never a pattern: the next review does not call it recurring
        self.assertEqual(notes(s), ["Protokoll-Durchsicht: keine neuen Fehlermuster. Aussortiert, kein Wunsch: haus.service 7× während eines geplanten Neustarts "
                                    "(Anwenden von home-assistant 10:08, Anwenden von home-assistant 10:11, Anwenden von home-assistant 12:11)."])

    def test_304_music_assistant_502_while_the_backup_stopped_home_assistant_is_expected(self):
        msg = "aiohttp.client_exceptions.WSServerHandshakeError: 502, message='Invalid response status', url='wss://home.dopp.cloud/api/websocket'"
        day = {"music-assistant": {"music-assistant.service": "\n".join(at(t, "systemd-music-assistant[994158]", msg) for t in
                                                                     ("2026-11-01T03:07:40", "2026-11-01T03:08:30", "2026-11-01T03:09:30", "2026-11-01T03:09:35", "2026-11-01T03:11:00"))}}
        windows = [win("home-assistant", "Sicherung", "2026-11-01T03:07:28", "2026-11-01T03:08:21"), win("home-assistant", "laib restart", "2026-11-01T03:09:27", "2026-11-01T03:09:33")]
        s = Fake(day, planned={"music-assistant": windows}, now="2026-11-01T06:30:00+01:00")
        r = run.run(s)
        self.assertEqual(r["outcome"], "unchanged")
        self.assertIn("music-assistant.service 5× während eines geplanten Neustarts (Sicherung von home-assistant 04:07, laib restart von home-assistant 04:09)", notes(s)[0])

    def test_268_a_certificate_obtained_after_its_failures_is_resolved(self):
        fail = '{"level":"error","ts":1793275970.5,"logger":"tls.obtain","msg":"could not get certificate from issuer","identifier":"gesundheit.dopp.cloud","issuer":"acme-v02.api.letsencrypt.org-directory","error":"HTTP 400 urn:ietf:params:acme:error:dns - no valid A records found for gesundheit.dopp.cloud; no valid AAAA records found for gesundheit.dopp.cloud"}'
        ok = '{"level":"info","ts":1793276775.4,"logger":"tls.obtain","msg":"certificate obtained successfully","identifier":"gesundheit.dopp.cloud","issuer":"acme-v02.api.letsencrypt.org-directory"}'
        lines = [at(t, "systemd-caddy[1055506]", fail) for t in ("2026-10-29T12:12:50", "2026-10-29T12:13:54", "2026-10-29T12:17:27", "2026-10-29T12:19:30")]
        s = Fake({"caddy": {"caddy.service": "\n".join(lines + [at("2026-10-29T12:26:15", "systemd-caddy[1055506]", ok)])}}, now="2026-10-29T14:00:00+01:00")
        r = run.run(s)
        self.assertEqual((r["outcome"], r["why"]), ("unchanged", "keine neuen Fehlermuster; 1 Muster aussortiert"))
        self.assertIn("caddy.service „tls.obtain: could not get certificate from issuer (HTTP <n>…“ 4× "
                      "behoben: Zertifikat für gesundheit.dopp.cloud danach ausgestellt (13:26)", notes(s)[0])
        # still failing for it: a wish
        r = run.run(Fake({"caddy": {"caddy.service": "\n".join(lines)}}, now="2026-10-29T14:00:00+01:00"))
        self.assertIn("neu, caddy.service, 4×", r["context"])

    def test_294_the_lease_stopped_by_each_laib_update_is_expected(self):
        times = ["2026-10-30T08:22:22", "2026-10-30T11:05:45", "2026-10-30T16:29:58", "2026-10-30T19:27:26", "2026-10-30T21:09:09", "2026-10-30T22:23:37", "2026-10-30T23:56:48", "2026-10-31T00:46:50"]
        lines = [at(t, "systemd[1373]", "llama-lease.service: Failed with result 'exit-code'.") for t in times]
        s = Fake({"llama": {"llama-lease.service": "\n".join(lines)}}, planned={"llama": [win("llama", "Update", t, t) for t in times]}, now="2026-10-31T06:30:00+01:00")
        r = run.run(s)
        self.assertEqual(r["outcome"], "unchanged")
        self.assertIn("llama-lease.service 8× während eines geplanten Neustarts (Update von llama ", notes(s)[0])

    def test_a_genuine_error_outside_restarts_is_still_a_wish_and_says_what_was_checked(self):
        day = haus("2026-11-01T05:00:00", "2026-11-01T05:10:00", "2026-11-01T05:20:00")
        s = Fake(day, planned={"haus": HA_APPLIES}, now="2026-11-01T06:30:00+01:00")
        r = run.run(s)
        self.assertEqual(r["outcome"], "")
        self.assertIn("- neu, haus.service, 3×: haus: HA state_changed: dial tcp <ip>: connect: connection refused\n", r["context"])
        self.assertIn("  geprüft: nicht während eines geplanten Neustarts, zuletzt 06:20 (vor 10 min), auch nach Anwenden von home-assistant um 12:11, noch aktuell\n", r["context"])
        self.assertEqual(r["checks"], ["laib_wish"])
        # the model's wish says what was checked, about a shown pattern
        self.assertIn("geprüft", run.check(s, "laib_wish", {"text": "haus: HA nicht erreichbar", "saw": "3× connection refused"}))
        self.assertIn("nenne seinen Dienst (haus)", run.check(s, "laib_wish", {"text": "Mehr RAM", "saw": "3×, geprüft: nicht während eines Neustarts"}))
        self.assertEqual(run.check(s, "laib_wish", {"text": "Haus erreicht Home Assistant nicht", "saw": "haus.service 3×, geprüft: nicht während eines geplanten Neustarts, zuletzt 06:20"}), "")
        self.assertEqual(run.check(s, "laib_note", {"text": "x"}), "")

    def test_a_stop_the_journal_does_not_account_for_is_not_planned(self):
        # a crash cascade or a hand at systemctl: laib planned has no window (cmd/laib/planned978_test.go)
        day = haus("2026-10-31T09:08:49", "2026-10-31T09:08:53", "2026-10-31T09:09:01")
        r = run.run(Fake(day, planned={"haus": []}, now="2026-10-31T11:00:00+01:00"))
        self.assertIn("neu, haus.service, 3×", r["context"])
        self.assertIn("geprüft: nicht während eines geplanten Neustarts", r["context"])
        s = Fake(day, planned="nicht ausgeführt: laib_planned ist der Fähigkeit durchsicht nicht gewährt", now="2026-10-31T11:00:00+01:00")
        r = run.run(s)
        self.assertIn("geprüft: geplante Neustarts nicht geprüft (nicht lesbar)", r["context"])
        # until Laib can answer (the ops grant waits in the Inbox, #932): a note, never an unchecked wish
        self.assertIn("kein Wunsch", run.check(s, "laib_wish", {"text": "haus erreicht HA nicht", "saw": "haus.service 3×, geprüft: geplante Neustarts nicht geprüft (nicht lesbar)"}))

    def test_a_new_pattern_quiet_for_six_hours_is_over_a_recurring_one_is_not(self):
        day = haus("2026-10-31T20:00:00", "2026-10-31T20:05:00", "2026-10-31T20:10:00")
        s = Fake(day, now="2026-11-01T06:30:00+01:00")
        r = run.run(s)
        self.assertEqual(r["outcome"], "unchanged")
        self.assertIn("3× vorbei: zuletzt 21:10, seit über 6 Stunden nicht mehr", notes(s)[0])
        self.assertEqual(list(r["state"].values()), [{"count": 3, "told": False}])
        r = run.run(Fake(day, state=r["state"], now="2026-11-01T06:30:00+01:00"))  # back the next day
        self.assertIn("wiederkehrend, haus.service, 3×", r["context"])
        self.assertIn("schon in der letzten Durchsicht", r["context"])

    def test_the_dropped_list_stays_short_enough_for_a_note(self):
        many = "\n".join(at(f"2026-10-31T20:{i:02d}:00", "systemd-haus[1]", f"ERROR job {chr(97 + i // 3)}{'x' * 200} failed") for i in range(30))
        s = Fake({"haus": {"haus.service": many}}, now="2026-11-01T06:30:00+01:00")
        run.run(s)
        self.assertIn("; 2 weitere Muster (6×).", notes(s)[0])
        self.assertLess(len(notes(s)[0]), 1500)

    def test_a_new_pattern_gone_since_a_planned_restart_of_a_green_service_is_over(self):
        day = haus("2026-11-01T04:00:00", "2026-11-01T04:05:00", "2026-11-01T04:10:00")
        restart = {"haus": [win("haus", "Anwenden", "2026-11-01T04:30:00", "2026-11-01T04:30:10")]}
        s = Fake(day, planned=restart, now="2026-11-01T06:30:00+01:00")
        run.run(s)
        self.assertIn("3× vorbei: zuletzt 05:10, seit Anwenden von haus um 05:30 nicht mehr, Dienst grün", notes(s)[0])
        r = run.run(Fake(day, planned=restart, now="2026-11-01T06:30:00+01:00", red=["haus"]))
        self.assertIn("vor Anwenden von haus um 05:30, Dienst nicht grün", r["context"])


if __name__ == "__main__":
    unittest.main()
