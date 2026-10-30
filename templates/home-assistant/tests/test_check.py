"""#55, #309: the integrations checklist moved out of laib's kernel into this
template. home-assistant-check.py reads HA's config entries with HA_TOKEN and
writes the status file laib shows; here against a fake Home Assistant."""
import json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "home-assistant-check.py"
sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_post_deploy as fake   # its fake Home Assistant, WebSocket included (#634)


def fake_ha(entries, status=200):
    class H(BaseHTTPRequestHandler):
        seen = []

        def do_GET(self):
            H.seen.append((self.path, self.headers.get("Authorization")))
            body = json.dumps(entries).encode()
            self.send_response(status)
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *a):
            pass
    srv = HTTPServer(("127.0.0.1", 0), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, H


def fake_zwave(code):
    class Z(BaseHTTPRequestHandler):
        seen = []

        def do_GET(self):
            Z.seen.append(self.path)
            self.send_response(code if self.path == "/health/zwave" else 200)
            self.end_headers()
            self.wfile.write(b"{}")

        def log_message(self, *a):
            pass
    srv = HTTPServer(("127.0.0.1", 0), Z)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, Z


def fake_ma(onboarded=True, jellyfin="loaded", hass="loaded", tracks=1200, players=2):
    """Music Assistant 2.10's /info and the JSON-RPC commands the check asks (#526)"""
    class M(BaseHTTPRequestHandler):
        seen = []

        def do_GET(self):
            self.answer(200, {"onboard_done": onboarded, "schema_version": 28})

        def do_POST(self):
            b = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            M.seen.append((b["command"], self.headers.get("Authorization")))
            if self.headers.get("Authorization") != "Bearer ma-tok":
                return self.answer(401, "Authentication required")
            status = {"jellyfin": jellyfin, "hass": hass}
            r = {"music/tracks/count": tracks, "players/all": [{"player_id": f"p{i}"} for i in range(players)],
                 "config/providers": [{"domain": d, "status": status[d]} for d in [b["args"].get("provider_domain")] if status.get(d)]}
            self.answer(200, r[b["command"]])

        def answer(self, code, body):
            data = json.dumps(body).encode()
            self.send_response(code)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, *a):
            pass
    srv = HTTPServer(("127.0.0.1", 0), M)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, M


EVERYTHING = [{"domain": d, "title": t} for d, t in [("wyoming", "whisper"), ("wyoming", "piper"), ("wyoming", "openwakeword"),
                                                     ("caldav", ""), ("music_assistant", ""), ("mqtt", ""), ("mcp_server", ""),
                                                     ("ollama", "http://127.0.0.1:8710")]]


