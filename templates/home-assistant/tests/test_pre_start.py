"""#253: Home Assistant keeps the family's Authelia login (owner, #229).

Laib's pre-start, before Home Assistant starts, installs
christiaangoossens/hass-oidc-auth from a SHA-256-pinned tag archive and
writes the block as a package Laib owns whole (packages/laib_auth_oidc.yaml,
the secret from the unit's environment, never in a file). The admin's
configuration.yaml is only added to. The archive is served here by a fake
GitHub."""
import hashlib, io, json, os, re, subprocess, sys, tarfile, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

T = Path(__file__).resolve().parent.parent
HOOK = T / "pre-start"


def archive(marker="1.1.0"):
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tf:
        for name, data in ((f"hass-oidc-auth-1.1.0/custom_components/auth_oidc/manifest.json", '{"domain": "auth_oidc", "version": "%s"}' % marker),
                           ("hass-oidc-auth-1.1.0/custom_components/auth_oidc/__init__.py", "# component\n"),
                           ("hass-oidc-auth-1.1.0/README.md", "not this\n")):
            info = tarfile.TarInfo(name)
            info.size = len(data.encode())
            tf.addfile(info, io.BytesIO(data.encode()))
    return buf.getvalue()


SERVED = {"body": archive(), "hits": 0}


