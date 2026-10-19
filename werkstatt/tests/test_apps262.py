"""#262: a small app from the Werkstatt to <name>.<domain> — the scaffold
builds, tests and lints, the registry proposal is a box.yml patch the
kernel applies, `laib-app check` tells DNS, certificate and login apart,
and the guide reaches both agents and names only commands that exist. The
whole path on a real Caddy and Authelia is scripts/werkstatt-app.sh."""
import importlib.util, json, os, re, shutil, ssl, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent          # werkstatt/
ROOT = HERE.parent
APP = HERE / "laibapp.py"


def load(path: Path, name: str):
    spec = importlib.util.spec_from_file_location(name, path)
    m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
    return m


def laib_app(*args, env=None, **kw):
    e = dict(os.environ, LAIB_APP_SCAFFOLD=str(HERE / "scaffold"), **(env or {}))
    return subprocess.run([sys.executable, str(APP), *args], env=e, capture_output=True, text=True, timeout=120, **kw)


def kernel() -> str:
    """The laib binary: the one CI built, or one built here."""
    built = ROOT / "laib"
    if built.exists():
        return str(built)
    out = Path(tempfile.mkdtemp()) / "laib"
    subprocess.run(["go", "build", "-o", str(out), "./cmd/laib"], cwd=ROOT, check=True, env=dict(os.environ, CGO_ENABLED="0"))
    return str(out)


class Scaffold(unittest.TestCase):
    def test_a_new_app_tests_green_and_its_template_lints(self):
        with tempfile.TemporaryDirectory() as d:
            repo = Path(d) / "laib-apps"
            p = laib_app("new", "hallo", "--repo", str(repo), "--owner", "mdopp")
            self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
            for f in ("apps/hallo/main.go", "apps/hallo/main_test.go", "apps/hallo/Containerfile", "templates/hallo/laib.yml",
                      "templates/hallo/hallo.container", "templates/hallo/values.example.yml", ".github/workflows/apps.yml", "AGENTS.md", "CLAUDE.md"):
                self.assertTrue((repo / f).is_file(), f)
            for f in repo.rglob("*"):
                if f.is_file() and ".git" not in f.parts:
                    self.assertNotRegex(f.read_text(), r"__[A-Za-z]+__", f"placeholder left in {f}")
            self.assertIn("X-Hallo-Proxy", (repo / "apps/hallo/main.go").read_text(), "the header laib renders for proxySecret")
            if shutil.which("go"):
                t = subprocess.run(["go", "test", "./..."], cwd=repo / "apps/hallo", capture_output=True, text=True, timeout=300)
                self.assertEqual(t.returncode, 0, t.stdout + t.stderr)
                lint = subprocess.run([kernel(), "template", "lint", str(repo / "templates/hallo")], capture_output=True, text=True, timeout=60)
                self.assertEqual(lint.returncode, 0, lint.stdout + lint.stderr)
            contract = (repo / "templates/hallo/laib.yml").read_text()
            for want in ("exposure: internal", "auth: one_factor", "group: family", "proxySecret: HALLO_PROXY_SECRET", "lan: false"):
                self.assertIn(want, contract)
            self.assertIn("!secret HALLO_PROXY_SECRET", (repo / "templates/hallo/values.example.yml").read_text())

    def test_the_ci_publishes_only_after_the_tests(self):
        import yaml
        wf = yaml.safe_load((HERE / "scaffold/repo/.github/workflows/apps.yml").read_text())
        image = wf["jobs"]["image"]
        self.assertIn("test", image["needs"])
        self.assertTrue(image["if"].startswith("github.event_name == 'push'"))
        self.assertIn("templates", wf["jobs"])

    def test_names_and_existing_apps_are_refused(self):
        with tempfile.TemporaryDirectory() as d:
            for bad in ("Hallo", "h", "hallo-welt", "1x"):
                self.assertNotEqual(laib_app("new", bad, "--repo", d).returncode, 0, bad)
            self.assertEqual(laib_app("new", "hallo", "--repo", d).returncode, 0)
            p = laib_app("new", "hallo", "--repo", d)
            self.assertNotEqual(p.returncode, 0)
            self.assertIn("exists", p.stderr)