class Check(unittest.TestCase):
    def run_check(self, token, url, zwave="", rc=0, ma_token="", ma_url="http://127.0.0.1:1", config=None):
        d = config or tempfile.mkdtemp()
        out = os.path.join(d, ".laib-status.json")
        # the unit feeds the script on stdin, as `podman exec -i … python3 -` does
        p = subprocess.run([sys.executable, "-"], stdin=open(SCRIPT), capture_output=True, text=True,
                           env=dict(os.environ, HA_TOKEN=token, HA_URL=url, LAIB_STATUS_FILE=out, ZWAVE_URL=zwave,
                                    MA_TOKEN=ma_token, MA_URL=ma_url, HA_CONFIG=d))
        self.assertEqual(p.returncode, rc, p.stdout + p.stderr)
        return json.loads(Path(out).read_text())

    def test_a_zwave_without_its_controller_is_red(self):
        # #417: zwave-js-ui retried the stick all night (SELinux EACCES), Home Assistant answered 200,
        # laib status stayed green. The check asks zwave-js-ui whether its driver is ready; when not, a
        # red finding and a failed unit, which laib status shows red.
        everything = [{"domain": d, "title": t} for d, t in [("wyoming", "whisper"), ("wyoming", "piper"), ("wyoming", "openwakeword"),
                                                             ("caldav", ""), ("music_assistant", ""), ("mqtt", ""), ("mcp_server", ""),
                                                             ("ollama", "http://127.0.0.1:8710")]]
        ha, _ = fake_ha(everything)
        zw, seen = fake_zwave(500)
        r = self.run_check("tok", f"http://127.0.0.1:{ha.server_port}", f"http://127.0.0.1:{zw.server_port}", rc=1)
        self.assertIn("/health/zwave", seen.seen)
        rot = [f for f in r["findings"] if f["severity"] == "rot"]
        self.assertEqual(len(rot), 1, r)
        self.assertIn("Z-Wave", rot[0]["detail"])
        self.assertIn("Z-Wave", r["detail"])
        zw, _ = fake_zwave(200)
        r = self.run_check("tok", f"http://127.0.0.1:{ha.server_port}", f"http://127.0.0.1:{zw.server_port}")
        self.assertFalse([f for f in r["findings"] if f["severity"] == "rot"], r)
        # zwave-js-ui not answering at all is red too
        r = self.run_check("tok", f"http://127.0.0.1:{ha.server_port}", "http://127.0.0.1:1", rc=1)
        self.assertTrue([f for f in r["findings"] if f["severity"] == "rot"], r)

    def test_the_unit_asks_zwave_and_runs_often(self):
        unit = (SCRIPT.parent / "home-assistant-check.service").read_text()
        self.assertIn("ZWAVE_URL=http://127.0.0.1:8091", unit)
        self.assertIn("-e ZWAVE_URL", unit)
        self.assertIn("-e LAIB_SERVICE", unit)   # #528: the HA_INSTALL proposal names the service
        self.assertNotIn("OnCalendar=hourly", (SCRIPT.parent / "home-assistant-check.timer").read_text(),
                         "a dead Z-Wave network must not wait an hour to turn red")

    def test_a_box_without_the_zwave_unit_checks_no_zwave(self):
        # the VM and a box without a stick drop home-assistant-zwave.container: no ZWAVE_URL, no red
        import re, subprocess, tempfile
        unit = (SCRIPT.parent / "home-assistant-check.service").read_text()
        cmd = re.search(r"^ExecStart=/bin/sh -c '(.*)'$", unit, re.M).group(1)
        with tempfile.TemporaryDirectory() as home:
            fake = Path(home) / "podman"
            fake.write_text('#!/bin/sh\necho "ZWAVE_URL=${ZWAVE_URL-unset}"\n')
            fake.chmod(0o755)
            sh = cmd.replace("%h", home).replace("%%", "%").replace("$$", "$").replace("/usr/bin/podman", str(fake))   # as systemd hands it to sh
            run = lambda: subprocess.run(["/bin/sh", "-c", sh],
                                         env={"PATH": "/usr/bin:/bin", "ZWAVE_URL": "http://127.0.0.1:8091"},
                                         capture_output=True, text=True).stdout.strip()
            self.assertEqual(run(), "ZWAVE_URL=unset")
            d = Path(home) / ".config/containers/systemd/home-assistant"
            d.mkdir(parents=True)
            (d / "home-assistant-zwave.container").write_text("")
            self.assertEqual(run(), "ZWAVE_URL=http://127.0.0.1:8091")

    def test_zwave_keys_readable_by_others_are_a_finding(self):
        # #664: the unit looks at the store on the host (the HA container does not mount it) and hands
        # the names of the key files others can read to the check, which names them
        ha, _ = fake_ha(EVERYTHING)
        d = tempfile.mkdtemp()
        out = os.path.join(d, ".laib-status.json")
        p = subprocess.run([sys.executable, "-"], stdin=open(SCRIPT), capture_output=True, text=True,
                           env=dict(os.environ, HA_TOKEN="tok", HA_URL=f"http://127.0.0.1:{ha.server_port}", LAIB_STATUS_FILE=out, ZWAVE_URL="",
                                    ZWAVE_OPEN_KEYS="settings.json settings.json.servicebay "))
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        r = json.loads(Path(out).read_text())
        f = [f for f in r["findings"] if "Z-Wave-Schlüssel" in f["detail"]]
        self.assertEqual(len(f), 1, r)
        self.assertIn("settings.json.servicebay", f[0]["detail"])
        self.assertIn("laib apply home-assistant", f[0]["fix"])
        r = self.run_check("tok", f"http://127.0.0.1:{ha.server_port}")
        self.assertFalse([f for f in r["findings"] if "Z-Wave-Schlüssel" in f["detail"]], r)

    def test_the_unit_finds_the_open_key_files_on_the_host(self):
        import re
        unit = (SCRIPT.parent / "home-assistant-check.service").read_text()
        self.assertIn("-e ZWAVE_OPEN_KEYS", unit)
        cmd = re.search(r"^ExecStart=/bin/sh -c '(.*)'$", unit, re.M).group(1)
        with tempfile.TemporaryDirectory() as home:
            z = Path(home, "d", "home-assistant", "zwave-js")
            z.mkdir(parents=True)
            for f, mode in (("settings.json", 0o644), ("settings.json.servicebay", 0o640), ("settings.json.sb-bak-1", 0o600), ("nodes.json", 0o644)):
                (z / f).write_text("{}")
                (z / f).chmod(mode)
            fake = Path(home) / "podman"
            fake.write_text('#!/bin/sh\necho "$ZWAVE_OPEN_KEYS"\n')
            fake.chmod(0o755)
            # as systemd hands it to sh: %% and $$ are one character there; laib renders ${DATA_DIR}
            sh = cmd.replace("%h", home).replace("%%", "%").replace("$$", "$").replace("${DATA_DIR}", str(Path(home, "d"))).replace("/usr/bin/podman", str(fake))
            run = lambda: subprocess.run(["/bin/sh", "-c", sh], env={"PATH": "/usr/bin:/bin"}, capture_output=True, text=True).stdout.split()
            self.assertEqual(sorted(run()), ["settings.json", "settings.json.servicebay"])
            for f in z.iterdir():
                f.chmod(0o600)
            self.assertEqual(run(), [])
            for f in z.iterdir():
                f.unlink()
            z.rmdir()
            self.assertEqual(run(), [], "a box without the store")

    def test_names_each_missing_integration(self):
        srv, h = fake_ha([{"domain": "wyoming", "title": "faster-whisper"}, {"domain": "mqtt", "title": "Mosquitto"},
                          {"domain": "ollama", "title": "http://127.0.0.1:8787/ollama"}])
        r = self.run_check("tok", f"http://127.0.0.1:{srv.server_port}")
        self.assertEqual(h.seen[0], ("/api/config/config_entries/entry", "Bearer tok"))
        missing = [f["detail"] for f in r["findings"]]
        self.assertIn("Wyoming Piper: fehlt", missing)
        self.assertIn("CalDAV: fehlt", missing)
        self.assertNotIn("Wyoming Whisper: fehlt", missing)
        self.assertNotIn("MQTT: fehlt", missing)
        # #491: Frage's agent is the Ollama entry on 8710; Solaris's on 8787 is not it, and its fix says how
        self.assertIn("Frage als Gesprächsagent: fehlt", missing)
        self.assertIn("laib apply home-assistant", next(f["fix"] for f in r["findings"] if f["detail"].startswith("Frage")))
        self.assertTrue(all(f["severity"] == "gelb" for f in r["findings"]))
        self.assertTrue(r["at"].endswith("Z") and r["detail"].startswith("fehlt: "))

    def test_stimmes_entries_count_and_solaris_dead_ones_are_named(self):
        # #540: HA titles a Wyoming entry with the server's first program (wyoming/data.py get_name):
        # faster-whisper, piper, openwakeword for Stimme's images. Solaris' entries stay after its units
        # went, in setup_retry: they count for no row, and each is a finding of its own.
        ok = lambda d, t: {"domain": d, "title": t, "state": "loaded"}
        got = [ok("wyoming", "faster-whisper"), ok("wyoming", "piper"), ok("wyoming", "openwakeword"), ok("caldav", "homeassistant"),
               ok("music_assistant", "Music Assistant"), ok("mqtt", "Mosquitto"), ok("mcp_server", "Assist"),
               ok("ollama", "http://127.0.0.1:8710")]
        srv, _ = fake_ha(got)
        r = self.run_check("tok", f"http://127.0.0.1:{srv.server_port}")
        self.assertEqual((r["findings"], r["detail"]), ([], "alle 8 Integrationen eingerichtet"))
        dead = [{"domain": "wyoming", "title": "piper", "state": "setup_retry"}, {"domain": "ollama", "title": "http://127.0.0.1:8787/ollama", "state": "setup_retry"}]
        srv, _ = fake_ha([e for e in got if e["title"] != "piper"] + dead)
        r = self.run_check("tok", f"http://127.0.0.1:{srv.server_port}")
        details = [f["detail"] for f in r["findings"]]
        self.assertIn("Wyoming Piper: fehlt", details, "a Wyoming entry whose server is gone counted as Piper")
        self.assertIn("laib add stimme", next(f["fix"] for f in r["findings"] if f["detail"] == "Wyoming Piper: fehlt"))
        self.assertIn("„piper“ (wyoming) antwortet nicht", details)
        self.assertIn("„http://127.0.0.1:8787/ollama“ (ollama) antwortet nicht", details)
        self.assertIn("in Home Assistant löschen", r["findings"][-1]["fix"])
        self.assertEqual(r["detail"], "fehlt: Wyoming Piper · 2 Sprach-Integrationen ohne Dienst")

    def test_complete_and_unreachable_and_no_token(self):
        everything = [{"domain": d, "title": t} for _, d, t in
                      [("", "wyoming", "whisper"), ("", "wyoming", "piper"), ("", "wyoming", "openwakeword"), ("", "caldav", ""),
                       ("", "music_assistant", ""), ("", "mqtt", ""), ("", "mcp_server", ""), ("", "ollama", "http://127.0.0.1:8710")]]
        srv, _ = fake_ha(everything)
        r = self.run_check("tok", f"http://127.0.0.1:{srv.server_port}")
        self.assertEqual((r["findings"], r["detail"]), ([], "alle 8 Integrationen eingerichtet"))
        srv, _ = fake_ha([], status=401)
        r = self.run_check("bad", f"http://127.0.0.1:{srv.server_port}")
        self.assertIn("antwortet nicht", r["findings"][0]["detail"])
        r = self.run_check("", "http://127.0.0.1:1")
        self.assertEqual((r["findings"], r["detail"]), ([], ""))

    def test_updates_are_reported_and_the_extensions_offered_as_one_proposal(self):
        # #528: "Einige Plugins in Home Assistant sind schon 4 Monate alt." HA's update entities that are
        # on: the extensions as "Update: …" with their release notes, the installable ones as one HA_INSTALL
        # proposal (the fix the Operator files as it stands), the firmware as "Firmware: …", only reported.
        upd = lambda eid, title, old, new, feat=1, cls=None, url=None: {"entity_id": eid, "state": "on", "attributes": {
            "title": title, "installed_version": old, "latest_version": new, "supported_features": feat, "device_class": cls, "release_url": url}}
        states = [upd("update.senec_update", "SENEC.Home", "2026.6.3", "2026.10.0", url="https://github.com/x/senec/releases/tag/2026.10.0"),
                  upd("update.meross_lan_update", "Meross LAN", "5.3.0", "5.4.1"),
                  upd("update.plug_firmware", "Steckdose", "9.5.52", "9.5.60", cls="firmware"),
                  upd("update.no_install", "Nur Anzeige", "1", "2", feat=0),
                  {"entity_id": "update.hacs_update", "state": "off", "attributes": {"installed_version": "2.0", "latest_version": "2.0"}},
                  {"entity_id": "sun.sun", "state": "above_horizon", "attributes": {}}]

        class H(BaseHTTPRequestHandler):
            def do_GET(self):
                body = json.dumps(states if self.path == "/api/states" else []).encode()
                self.send_response(200)
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *a):
                pass
        srv = HTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        r = self.run_check("tok", f"http://127.0.0.1:{srv.server_port}")
        by = {f["detail"]: f for f in r["findings"] if f["severity"] == "info"}
        self.assertIn("Update: SENEC.Home 2026.6.3 → 2026.10.0 · https://github.com/x/senec/releases/tag/2026.10.0", by)
        self.assertIn("Update: Meross LAN 5.3.0 → 5.4.1", by)
        self.assertIn("Firmware: Steckdose 9.5.52 → 9.5.60 (nur gemeldet)", by)
        self.assertIn("Update: Nur Anzeige 1 → 2", by)
        offer = [f for f in r["findings"] if f["fix"].startswith("laib propose")]
        self.assertEqual(len(offer), 1, r)
        self.assertEqual(offer[0]["fix"], "laib propose --set services/home-assistant/values.yml:HA_INSTALL=update.senec_update=2026.10.0,update.meross_lan_update=5.4.1")
        self.assertIn("https://github.com/x/senec/releases/tag/2026.10.0", offer[0]["detail"], "the proposal carries the release notes")
        self.assertNotIn("firmware", offer[0]["fix"])
        self.assertFalse([f for f in r["findings"] if "hacs" in f["detail"].lower()], "an extension that is current is no finding")

    def test_an_extension_home_assistant_cannot_load_is_a_finding(self):
        # #528: HACS 2.0.5 unpacked marq24/ha-senec-v3 one level too deep; HA said "Integration not found"
        d = tempfile.mkdtemp()
        cc = Path(d, "custom_components")
        for path in ("senec/senec/manifest.json", "meross_lan/manifest.json", "kaputt/__init__.py", "__pycache__/x.pyc"):
            (cc / path).parent.mkdir(parents=True, exist_ok=True)
            (cc / path).write_text("{}")
        srv, _ = fake_ha(EVERYTHING)
        r = self.run_check("tok", f"http://127.0.0.1:{srv.server_port}", config=d)
        found = {f["detail"]: f["fix"] for f in r["findings"] if f["detail"].startswith("Erweiterung")}
        self.assertEqual(found, {"Erweiterung senec: Home Assistant lädt sie nicht, sie liegt eine Ebene zu tief":
                                 "laib apply home-assistant: zieht sie gerade und startet Home Assistant neu",
                                 "Erweiterung kaputt: Home Assistant lädt sie nicht, ihr fehlt die manifest.json":
                                 "in HACS neu herunterladen, dann laib apply home-assistant"}, r)

    def test_music_assistant_is_green_only_set_up_with_titles_and_players(self):
        # #526: the integration's entry in HA was the whole check; Music Assistant stood on SETUP REQUIRED behind it
        ha, _ = fake_ha(EVERYTHING)
        base = f"http://127.0.0.1:{ha.server_port}"
        ma, seen = fake_ma()
        r = self.run_check("tok", base, ma_token="ma-tok", ma_url=f"http://127.0.0.1:{ma.server_port}")
        self.assertEqual(r["findings"], [])
        self.assertIn("Music Assistant: 1200 Titel, 2 Lautsprecher", r["detail"])
        self.assertIn(("music/tracks/count", "Bearer ma-tok"), seen.seen)
        for kw, want in ((dict(onboarded=False), "nicht eingerichtet"), (dict(jellyfin=None), "keine Jellyfin-Quelle"),
                         (dict(jellyfin="auth_required"), "keine Jellyfin-Quelle"), (dict(tracks=0), "keine Titel"),
                         (dict(players=0), "kennt keine Lautsprecher"), (dict(hass="error"), "Anmeldung über Home Assistant")):
            ma, _ = fake_ma(**kw)
            r = self.run_check("tok", base, ma_token="ma-tok", ma_url=f"http://127.0.0.1:{ma.server_port}")
            self.assertEqual([f["severity"] for f in r["findings"]], ["gelb"], (kw, r))
            self.assertIn(want, r["findings"][0]["detail"], kw)
            self.assertIn("Music Assistant unvollständig", r["detail"])
        r = self.run_check("tok", base, ma_token="refused", ma_url=f"http://127.0.0.1:{ma.server_port}")
        self.assertIn("antwortet nicht", r["findings"][0]["detail"])
        r = self.run_check("tok", base)   # no Music Assistant on the box: nothing to say about it
        self.assertEqual((r["findings"], r["detail"]), ([], "alle 8 Integrationen eingerichtet"))

    def test_the_unit_hands_the_check_music_assistants_token(self):
        unit = (SCRIPT.parent / "home-assistant-check.service").read_text()
        self.assertIn("Environment=MA_TOKEN=${MA_TOKEN}", unit)
        self.assertIn("-e MA_TOKEN", unit)


