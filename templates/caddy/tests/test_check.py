"""#785: caddy-check.py writes the proxy's detail line for the Box page: how
many addresses Caddy serves (from its admin API's running config) and until
when the certificates it hands out are valid (one TLS handshake per address,
as a phone would see it). A fake admin API and a TLS server with a certificate
made here stand in; a silent admin API is "nicht gemessen", a certificate
running out within 14 days or an address without one is a yellow finding."""
import datetime, json, os, ssl, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "caddy-check.py"
SECRET = "proxy-secret-Kq9"


def cert(days):
    d = tempfile.mkdtemp()
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", d + "/k.pem", "-out", d + "/c.pem",
                    "-days", str(days), "-subj", "/CN=example.org"], check=True, capture_output=True)
    return d + "/c.pem", d + "/k.pem"


def config(hosts):
    return {"admin": {"listen": "localhost:2019"}, "apps": {"http": {"servers": {
        "srv0": {"listen": [":443"], "routes": [
            {"match": [{"host": [h]}], "handle": [{"handler": "subroute", "routes": [{"handle": [
                {"handler": "reverse_proxy", "headers": {"request": {"set": {"X-Haus-Proxy": [SECRET]}}}}]}]}]} for h in hosts]
            + [{"match": [{"host": ["*.example.org"]}], "handle": []}, {"handle": [{"handler": "static_response"}]}]},
        "srv1": {"listen": [":80"], "routes": [{"match": [{"host": [hosts[0]]}], "handle": []}]}}}}}


class Admin:
    def __init__(self, cfg, code=200):
        outer = self
        self.seen = []

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def do_GET(self):
                outer.seen.append(self.path)
                sub = cfg
                for k in self.path.strip("/").split("/")[1:]:
                    sub = sub[k]
                b = json.dumps(sub).encode()
                self.send_response(code); self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"


class TLS:
    """serves the certificate to every name but those in without (an alert, as Caddy without a certificate)"""
    def __init__(self, days, without=()):
        c, k = cert(days)
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain(c, k)

        def sni(sock, name, _):
            if name in without:
                return ssl.ALERT_DESCRIPTION_INTERNAL_ERROR
        ctx.sni_callback = sni
        import socket
        self.sock = socket.socket(); self.sock.bind(("127.0.0.1", 0)); self.sock.listen(16)
        self.port = self.sock.getsockname()[1]

        def loop():
            while True:
                conn, _ = self.sock.accept()
                def one(conn=conn):
                    try:
                        s = ctx.wrap_socket(conn, server_side=True); s.close()
                    except (OSError, ssl.SSLError):
                        conn.close()
                threading.Thread(target=one, daemon=True).start()
        threading.Thread(target=loop, daemon=True).start()


def run(admin, tls_port):
    d = tempfile.mkdtemp()
    env = {"PATH": os.environ["PATH"], "CADDY_ADMIN": admin, "TLS_HOST": "127.0.0.1", "TLS_PORT": str(tls_port), "STATUS_DIR": d}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=120)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


HOSTS = ["home.example.org", "photos.example.org", "media.example.org"]


def until(days):
    return (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=days)).strftime("%d.%m.%Y")


class Check(unittest.TestCase):
    def test_addresses_and_earliest_certificate(self):
        a, t = Admin(config(HOSTS)), TLS(90)
        r, st = run(a.url, t.port)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(st["detail"], ["3 Adressen · Zertifikate gültig bis " + until(d) for d in (89, 90)])
        self.assertEqual(st["findings"], [])
        self.assertEqual(a.seen, ["/config/apps/http/servers"])
        self.assertNotIn(SECRET, r.stdout + r.stderr + json.dumps(st))

    def test_certificate_running_out_is_yellow(self):
        _, st = run(Admin(config(HOSTS)).url, TLS(5).port)
        self.assertIn("Zertifikate gültig bis", st["detail"])
        self.assertEqual(st["findings"][0]["severity"], "gelb")
        self.assertIn("läuft in", st["findings"][0]["detail"])

    def test_address_without_certificate_is_yellow(self):
        _, st = run(Admin(config(HOSTS)).url, TLS(90, without={"photos.example.org"}).port)
        self.assertTrue(st["detail"].startswith("3 Adressen · Zertifikate gültig bis"), st["detail"])
        self.assertIn("1 ohne Zertifikat", st["detail"])
        self.assertEqual(st["findings"][0]["severity"], "gelb")
        self.assertIn("photos.example.org", st["findings"][0]["detail"])

    def test_admin_api_down_is_not_measured(self):
        _, st = run("http://127.0.0.1:9", TLS(90).port)
        self.assertTrue(st["detail"].startswith("nicht gemessen: Caddys Admin-Schnittstelle antwortet nicht"), st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])
        self.assertNotIn("Adressen", st["detail"])


if __name__ == "__main__":
    unittest.main()