class Register(unittest.TestCase):
    def test_the_registry_is_a_box_yml_patch_the_kernel_applies(self):
        with tempfile.TemporaryDirectory() as d:
            fake = Path(d) / "laib"
            fake.write_text(f'#!/bin/sh\nprintf "%s\\n" "$@" > {d}/argv\ncat > {d}/stdin\necho "Vorschlag #3"\n')
            fake.chmod(0o755)
            p = laib_app("register", "https://github.com/mdopp/laib-apps", env={"PATH": f"{d}:{os.environ['PATH']}"})
            self.assertEqual(p.returncode, 0, p.stderr)
            self.assertEqual((Path(d) / "argv").read_text().splitlines()[:2], ["propose", "--patch"])
            box = Path(d) / "state"
            box.mkdir()
            (box / "box.yml").write_text("domain: example.home\nroles: {proxy: caddy}\n")
            subprocess.run(["git", "init", "-q"], cwd=box, check=True)
            subprocess.run(["git", "add", "box.yml"], cwd=box, check=True)
            a = subprocess.run(["git", "apply", "--index", "--unidiff-zero", str(Path(d) / "stdin")], cwd=box, capture_output=True, text=True)
            self.assertEqual(a.returncode, 0, a.stderr)   # internal/proposal applies a patch exactly so
            import yaml
            doc = yaml.safe_load((box / "box.yml").read_text())
            self.assertEqual(doc["registries"], [{"name": "apps", "url": "https://github.com/mdopp/laib-apps", "ref": "main"}])
            self.assertEqual(doc["domain"], "example.home")


class _DoH(BaseHTTPRequestHandler):
    answers: dict = {}
    def log_message(self, *a): pass
    def do_GET(self):
        from urllib.parse import parse_qs, urlparse
        name = parse_qs(urlparse(self.path).query)["name"][0]
        b = json.dumps({"Status": 0, "Answer": self.answers.get(name, [])}).encode()
        self.send_response(200); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)


def serve(server):
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