class Voice(unittest.TestCase):
    """#634: the voice, measured: the persona's pipeline is there and preferred, HA's local intents
    recognize a harmless sentence in its language (conversation/agent/homeassistant/debug: recognized,
    never executed, so nothing switches), the wake word is the persona's or the status says why not,
    the satellites are on the pipeline. Against the fake Home Assistant of test_post_deploy, as the
    check's own non-admin user."""

    def setUp(self):
        fake.fresh(onboarded=("laib", "pw"))
        self.srv = fake.ThreadingHTTPServer(("127.0.0.1", 0), fake.FakeHA)
        self.srv.daemon_threads = True
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.shutdown)
        self.token = fake.token_for(fake.add_user("Laib-Prüfung", admin=False), True, "laib: home-assistant-check")
        H = fake.HA
        H["entries"] = [{"entry_id": "e", "domain": d, "title": t, "state": "loaded"} for d, t in [
            ("wyoming", "faster-whisper"), ("wyoming", "piper"), ("wyoming", "openwakeword"), ("caldav", ""), ("music_assistant", ""),
            ("mqtt", ""), ("mcp_server", ""), ("ollama", "http://127.0.0.1:8710")]]
        H["pipelines"] = [{"id": "p-ha", "name": "Home Assistant", "language": "en", "conversation_engine": "conversation.home_assistant",
                           "prefer_local_intents": False, "wake_word_entity": None, "wake_word_id": None},
                          {"id": "p-j", "name": "Joshua", "language": "de", "conversation_engine": "conversation.frage", "prefer_local_intents": True,
                           "wake_word_entity": "wake_word.openwakeword", "wake_word_id": "okay_nabu"}]
        H["preferred"] = "p-j"
        H["wake_words"] = {"wake_word.openwakeword": ["okay_nabu"]}
        hit = lambda name: {"intent": {"name": name}, "match": True, "source": "builtin"}
        H["intents"] = {("de", "Wie spät ist es"): hit("HassGetCurrentTime"), ("de", "Stelle einen Timer auf 5 Minuten"): hit("HassStartTimer"),
                        ("de", "Spiele Einslive"): hit("LaibRadio"), ("de", "Spiele Musik von Queen"): hit("LaibMusik"),
                        ("de", "Schalte Sofalicht ein"): hit("HassTurnOn")}
        H["registry"] = [{"entity_id": "light.sofa", "platform": "hue", "disabled_by": None, "options": {"conversation": {"should_expose": True}}},{"entity_id": "select.voice_kueche_pipeline", "platform": "esphome", "translation_key": "pipeline", "disabled_by": None},
                         {"entity_id": "select.voice_kueche_wake_word", "platform": "esphome", "translation_key": "wake_word", "disabled_by": None}]
        H["states"] = {"light.sofa": {"state": "off", "attributes": {"friendly_name": "Sofalicht"}}, "select.voice_kueche_pipeline": {"state": "Joshua", "attributes": {"options": ["preferred", "Joshua"], "friendly_name": "Küche Assistent"}},
                       "select.voice_kueche_wake_word": {"state": "Okay Nabu", "attributes": {"options": ["no_wake_word", "Okay Nabu"]}}}

    def check(self, **env):
        d = tempfile.mkdtemp()
        out = os.path.join(d, ".laib-status.json")
        e = dict(os.environ, HA_TOKEN=self.token, HA_URL=f"http://127.0.0.1:{self.srv.server_port}", LAIB_STATUS_FILE=out, ZWAVE_URL="",
                 MA_TOKEN="", VOICE_PERSONA="Joshua", VOICE_WAKEWORD="hey_joshua", VOICE_PHRASE="Hey Joshua")
        e.update(env)
        p = subprocess.run([sys.executable, "-"], stdin=open(SCRIPT), capture_output=True, text=True, env=e)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return json.loads(Path(out).read_text())

    def voice(self, r):
        return [f["detail"] for f in r["findings"] if f["detail"].startswith(("Sprache", "Weckwort", "Satellit"))]

    def test_local_intents_answer_and_the_status_says_so_and_nothing_switches(self):
        r = self.check()
        self.assertRegex(r["detail"], r"Sprache: lokale Befehle aktiv \(HassGetCurrentTime in \d+ ms\)", r)
        self.assertEqual([c for c in fake.HA["calls"]], [], "the check called a service")
        self.assertEqual(fake.HA["pipeline_updates"], [], "the check changed a pipeline")

    def test_until_the_persona_wake_word_exists_the_status_says_which_one_wakes(self):
        r = self.check()
        self.assertIn("Weckwort okay_nabu, bis hey_joshua da ist", r["detail"], r)
        self.assertFalse([f for f in r["findings"] if f["severity"] != "info" and "Weckwort" in f["detail"]], r)

    def test_a_wake_word_on_the_device_is_never_said_to_be_missing(self):
        """#785, #759: Stimme put „Hey Joshua“ onto the Voice PE; the box's openWakeWord still lacks
        hey_joshua, but the device's select offers the phrase: the line says where the word is."""
        sel = fake.HA["states"]["select.voice_kueche_wake_word"]
        sel["attributes"]["options"].append("Hey Joshua")
        sel["state"] = "Hey Joshua"
        for have in ("okay_nabu", None):
            fake.HA["pipelines"][1].update(wake_word_id=have)
            r = self.check()
            self.assertIn("Weckwort „Hey Joshua“ am Gerät", r["detail"], r)
            self.assertNotIn("bis hey_joshua da ist", r["detail"], r)
            self.assertEqual(self.voice(r), [], r)

    def test_a_persona_wake_word_offered_but_unused_is_a_finding_with_the_apply(self):
        fake.HA["wake_words"]["wake_word.openwakeword"].append("hey_joshua")
        r = self.check()
        f = [f for f in r["findings"] if "Weckwort" in f["detail"]]
        self.assertTrue(f and f[0]["severity"] == "gelb" and "laib apply home-assistant" in f[0]["fix"], r)
        fake.HA["pipelines"][1].update(wake_word_id="hey_joshua")
        r = self.check()
        self.assertIn("Weckwort hey_joshua", r["detail"])
        self.assertEqual(self.voice(r), [], r)

    def test_a_dead_wake_word_is_yellow(self):
        fake.HA["pipelines"][1].update(wake_word_id="solaris")
        r = self.check()
        f = [f for f in r["findings"] if "Weckwort" in f["detail"]]
        self.assertTrue(f and f[0]["severity"] == "gelb" and "solaris" in f[0]["detail"], r)

    def test_local_intents_that_do_not_answer_are_yellow(self):
        fake.HA["intents"].clear()
        r = self.check()
        self.assertIn("Sprache: lokale Befehle antworten nicht", r["detail"], r)
        self.assertTrue([f for f in r["findings"] if f["severity"] == "gelb" and f["detail"].startswith("Sprache")], r)

    def test_no_persona_pipeline_or_not_preferred_or_a_satellite_elsewhere_is_named(self):
        fake.HA["preferred"] = "p-ha"
        fake.HA["states"]["select.voice_kueche_pipeline"]["state"] = "Home Assistant"
        r = self.check()
        details = " | ".join(self.voice(r))
        self.assertIn("nicht bevorzugt", details, r)
        self.assertIn("Küche Assistent", details, r)
        fake.HA["pipelines"].pop()
        r = self.check()
        self.assertIn("Sprache: kein Ablauf „Joshua“", r["detail"], r)

    def test_the_five_intents_are_recognized_as_text_and_nothing_switches(self):
        """#864: ADR 0015's five intents (timer, Einslive, music, „was ist …“, a switch) through HA's
        conversation API as text: recognized, never executed; „was ist …“ must reach Frage."""
        r = self.check()
        self.assertIn("Absichten: 5 von 5 erkannt", r["detail"], r)
        self.assertEqual(fake.HA["calls"], [], "the check switched something")
        fake.HA["intents"].pop(("de", "Spiele Einslive"))
        fake.HA["intents"][("de", "Was ist ein Igel")] = {"intent": {"name": "HassGetState"}, "match": True}
        r = self.check()
        self.assertIn("Absichten: 3 von 5 erkannt", r["detail"], r)
        details = " | ".join(f["detail"] for f in r["findings"] if f["severity"] == "gelb")
        self.assertIn("„Spiele Einslive“ wird nicht erkannt", details, r)
        self.assertIn("„Was ist ein Igel“ nimmt HassGetState, nicht Frage", details, r)
        # #933: the media sentences are Laib's own (pre-start): another intent taking them is no pass
        fake.HA["intents"][("de", "Spiele Musik von Queen")] = {"intent": {"name": "HassMediaSearchAndPlay"}, "match": True}
        r = self.check()
        details = " | ".join(f["detail"] for f in r["findings"] if f["severity"] == "gelb")
        self.assertIn("„Spiele Musik von Queen“ nimmt HassMediaSearchAndPlay, nicht LaibMusik", details, r)

    def test_without_an_exposed_switch_the_switch_is_said_unmeasured(self):
        fake.HA["registry"][0]["options"] = {}
        r = self.check()
        self.assertIn("Absichten: 4 von 4 erkannt · Schalter nicht geprüft", r["detail"], r)

    def test_without_the_persona_no_voice_line(self):
        r = self.check(VOICE_PERSONA="")
        self.assertNotIn("Sprache", r["detail"])

    def test_the_unit_hands_the_check_the_persona(self):
        unit = (SCRIPT.parent / "home-assistant-check.service").read_text()
        for v in ("VOICE_PERSONA", "VOICE_WAKEWORD", "VOICE_PHRASE"):
            self.assertIn("-e " + v, unit)
        self.assertIn('"VOICE_PHRASE=${PERSONA_PHRASE}"', unit, "a phrase with a space, quoted (#627)")


if __name__ == "__main__":
    unittest.main()