class FakeGitHub(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        SERVED["hits"] += 1
        self.send_response(200)
        self.send_header("Content-Length", str(len(SERVED["body"])))
        self.end_headers()
        self.wfile.write(SERVED["body"])


# the shape of the Kraftwerk's configuration.yaml today (read-only, 2026-10-24)
KRAFTWERK = """default_config:

automation: !include automations.yaml
script: !include scripts.yaml
scene: !include scenes.yaml

# auth_oidc: moved to packages/laib_auth_oidc.yaml by laib (#253)

homeassistant:
  packages: !include_dir_named packages
"""


class PreStart(unittest.TestCase):
    def setUp(self):
        SERVED.update(body=archive(), hits=0)
        self.srv = HTTPServer(("127.0.0.1", 0), FakeGitHub)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.shutdown)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.c = Path(self.tmp.name) / "home-assistant" / "homeassistant"
        self.c.mkdir(parents=True)

    def run_hook(self, sha=None):
        env = dict(os.environ, LAIB_DATA_DIR=self.tmp.name, LAIB_DOMAIN="vm.test", OIDC_SECRET="laib-oidc-secret-Q9",
                   AUTH_OIDC_ARCHIVE=f"http://127.0.0.1:{self.srv.server_port}/v1.1.0.tar.gz",
                   AUTH_OIDC_SHA256=sha or hashlib.sha256(SERVED["body"]).hexdigest())
        return subprocess.run([sys.executable, str(HOOK)], env=env, capture_output=True, text=True, timeout=60)

    def everything(self):
        return "".join(p.read_text(errors="replace") for p in self.c.rglob("*") if p.is_file())

    def test_a_fresh_home_assistant_starts_with_the_family_login(self):
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertTrue((self.c / "custom_components/auth_oidc/manifest.json").exists())
        self.assertFalse((self.c / "custom_components/README.md").exists(), "more than the component was unpacked")
        pkg = (self.c / "packages/laib_auth_oidc.yaml").read_text()
        for want in ("auth_oidc:", "client_id: homeassistant", "client_secret: !env_var AUTH_OIDC_SECRET",
                     "discovery_url: https://auth.vm.test/.well-known/openid-configuration",
                     "automatic_user_linking: true", "automatic_person_creation: true", "admin: admins", "user: family"):
            self.assertIn(want, pkg)
        conf = (self.c / "configuration.yaml").read_text()
        self.assertIn("default_config:", conf)
        self.assertRegex(conf, r"(?m)^homeassistant:\n  packages: !include_dir_named packages$")
        for f in ("automations.yaml", "scripts.yaml", "scenes.yaml", "packages/laib_mitteilung.yaml"):
            self.assertTrue((self.c / f).exists(), f)
        self.assertNotIn("laib-oidc-secret-Q9", self.everything(), "the OIDC secret is in a file; it belongs in the unit's environment")
        hits = SERVED["hits"]
        before = {p: p.read_bytes() for p in self.c.rglob("*") if p.is_file()}
        p = self.run_hook()
        self.assertEqual((p.returncode, SERVED["hits"]), (0, hits), "the pinned archive was fetched again")
        self.assertEqual(before, {p: p.read_bytes() for p in self.c.rglob("*") if p.is_file()}, "a second run changed files")

    # Ablauf L-47
    def test_the_tone_for_a_wake_nobody_meant_is_short_quiet_and_falling(self):
        import array, wave
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        f = self.c / "www/laib/falsch.wav"   # HA serves it at /local/laib/falsch.wav, Frage's FRAGE_TONE
        with wave.open(str(f)) as w:
            self.assertEqual((w.getnchannels(), w.getsampwidth()), (1, 2))
            rate, pcm = w.getframerate(), array.array("h", w.readframes(w.getnframes()))
        self.assertAlmostEqual(len(pcm) / rate, 0.4, delta=0.05)
        self.assertLess(max(map(abs, pcm)), 0.25 * 32767, "not quiet")
        self.assertLess(max(abs(pcm[0]), abs(pcm[-1])), 300, "it clicks: no soft start or end")

        def pitch(part):   # zero crossings per second, halved
            return sum(1 for a, b in zip(part, part[1:]) if (a < 0) != (b < 0)) / (len(part) / rate) / 2
        first, second = pitch(pcm[:int(0.15 * rate)]), pitch(pcm[int(0.15 * rate):])
        self.assertGreater(first, second * 1.2, f"not a falling „düü-doow“: {first:.0f} Hz, then {second:.0f} Hz")
        before = f.read_bytes()
        p = self.run_hook()
        self.assertEqual(f.read_bytes(), before)
        self.assertNotIn("falsch.wav", p.stdout, "a second run wrote the tone again")

    def test_the_admins_configuration_is_left_as_it_is(self):
        (self.c / "configuration.yaml").write_text(KRAFTWERK)
        (self.c / ".storage").mkdir()
        (self.c / ".storage/auth_provider.auth_oidc").write_text('{"linked": "anna"}')
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual((self.c / "configuration.yaml").read_text(), KRAFTWERK)
        self.assertEqual((self.c / ".storage/auth_provider.auth_oidc").read_text(), '{"linked": "anna"}', "the linked users were touched")
        self.assertTrue((self.c / "custom_components/auth_oidc/manifest.json").exists())
        self.assertIn("client_id: homeassistant", (self.c / "packages/laib_auth_oidc.yaml").read_text())

    def test_an_archive_that_is_not_the_pinned_one_is_refused_and_nothing_replaced(self):
        good = self.run_hook()
        self.assertEqual(good.returncode, 0, good.stderr)
        pinned = hashlib.sha256(SERVED["body"]).hexdigest()
        SERVED["body"] = archive("evil")
        (self.c / "custom_components/auth_oidc/.laib-archive").unlink()   # a reinstall is due
        p = self.run_hook(sha=pinned)
        self.assertNotEqual(p.returncode, 0, "a changed archive was accepted")
        self.assertIn("SHA-256", p.stderr)
        self.assertIn('"1.1.0"', (self.c / "custom_components/auth_oidc/manifest.json").read_text())

    def test_the_unit_and_the_contract(self):
        unit = (T / "home-assistant.container").read_text()
        self.assertIn("Environment=AUTH_OIDC_SECRET=${OIDC_SECRET}", unit)
        contract = (T / "laib.yml").read_text()
        self.assertRegex(contract, r"(?s)oidc:[^\n]*\n\s+clientId: homeassistant.*/auth/oidc/callback.*tokenEndpointAuthMethod: client_secret_post")
        self.assertIn("OIDC_SECRET: !secret OIDC_HOMEASSISTANT_SECRET", (T / "values.example.yml").read_text())
        # the config is pre-start's: it must be there before Home Assistant starts; post-deploy only mints the tokens (#349).
        # It may move a HACS integration HACS unpacked one level too deep (#528), never auth_oidc, pre-start's
        hook = (T / "post-deploy").read_text()
        self.assertFalse(any(w in hook for w in ("configuration.yaml", "packages/", "auth_oidc")), "post-deploy writes config")

    def test_the_zwave_network_keys_are_the_containers_alone(self):
        # #664: zwave-js-ui's settings.json holds the S0/S2 network keys and was 0644 on the box, with
        # ServiceBay's copies next to it (settings.json.servicebay, .sb-bak-…). Every apply makes them 0600;
        # the other files of the store, and a link, stay as they are
        z = Path(self.tmp.name) / "home-assistant" / "zwave-js"
        z.mkdir(parents=True)
        keys = ["settings.json", "settings.json.servicebay", "settings.json.sb-bak-1780442083", "users.json"]   # users.json: the UI login's hash
        for f in keys + ["nodes.json"]:
            (z / f).write_text("{}")
            (z / f).chmod(0o644)
        (z / "settings.json.link").symlink_to(z / "nodes.json")
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        for f in keys:
            self.assertEqual((z / f).stat().st_mode & 0o777, 0o600, f)
        self.assertEqual((z / "nodes.json").stat().st_mode & 0o777, 0o644, "a file without keys was touched")
        self.assertIn("settings.json", p.stdout)
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertNotIn("0600", p.stdout, "a second run changed something")
        # a box without Z-Wave (no store) is no error
        for f in z.iterdir():
            f.unlink()
        z.rmdir()
        self.assertEqual(self.run_hook().returncode, 0)

    def test_the_media_sentences_and_their_answers(self):
        # #933: ADR 0015's check on the box knew 3 of 5: „Spiele Einslive“ and „Spiele Musik von Queen“ were not
        # recognized. HA's German media sentences without a named player need the satellite's area, and Music
        # Assistant's players have none, so neither matched. Laib's own sentences take them: the radio by the
        # house's stations (the share's radio/, as Music Assistant reads them, #646) with how they are said
        # („1LIVE“ is „Einslive“), the music by artist; an intent_script plays them on the room's player.
        radio = Path(self.tmp.name) / "file-share" / "data" / "radio"
        radio.mkdir(parents=True)
        (radio / "1LIVE.strm").write_text("https://example.invalid/1live.mp3\n")
        (radio / "NDR 2.strm").write_text("https://example.invalid/ndr2.mp3\n")
        (radio / "liste.m3u").write_text('#EXTM3U\n#EXTINF:-1 tvg-logo="x", Deutschlandfunk (DLF)\nhttps://example.invalid/dlf\n'
                                         '#EXTINF:-1, RADIO BOB!\nhttps://example.invalid/bob\n#EXTINF:-1, 1LIVE\nhttps://example.invalid/1live.mp3\n')
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        text = (self.c / "custom_sentences/de/laib_medien.yaml").read_text()
        self.assertTrue(text.startswith("# "), "a comment says who owns the file")
        doc = json.loads(text.split("\n", 1)[1])   # JSON is YAML: Home Assistant reads it, the test needs no yaml module
        self.assertEqual((doc["language"], sorted(doc["intents"])), ("de", ["LaibLautstaerke", "LaibMusik", "LaibPodcast", "LaibRadio"]))
        said = {}
        for v in doc["lists"]["laib_sender"]["values"]:
            said.setdefault(v["out"], set()).update(x.strip().lower() for x in v["in"].strip("()").split("|"))   # hassil ignores case
        self.assertEqual(sorted(said), ["1LIVE", "Deutschlandfunk (DLF)", "NDR 2", "RADIO BOB!"], said)
        self.assertTrue({"1live", "einslive", "eins live"} <= said["1LIVE"], said["1LIVE"])
        # 2026-10-31 on the box Whisper wrote „Spieler 1 live.“: the digit apart from the word, and „Spieler“
        self.assertIn("1 live", said["1LIVE"], said["1LIVE"])
        self.assertTrue({"ndr 2", "ndr zwei"} <= said["NDR 2"], said["NDR 2"])
        for x in set().union(*said.values()):
            self.assertNotRegex(x, r"[()\[\]{}<>|!]", "a template character in a spoken name")
        self.assertTrue(doc["lists"]["laib_kuenstler"]["wildcard"])
        sentences = " ".join(s for n in ("LaibRadio", "LaibMusik") for d in doc["intents"][n]["data"] for s in d["sentences"])
        for want in ("{laib_sender}", "(Musik|Lieder", "{laib_kuenstler}"):
            self.assertIn(want, sentences)
        for heard in ("spieler", "spiel ", "spiele "):   # how Whisper hears „Spiele“: the verb takes them all
            self.assertIn(heard.strip(), sentences.lower(), heard)
        pkg = (self.c / "packages/laib_medien.yaml").read_text()
        for want in ("intent_script:", "LaibRadio:", "LaibMusik:", "music_assistant.play_media", "media_type: radio", "media_type: artist",
                     "preferred_area_id", "integration_entities('music_assistant')"):
            self.assertIn(want, pkg)
        # Ablauf L-45 @7904. Owner 2026-11-12: a room's standard speaker, HA's label „Standard-Lautsprecher“ on Music Assistant's player in
        # the satellite's area, comes first; without one, the room's Music Assistant player as before
        player = pkg.split('player: "', 1)[1].split('"\n', 1)[0]
        self.assertIn("label_entities('Standard-Lautsprecher') | select('in', ma) | select('in', area_entities(a))", player)
        self.assertLess(player.index("std +"), player.index("(ma | select('in', area_entities(a))"), "the standard speaker first")
        self.assertNotIn('"', player, "a double quote would end the YAML string")
        # post-deploy reloads Home Assistant's sentences once, after they changed
        self.assertTrue((self.c / ".laib-sentences-changed").exists())
        (self.c / ".laib-sentences-changed").unlink()
        before = {p: p.read_bytes() for p in self.c.rglob("*") if p.is_file()}
        self.assertEqual(self.run_hook().returncode, 0)
        self.assertEqual(before, {p: p.read_bytes() for p in self.c.rglob("*") if p.is_file()}, "a second run changed files")
        # no stations on the share: the music sentence alone
        for f in radio.iterdir():
            f.unlink()
        self.assertEqual(self.run_hook().returncode, 0)
        doc = json.loads((self.c / "custom_sentences/de/laib_medien.yaml").read_text().split("\n", 1)[1])
        self.assertEqual((sorted(doc["intents"]), "laib_sender" in doc["lists"]), (["LaibLautstaerke", "LaibMusik", "LaibPodcast"], False))
        self.assertTrue((self.c / ".laib-sentences-changed").exists())
        # the hook sees only the declared data paths: the share's stations, read, not backed up twice
        self.assertRegex((T / "laib.yml").read_text(), r"- path: \$\{DATA_DIR\}/file-share/data/radio[^\n]*\n\s+backup: false")

    # the evidence of 2026-11-19 (#1272) as a fixture: a Voice PE in the Wohnzimmer, one more satellite, one disabled
    VOICE = "assist_satellite.home_assistant_voice_0907c9_assist_satellit"

    def medien(self, satellites=(VOICE, "assist_satellite.kueche"), stations=("1LIVE", "NDR 2")):
        """(the rendered sentences, the rendered package, the hook's run) for a house with these satellites and stations"""
        radio = Path(self.tmp.name) / "file-share" / "data" / "radio"
        radio.mkdir(parents=True, exist_ok=True)
        for s in stations:
            (radio / f"{s}.strm").write_text("https://example.invalid/x.mp3\n")
        if satellites is not None:
            (self.c / ".storage").mkdir(exist_ok=True)
            (self.c / ".storage/core.entity_registry").write_text(json.dumps({"data": {"entities": [
                {"entity_id": "light.sofa", "disabled_by": None}, {"entity_id": "assist_satellite.aus", "disabled_by": "user"},
                {"entity_id": "media_player.jbl_bar_300_ecdf_2", "disabled_by": None}] + [{"entity_id": s, "disabled_by": None} for s in satellites]}}))
        p = self.run_hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return (json.loads((self.c / "custom_sentences/de/laib_medien.yaml").read_text().split("\n", 1)[1]),
                (self.c / "packages/laib_medien.yaml").read_text(), p)

    def heard(self, doc, text):
        """which of Laib's intents takes a sentence, and with which slots: hassil's templates as far as pre-start
        writes them ([optional], (a|b), {list} as a wildcard, a range or values), a fixed text before a wildcard's"""
        def rx(tpl, lists):
            out, i = "", 0
            for m in re.finditer(r"\{(\w+)\}|[\[\]()|]", tpl):
                out += re.escape(tpl[i:m.start()])
                i, name, l = m.end(), m.group(1), doc["lists"].get(m.group(1) or "", {})
                if not name:
                    out += {"[": "(?:", "]": ")?", "(": "(?:", ")": ")", "|": "|"}[m.group()]
                elif l.get("wildcard"):
                    out += f"(?P<{name}>\\S.*?)"
                elif "range" in l:
                    out += f"(?P<{name}>\\d+)"
                else:
                    lists[name] = [(re.compile(rx(v["in"], {}), re.I), v["out"]) for v in l["values"]]
                    out += f"(?P<{name}>" + "|".join(rx(v["in"], {}) for v in l["values"]) + ")"
            return out + re.escape(tpl[i:])
        said, hits = re.sub(r"\s+", " ", text.strip().rstrip(".!?")), []
        for intent, body in doc["intents"].items():
            for d in body["data"]:
                for s in d["sentences"]:
                    lists = {}
                    if not (m := re.fullmatch(rx(s, lists), said, re.I)):
                        continue
                    slots = dict(d.get("slots") or {})
                    for k, v in m.groupdict().items():
                        r = doc["lists"][k].get("range")
                        if r and not r["from"] <= int(v) <= r["to"]:
                            break
                        slots[k] = int(v) if r else next(out for pat, out in lists[k] if pat.fullmatch(v)) if k in lists else v
                    else:
                        free = sum(len(v) for k, v in m.groupdict().items() if doc["lists"][k].get("wildcard"))
                        hits.append((free, intent, slots))
        return min(hits, key=lambda h: h[0])[1:] if hits else (None, {})

    # Ablauf L-60
    def test_the_volume_sentences(self):
        """#1272: „Lautstärke auf 10 %“ went to HA's own intent and from there to three unreachable players. Laib's
        own sentences take a value and a step; the sentences are Whisper's of 2026-11-19 and the owner's list"""
        doc, _, _ = self.medien()
        for text, want in {"Lautstärke auf 10 %.": ("auf", 10), "Lautstärke auf 40%": ("auf", 40), "Lautstärke auf 25 Prozent.": ("auf", 25),
                           "Lautstärke auf 1 %": ("auf", 1), "Stell die Lautstärke auf 30 Prozent": ("auf", 30), "Stelle Lautstärke auf 0": ("auf", 0),
                           "Mach es auf 100 Prozent": ("auf", 100), "Mach auf 15 %": ("auf", 15),
                           "Lauter.": ("lauter", None), "mach lauter": ("lauter", None), "Mach die Musik lauter": ("lauter", None), "Mache es lauter": ("lauter", None),
                           "Lautstärke um 20 Prozent erhöhen": ("lauter", 20), "leiser": ("leiser", None), "Mach es leiser.": ("leiser", None),
                           "Die Lautstärke um 5 % reduzieren": ("leiser", 5), "Lautstärke um 15 verringern": ("leiser", 15)}.items():
            intent, slots = self.heard(doc, text)
            self.assertEqual((intent, slots.get("laib_richtung"), slots.get("laib_prozent")), ("LaibLautstaerke",) + want, text)
        for text in ("Lautstärke auf 150 Prozent", "Mach das Licht an", "Mach es auf", "Lauter Unsinn", "Mach es auf 20"):   # a lamp is dimmed „auf 20“
            self.assertEqual(self.heard(doc, text)[0], None, text)

    # Ablauf L-61
    def test_the_podcast_sentences_leave_the_radio_and_the_music_theirs(self):
        """#1272: „Spiele die aktuelle Lage der Nation.“ reached nothing. A podcast by its name, with or without the
        word; the station and the artist stay LaibRadio's and LaibMusik's (the wildcard takes neither)"""
        doc, pkg, _ = self.medien()
        for text, name in {"Spiele die aktuelle Lage der Nation.": "Lage der Nation", "Spiele den Podcast Lage der Nation": "Lage der Nation",
                           "Spiel Podcast Fest und Flauschig": "Fest und Flauschig", "Spieler den Podcast Zeit Verbrechen.": "Zeit Verbrechen",
                           "Spiele die neueste Folge von Lage der Nation": "Lage der Nation", "Spiele die aktuelle Folge Lage der Nation": "Lage der Nation",
                           "Spiele die letzte Folge vom Podcast Zeit Verbrechen": "Zeit Verbrechen", "Spiele den neuesten Tatort": "Tatort",
                           "Spiele in der neueste Folge Lage der Nation": "Lage der Nation", "Spiele die Folge von Lage der Nation": "Lage der Nation"}.items():
            self.assertEqual(self.heard(doc, text), ("LaibPodcast", {"laib_podcast": name}), text)
        for text, want in {"Spiele Einslive": ("LaibRadio", {"laib_sender": "1LIVE"}), "Spieler 1 live.": ("LaibRadio", {"laib_sender": "1LIVE"}),
                           "Spiele das Radio NDR zwei": ("LaibRadio", {"laib_sender": "NDR 2"}), "Radio Einslive anmachen": ("LaibRadio", {"laib_sender": "1LIVE"}),
                           "Spiele Musik von Queen": ("LaibMusik", {"laib_kuenstler": "Queen"}), "Spiele etwas von Frank Turner": ("LaibMusik", {"laib_kuenstler": "Frank Turner"}),
                           "Songs von Queen abspielen": ("LaibMusik", {"laib_kuenstler": "Queen"}), "Spiele Lage der Nation": (None, {})}.items():
            self.assertEqual(self.heard(doc, text), want, text)
        script = pkg.split("  LaibPodcast:\n", 1)[1].split("  LaibLautstaerke:\n", 1)[0]
        for want in ("music_assistant.play_media", 'media_id: "{{ laib_podcast }}"', "media_type: podcast", "enqueue: replace",
                     "'Ich spiele den Podcast ' ~ laib_podcast", "Ich finde hier keinen Lautsprecher."):
            self.assertIn(want, script)
        self.assertEqual(script.split('player: "', 1)[1].split('"\n', 1)[0], pkg.split('player: "', 1)[1].split('"\n', 1)[0], "not the player LaibRadio plays on")

    def templates(self, pkg, key):
        """the templates of the rendered package under a key, each one YAML string on one line without a double quote in it"""
        got = re.findall(r'(?m)^ +' + re.escape(key) + r': "(.*)"$', pkg)
        for t in got:
            self.assertNotIn('"', t, "a double quote would end the YAML string")
            self.assertNotIn("\\", t, "a backslash is an escape in a YAML string")
            self.assertEqual((t.count("{%"), t.count("{{")), (t.count("%}"), t.count("}}")), t)
            self.assertEqual((t.count("{% for"), t.count("{% if")), (t.count("{% endfor %}"), t.count("{% endif %}")), t)
        return got

    # Ablauf L-60
    def test_the_volume_goes_to_what_plays_in_the_room(self):
        """#1272: whose volume: the players of the satellite's area that play, without the satellite's own, Music
        Assistant's alone when one of them plays (a speaker that is there twice gets a step once), else the room's
        standard speaker as LaibRadio picks it, else none and the answer says so. What Home Assistant makes of the
        template is not measured here: no Home Assistant runs in this test."""
        _, pkg, _ = self.medien()
        script = pkg.split("  LaibLautstaerke:\n", 1)[1].split("automation:\n", 1)[0]
        ziele, handed = self.templates(script, "ziele")
        self.assertEqual(handed, "{{ ziele }}", "the answer does not get what was set")
        player = pkg.split('player: "', 1)[1].split('"\n', 1)[0]
        self.assertTrue(ziele.startswith(player.rsplit("{{", 1)[0]), "not the standard speaker LaibRadio plays on")
        for want in ("preferred_area_id",                                                    # the room of the satellite that heard it
                     "{% set on = room | select('is_state', 'playing') | list %}",           # what plays there
                     "for p in (on | select('in', ma) | list) or on or ([player] if player else [])",   # MA's alone, else the others, else the standard
                     "states.assist_satellite | map(attribute='entity_id') | map('device_id')", "ns.own + device_entities(d)", "reject('in', ns.own)",   # never the satellite's own
                     "laib_prozent | default(10)",                                           # „lauter“ alone is a step of ten
                     "step if laib_richtung == 'auf' else now + step if laib_richtung == 'lauter' else now - step",
                     "([0, ", ", 100] | sort)[1]",                                           # 0 to 100
                     "state_attr(p, 'volume_level')"):
            self.assertIn(want, ziele)
        self.assertLess(ziele.index("reject('in', ns.own)"), ziele.index("set on ="), "the satellite's own players are out before anything is picked")
        for want in ('for_each: "{{ ziele }}"', "action: media_player.volume_set", "continue_on_error: true", 'entity_id: "{{ repeat.item[0] }}"',
                     'volume_level: "{{ repeat.item[1] / 100 }}"', "response_variable: result",
                     "('Lautstärke ' ~ action_response.ziele[0][1] ~ ' Prozent.') if action_response.ziele else 'Ich finde hier keinen Lautsprecher.'"):
            self.assertIn(want, script)
        # the template names no entity of a box: the fixture's are evidence, not configuration
        hook = HOOK.read_text()
        for box in ("jbl", "0907c9", "wohnzimmer_paar", "media_player.tv"):
            self.assertNotIn(box, hook.replace("media_player.wohnzimmer_2 is the Wohnzimmer's", ""))

    # Ablauf L-62
    def test_what_plays_in_the_room_is_muted_while_its_satellite_listens(self):
        """#1272: the box's own automation muted one named soundbar; Whisper misheard three of ten sentences once
        anything else played. Rendered for every satellite of Home Assistant's entity registry: what plays in its
        area and is not muted goes quiet, and exactly that comes back when it stops listening or after 20 s."""
        _, pkg, p = self.medien()
        auto = pkg.split("automation:\n", 1)[1]
        self.assertEqual(re.findall(r"(?m)^          - (\S+)$", auto.split("to: listening", 1)[0]), [self.VOICE, "assist_satellite.kueche"],
                         "every satellite that is not disabled, nothing else")
        for want in ("id: laib_stumm_beim_zuhoeren", "mode: parallel", "platform: state", "to: listening", "timeout: 20",
                     "wait_template: \"{{ not is_state(trigger.entity_id, 'listening') }}\"", 'value_template: "{{ stumm | count > 0 }}"'):
            self.assertIn(want, auto)
        [stumm] = self.templates(auto, "stumm")
        for want in ("{% set a = area_id(trigger.entity_id) | default('', true) %}", "reject('in', ns.own)",
                     "{{ room | select('is_state', 'playing') | reject('is_state_attr', 'is_volume_muted', true) | list }}"):
            self.assertIn(want, stumm)
        # muted, waited, unmuted, in this order; both over the one list this run made, each player its own call
        calls = re.findall(r'for_each: "\{\{ (\w+) \}\}"\n +sequence:\n +- action: (\S+)\n +(continue_on_error: true)\n +target:\n +entity_id: "\{\{ repeat.item \}\}"\n +data:\n +(.*)\n', auto)
        self.assertEqual(calls, [("stumm", "media_player.volume_mute", "continue_on_error: true", "is_volume_muted: true"),
                                 ("stumm", "media_player.volume_mute", "continue_on_error: true", "is_volume_muted: false")])
        self.assertLess(auto.index("is_volume_muted: true"), auto.index("wait_template"))
        self.assertLess(auto.index("wait_template"), auto.index("is_volume_muted: false"))
        self.assertEqual(auto.count('stumm: "'), 1, "the list is made once, at the trigger: the unmute takes what was muted")
        self.assertIn("stumm beim Zuhören für 2 Sprachgerät(e)", p.stdout)
        # a satellite joins: the next apply renders it and leaves the marker post-deploy reloads on
        (self.c / ".laib-sentences-changed").unlink()
        self.assertEqual(self.run_hook().returncode, 0)
        self.assertFalse((self.c / ".laib-sentences-changed").exists(), "nothing changed, and a reload was asked for")
        _, pkg, _ = self.medien(satellites=(self.VOICE, "assist_satellite.kueche", "assist_satellite.bad"))
        self.assertIn("          - assist_satellite.bad\n", pkg)
        self.assertTrue((self.c / ".laib-sentences-changed").exists(), "a new satellite, and no reload")
        # no satellite, a registry that is not there or not JSON: no automation (a state trigger without an entity is refused), the intents stay
        for satellites, registry in (((), None), (None, "{ not json"), (None, None)):
            if satellites is None:
                (self.c / ".storage/core.entity_registry").unlink()
            if registry:
                (self.c / ".storage/core.entity_registry").write_text(registry)
            _, pkg, _ = self.medien(satellites=satellites)
            self.assertNotIn("automation:", pkg)
            self.assertIn("  LaibLautstaerke:\n", pkg)


if __name__ == "__main__":
    unittest.main()