class Check(unittest.TestCase):
    """DNS, certificate and login, each against a fake: a DoH answer, and a
    TLS server with its own CA that answers like Caddy in front of Authelia."""

    @classmethod
    def setUpClass(cls):
        cls.la = load(APP, "laibapp")
        cls.tmp = tempfile.mkdtemp()
        t = cls.tmp
        if not shutil.which("openssl"):
            raise unittest.SkipTest("no openssl")
        run = lambda *a: subprocess.run(a, cwd=t, check=True, capture_output=True)
        run("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "ca.key", "-out", "ca.crt", "-days", "2", "-subj", "/O=Test CA/CN=test ca")
        run("openssl", "req", "-newkey", "rsa:2048", "-nodes", "-keyout", "leaf.key", "-out", "leaf.csr", "-subj", "/CN=hallo.boot.test")
        Path(t, "ext").write_text("subjectAltName=DNS:hallo.boot.test\n")
        run("openssl", "x509", "-req", "-in", "leaf.csr", "-CA", "ca.crt", "-CAkey", "ca.key", "-CAcreateserial", "-out", "leaf.crt", "-days", "1", "-extfile", "ext")

    def https(self, status, location=""):
        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass
            def do_GET(self):
                self.send_response(status)
                if location:
                    self.send_header("Location", location)
                self.send_header("Content-Length", "0"); self.end_headers()
        srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain(Path(self.tmp, "leaf.crt"), Path(self.tmp, "leaf.key"))
        srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
        self.addCleanup(srv.shutdown)
        return f"127.0.0.1:{serve(srv).server_address[1]}"

    def doh(self, answers):
        _DoH.answers = answers
        srv = serve(HTTPServer(("127.0.0.1", 0), _DoH))
        self.addCleanup(srv.shutdown)
        self.la.DOH = f"http://127.0.0.1:{srv.server_address[1]}/dns-query"

    HOUSE = {"boot.test": [{"type": 1, "data": "203.0.113.7"}]}
    RECORD = {"hallo.boot.test": [{"type": 5, "data": "boot.test."}, {"type": 1, "data": "203.0.113.7"}]}

    def check(self, *extra):
        import io, contextlib
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = self.la.main(["check", "hallo.boot.test", "--cafile", str(Path(self.tmp, "ca.crt")), *extra])
        return code, out.getvalue()

    def test_done_only_with_record_certificate_and_login(self):
        self.doh({**self.HOUSE, **self.RECORD})
        code, out = self.check("--box", self.https(302, "https://auth.boot.test/?rd=https%3A%2F%2Fhallo.boot.test%2F"))
        self.assertEqual(code, 0, out)
        self.assertIn("route done: https://hallo.boot.test/", out)

    def test_no_record_tells_the_owner_the_strato_entry(self):
        self.doh(self.HOUSE)
        code, out = self.check("--box", self.https(302, "https://auth.boot.test/"))
        self.assertEqual(code, 8, out)
        self.assertIn("Bei Strato unter boot.test die Subdomain „hallo“ anlegen und als CNAME-Ziel „boot.test“ eintragen.", out)

    def test_a_route_without_the_login_is_not_done(self):
        self.doh({**self.HOUSE, **self.RECORD})
        code, out = self.check("--box", self.https(200))
        self.assertEqual(code, 8, out)
        self.assertIn("not behind Authelia", out)
        self.assertEqual(self.check("--box", self.https(200), "--open")[0], 0)

    def test_a_certificate_nobody_trusts_is_not_done(self):
        self.doh({**self.HOUSE, **self.RECORD})
        import io, contextlib
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = self.la.main(["check", "hallo.boot.test", "--box", self.https(302, "https://auth.boot.test/")])   # system CAs only
        self.assertEqual(code, 8, out.getvalue())
        self.assertIn("no valid certificate", out.getvalue())

    def test_what_cannot_be_measured_is_never_done(self):
        self.la.DOH = "http://127.0.0.1:9/dns-query"   # nothing listens
        code, out = self.check("--box", "127.0.0.1:9")
        self.assertEqual(code, 9, out)
        self.assertIn("do not call it done", out)

    def test_a_trial_box_also_needs_the_kraftwerk_proxy_host(self):
        line = self.la.strato_line("hallo.nuc.dopp.cloud", "dopp.cloud")
        self.assertIn("Subdomain „hallo.nuc“", line)
        self.assertIn("NPM", line)


class LintRunsHere(unittest.TestCase):
    def test_laibctl_runs_template_lint_with_the_local_kernel(self):
        with tempfile.TemporaryDirectory() as d:
            fake = Path(d) / "laib"
            fake.write_text(f'#!/bin/sh\nprintf "%s\\n" "$@" > {d}/argv\n')
            fake.chmod(0o755)
            env = dict(os.environ, LAIB_LOCAL_BIN=str(fake), LAIB_MCP_URL="http://127.0.0.1:9/mcp")   # MCP is never asked
            p = subprocess.run([sys.executable, str(HERE / "laibctl.py"), "template", "lint", "/workspace/laib-apps/templates/hallo"],
                               env=env, capture_output=True, text=True, timeout=30)
            self.assertEqual(p.returncode, 0, p.stderr)
            self.assertEqual((Path(d) / "argv").read_text().splitlines(), ["template", "lint", "/workspace/laib-apps/templates/hallo"])


class WorkspaceIsDevs(unittest.TestCase):
    def test_the_entrypoint_hands_the_workspace_to_dev(self):
        # laib creates ${DATA_DIR}/werkstatt/workspace as core, the container's
        # root: on a fresh install dev could not write /workspace at all (the
        # first werkstatt-e2e app run: mkdir /workspace/laib-apps, permission denied)
        with tempfile.TemporaryDirectory() as root:
            r, bin_ = Path(root), Path(root) / "bin"
            bin_.mkdir()
            for name in ("su", "chpasswd", "ssh-keygen", "claude", "git"):
                (bin_ / name).write_text("#!/bin/sh\nexit 0\n"); (bin_ / name).chmod(0o755)
            (bin_ / "chown").write_text(f'#!/bin/sh\necho "$@" >> {root}/chown.log\n'); (bin_ / "chown").chmod(0o755)
            script = (HERE / "entrypoint.sh").read_text().replace("/usr/sbin/sshd", "true")
            for p in ("/home/dev", "/run/laib", "/etc/profile.d", "/etc/ssh", "/workspace"):
                script = script.replace(p, root + p)
            (r / "etc" / "profile.d").mkdir(parents=True); (r / "workspace").mkdir()
            (r / "entrypoint.sh").write_text(script)
            env = {k: v for k, v in os.environ.items() if not k.startswith(("LAIB_", "WERKSTATT_", "CLAUDE_"))}
            env["PATH"] = f"{bin_}:{os.environ['PATH']}"
            env["WERKSTATT_SESSIONS_EVERY"] = "0"   # the sessions' timer (#383) would outlive the test
            p = subprocess.run(["bash", str(r / "entrypoint.sh")], env=env, capture_output=True, text=True, timeout=30)
            self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
            calls = (r / "chown.log").read_text().splitlines()
            self.assertIn(f"dev:dev {root}/workspace", calls, calls)   # the folder itself, never -R over the checkouts


class TheGuide(unittest.TestCase):
    def test_both_agents_read_it(self):
        cf = (HERE / "Containerfile").read_text()
        self.assertIn("COPY guide/AGENTS.md /etc/claude-code/CLAUDE.md", cf)   # Claude Code's managed CLAUDE.md on Linux
        self.assertIn("COPY guide /opt/werkstatt/guide", cf)
        self.assertIn('--append-system-prompt "$GUIDE"', (HERE / "pi").read_text())
        self.assertIn("/opt/werkstatt/guide/AGENTS.md", (HERE / "pi").read_text())

    def test_the_image_carries_what_the_guide_prescribes(self):
        cf = (HERE / "Containerfile").read_text()
        for pkg in ("python3-venv", "python3-pip", "vim-tiny", "nano", "ripgrep", "dnsutils", "openssl", "xz-utils"):
            self.assertIn(pkg, cf)
        self.assertIn("go.dev/dl/go${GO_VERSION}", cf)
        self.assertIn("sha256sum -c", cf)
        self.assertNotIn("continuing without it", cf, "a tool that does not download fails the build")
        self.assertIn("COPY laibapp.py /usr/local/bin/laib-app", cf)
        self.assertIn("COPY scaffold /opt/werkstatt/scaffold", cf)

    def test_every_command_it_names_exists(self):
        verbs = set(re.findall(r'\{Name: "([a-z-]+)", Tier: "(?:read|propose|apply|destroy)"', (ROOT / "cmd/laib/main.go").read_text()))
        self.assertIn("propose", verbs)
        subs = {"new", "register", "check"}
        text = "\n".join(p.read_text() for p in (HERE / "guide").glob("*.md"))
        text += (HERE / "scaffold/repo/AGENTS.md").read_text()
        for verb in re.findall(r"`laib ([a-z-]+)", text):
            self.assertIn(verb, verbs, f"the guide names `laib {verb}`, which is no verb")
        for sub in re.findall(r"laib-app ([a-z]+)", text):
            self.assertIn(sub, subs, f"the guide names `laib-app {sub}`")
        for path in set(re.findall(r"/opt/werkstatt/([a-z/.-]+[a-z])", text)):
            top = path.split("/")[0]
            self.assertIn(top, {"guide", "scaffold", "extensions", "laib"}, path)
            if top == "guide":
                self.assertTrue((HERE / path).exists(), path)


if __name__ == "__main__":
    unittest.main()
