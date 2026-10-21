"""#521: a photo or PDF sent to the Bote with "ablegen" lands in Paperless'
consume folder as Bote/<sender>/<title>.<ext>, and the person reads "Liegt in
Paperless, Titel …". The plugin (ablage_plugin.py) under fake Hermes events,
bote-notify's real /ablage over HTTP into a temporary consume folder, and
post-deploy turning the plugin on only while Paperless answers."""
import asyncio, importlib.util, json, os, subprocess, tempfile, threading, types, unittest, urllib.error, urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent


def load(name, file):
    spec = importlib.util.spec_from_file_location(name, HERE / file)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


notify = load("notify", "notify.py")
ablage = load("ablage", "ablage_plugin.py")
PDF = b"%PDF-1.7\n1 0 obj<<>>endobj\n%%EOF\n"
JPG = b"\xff\xd8\xff\xe0" + b"\0" * 64


class Ablage(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.consume = self.tmp / "consume"
        self.consume.mkdir()
        people = self.tmp / "people.json"
        people.write_text(json.dumps({"mama": ["signal:+4917012345678"]}))
        self.logs = []
        cfg = notify.Config(token="s3cret", people=str(people), consume=str(self.consume), log=self.logs.append, owner=(os.getuid(), os.getgid()))
        self.srv = notify.server(cfg, ("127.0.0.1", 0))
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.server_close)
        self.addCleanup(self.srv.shutdown)
        os.environ["ABLAGE_URL"] = "http://127.0.0.1:%d/ablage" % self.srv.server_port
        os.environ["NOTIFY_TOKEN"] = "s3cret"

    def attachment(self, data, name="doc_ab12.pdf"):
        p = self.tmp / name
        p.write_bytes(data)
        return str(p)

    def send(self, text, media, user="+4917012345678", name="Mama Handy"):
        """one message through both hooks as Hermes' gateway calls them; the reply, or None (the agent's turn)"""
        event = types.SimpleNamespace(text=text, media_urls=media, media_types=[])
        r = ablage.on_dispatch(event=event, gateway=None, session_store=None)
        if r is not None:
            self.assertEqual(r["action"], "rewrite")
            text = r["text"]
        source = {"platform": "signal", "user_id": user, "user_name": name, "chat_id": user}
        res = asyncio.run(ablage.on_admitted(session_key="k", platform="signal", source=source, message_id=None, text=text))
        if res is None:
            return None, text
        self.assertEqual(res["action"], "handled")
        return res["reply"], text

    def filed(self):
        return sorted(str(p.relative_to(self.consume)) for p in self.consume.rglob("*") if p.is_file())

    def test_a_pdf_with_ablegen_lands_tagged_with_the_sender(self):
        reply, _ = self.send("Stromrechnung ablegen", [self.attachment(PDF)])
        self.assertEqual(reply, "Liegt in Paperless, Titel „Stromrechnung“. Die Texterkennung braucht ein paar Minuten.")
        self.assertEqual(self.filed(), ["Bote/Mama/Stromrechnung.pdf"])
        self.assertEqual((self.consume / "Bote/Mama/Stromrechnung.pdf").read_bytes(), PDF)
        # the same title again: a second file, never replaced; nothing staged left behind
        reply, _ = self.send("bitte archivieren: Stromrechnung!", [self.attachment(PDF)])
        self.assertIn("„Stromrechnung (2)“", reply)
        self.assertEqual(self.filed(), ["Bote/Mama/Stromrechnung (2).pdf", "Bote/Mama/Stromrechnung.pdf"])
        self.assertFalse(any("Stromrechnung" in l for l in self.logs), "the title says what the letter is: not in the log")

    def test_without_a_title_sender_and_time_and_a_stranger_by_their_chat_name(self):
        reply, _ = self.send("Ablegen", [self.attachment(JPG, "img_1.jpg"), self.attachment(PDF)], user="+491", name="Onkel/Ben")
        self.assertTrue(reply.startswith("Liegen in Paperless: „Dokument von Onkel_Ben "), reply)
        files = self.filed()
        self.assertEqual(len(files), 2)
        self.assertTrue(all(f.startswith("Bote/Onkel_Ben/Dokument von Onkel_Ben ") for f in files), files)
        self.assertEqual(sorted(Path(f).suffix for f in files), [".jpg", ".pdf"])

    def test_what_is_not_filed(self):
        # no word, or no attachment: the agent's turn as before, the text untouched
        self.assertEqual(self.send("Was steht in diesem Brief?", [self.attachment(PDF)]), (None, "Was steht in diesem Brief?"))
        self.assertEqual(self.send("ablegen", []), (None, "ablegen"))
        self.assertEqual(self.send("Ablegestelle", [self.attachment(PDF)])[0], None)
        # what Paperless does not take: said, nothing in the folder
        reply, _ = self.send("ablegen", [self.attachment(b"PK\x03\x04 a zip", "x.zip")])
        self.assertTrue(reply.startswith("Das konnte ich nicht ablegen: nur Fotos"), reply)
        self.assertEqual(self.filed(), [])
        # a mark whose files are gone (Hermes restarted between the hooks): said, not passed to the model
        reply, _ = self.send("[laib-ablage:" + "0" * 32 + "] ablegen", [])
        self.assertIn("nicht mehr da", reply)

    def test_ablage_wants_the_bearer_and_a_consume_folder(self):
        def post(token, body):
            req = urllib.request.Request(os.environ["ABLAGE_URL"], data=json.dumps(body).encode(), method="POST",
                                         headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
            try:
                with urllib.request.urlopen(req, timeout=5) as r:
                    return r.status
            except urllib.error.HTTPError as e:
                return e.code
        import base64
        body = {"titel": "x", "plattform": "signal", "absender": "+1", "daten": base64.b64encode(PDF).decode()}
        self.assertEqual(post("wrong", body), 401)
        self.assertEqual(post("s3cret", {"titel": "x"}), 400)
        self.consume.rename(self.tmp / "gone")
        self.assertEqual(post("s3cret", body), 503)
        self.assertEqual(self.filed(), [])


class PostDeploy(unittest.TestCase):
    def run_hook(self, paperless_url):
        with tempfile.TemporaryDirectory() as d:
            env = dict(os.environ, LAIB_DATA_DIR=d, LAIB_RENDER_DIR=d + "/none", MODEL_URL="http://127.0.0.1:1/v1", SIGNAL_ACCOUNT="+490000",
                       MODEL_NAME="m", THINKING_MODEL="t", LAIB_SECRET_BOTE_LAIB_TOKEN="laib-tok", PAPERLESS_URL=paperless_url)
            p = subprocess.run(["bash", str(HERE / "post-deploy")], env=env, capture_output=True, text=True, timeout=60)
            self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
            h = Path(d, "bote/hermes")
            return ((h / "config.yaml").read_text(), (h / "SOUL.md").read_text(),
                    (h / "plugins/laib-ablage/__init__.py").exists(), p.stdout)

    def test_the_plugin_only_while_paperless_answers(self):
        class Paperless(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def do_GET(self):
                self.send_response(401)   # the API without a token: an answer
                self.end_headers()
        srv = HTTPServer(("127.0.0.1", 0), Paperless)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.shutdown)
        conf, soul, there, _ = self.run_hook("http://127.0.0.1:%d" % srv.server_port)
        self.assertIn("enabled: [laib-persona, laib-ablage]", conf)
        self.assertTrue(there)
        self.assertIn('mit dem Wort "ablegen"', soul)
        srv.shutdown(); srv.server_close()
        conf, soul, there, out = self.run_hook("http://127.0.0.1:%d" % srv.server_port)
        self.assertNotIn("laib-ablage", conf)
        self.assertFalse(there)
        self.assertNotIn("ablegen", soul)
        self.assertIn("ablage: Paperless", out)


if __name__ == "__main__":
    unittest.main()
