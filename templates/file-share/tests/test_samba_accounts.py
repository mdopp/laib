"""file-share's Samba accounts (#296): samba-members.py reads who may have
one from LLDAP, samba-accounts.sh keeps the passdb in step and takes each
person's password from Haus through a FIFO.

LLDAP is a fake GraphQL server; pdbedit, smbpasswd, adduser, deluser and id
are fakes over a passdb text file in smbpasswd's format, which record their
argv and what reached their stdin. The script runs under bash here (the
image's busybox ash on the box; the VM proves that one, vm-install).

#214: no password on any argv, none in the output."""
import base64, json, os, shutil, subprocess, sys, tempfile, threading, time, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
MEMBERS, ACCOUNTS = HERE / "samba-members.py", HERE / "samba-accounts.sh"
READER_PW = "-r3ader_pw"
LLDAP = {"up": True, "groups": {}}


class FakeLLDAP(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        if not LLDAP["up"]:
            self.send_response(503); self.end_headers(); return
        if self.path == "/auth/simple/login":
            if body != {"username": "file-share", "password": READER_PW}:
                self.send_response(401); self.end_headers(); return
            out = {"token": "jwt"}
        elif self.headers.get("Authorization") != "Bearer jwt":
            self.send_response(401); self.end_headers(); return
        else:
            out = {"data": {"groups": [{"displayName": g, "users": [{"id": u} for u in us]} for g, us in LLDAP["groups"].items()]}}
        data = json.dumps(out).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


class Members(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.srv = HTTPServer(("127.0.0.1", 0), FakeLLDAP)
        threading.Thread(target=cls.srv.serve_forever, daemon=True).start()

    @classmethod
    def tearDownClass(cls):
        cls.srv.shutdown()

    def setUp(self):
        self.dir = tempfile.mkdtemp()
        LLDAP.update(up=True, groups={"family": ["anna", "ben", "Bad Name"], "admins": ["core", "anna"], "lldap_strict_readonly": ["file-share", "radicale"], "gaeste": ["gast"]})

    def tearDown(self):
        shutil.rmtree(self.dir)

    def run_once(self, pw=READER_PW):
        env = dict(os.environ, LLDAP_URL=f"http://127.0.0.1:{self.srv.server_port}", ACCOUNTS_DIR=self.dir, LDAP_PASSWORD=pw)
        return subprocess.run([sys.executable, str(MEMBERS), "--once"], env=env, capture_output=True, text=True, timeout=30)

    def members(self):
        return Path(self.dir, "members").read_text()

    def test_family_and_admins_only_one_name_a_line(self):
        r = self.run_once()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.members(), "anna\nben\ncore\n")
        self.assertIn("Bad Name", r.stdout + r.stderr)   # said, not taken
        self.assertNotIn(READER_PW, r.stdout + r.stderr)

    def test_the_admins_have_their_own_list_for_the_werkstatt_share(self):
        # #666: the Werkstatt share is the admins' only; family alone is not enough
        r = self.run_once()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(Path(self.dir, "admins").read_text(), "anna\ncore\n")

    def test_lldap_down_or_login_refused_leaves_the_list(self):
        Path(self.dir, "members").write_text("anna\n")
        LLDAP["up"] = False
        r = self.run_once()
        self.assertEqual(self.members(), "anna\n")
        self.assertIn("stays", r.stderr)
        LLDAP["up"] = True
        r = self.run_once(pw="wrong")
        self.assertEqual(self.members(), "anna\n")
        self.assertNotIn("wrong", r.stdout + r.stderr)


FAKES = {
    # passdb lines: name:uid:LM:NT:[U          ]:LCT-HEX:. As the real one, pdbedit -L stops
    # at the first entry whose POSIX account is missing ("smbpasswd database is corrupt!")
    "pdbedit": r'''#!/bin/bash
echo "pdbedit $*" >> "$FAKE/argv"
case "$1" in
  -L) while IFS=: read -r n rest; do grep -qx "$n" "$FAKE/posix" || { echo "corrupt: $n" >&2; exit 0; }
        if [ "$2" = -w ]; then echo "$n:$rest"; else echo "$n:${rest%%:*}"; fi; done < "$FAKE/passdb" ;;
  -x) grep -q "^$3:" "$FAKE/passdb" || exit 1; grep -v "^$3:" "$FAKE/passdb" > "$FAKE/p.tmp"; mv "$FAKE/p.tmp" "$FAKE/passdb" ;;
esac''',
    "smbpasswd": r'''#!/bin/bash
echo "smbpasswd $*" >> "$FAKE/argv"
[ "$1" = -e ] && exit 0
u="${@: -1}"
grep -qx "$u" "$FAKE/posix" || exit 1
IFS= read -r a; IFS= read -r b
[ "$a" = "$b" ] || exit 1
printf '%s %s\n' "$u" "$a" >> "$FAKE/stdin"
grep -v "^$u:" "$FAKE/passdb" > "$FAKE/p.tmp"; mv "$FAKE/p.tmp" "$FAKE/passdb"
printf '%s:1000:XX:NT:[U          ]:LCT-%X:\n' "$u" "$(date +%s)" >> "$FAKE/passdb"''',
    "adduser": '#!/bin/bash\necho "adduser $*" >> "$FAKE/argv"; echo "${@: -1}" >> "$FAKE/posix"',
    "deluser": '#!/bin/bash\necho "deluser $*" >> "$FAKE/argv"; grep -vx "$1" "$FAKE/posix" > "$FAKE/x.tmp"; mv "$FAKE/x.tmp" "$FAKE/posix"',
    "id": '#!/bin/bash\ngrep -qx "${@: -1}" "$FAKE/posix"',
    # busybox: addgroup GROUP makes it, addgroup USER GROUP adds to it, delgroup USER GROUP takes out
    "addgroup": '#!/bin/bash\necho "addgroup $*" >> "$FAKE/argv"; [ $# = 2 ] || exit 0; grep -qx "$1" "$FAKE/admins" && exit 1; echo "$1" >> "$FAKE/admins"',
    "delgroup": '#!/bin/bash\necho "delgroup $*" >> "$FAKE/argv"; grep -qx "$1" "$FAKE/admins" || exit 1; grep -vx "$1" "$FAKE/admins" > "$FAKE/g.tmp"; mv "$FAKE/g.tmp" "$FAKE/admins"',
}


class Accounts(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.fake, self.dir, self.bin = self.root / "fake", self.root / "accounts", self.root / "bin"
        for d in (self.fake, self.dir, self.bin):
            d.mkdir()
        for n, body in FAKES.items():
            (self.bin / n).write_text(body)
            (self.bin / n).chmod(0o755)
        # ServiceBay's passdb on a migrated box: anna and olda, and the shared account
        (self.fake / "passdb").write_text("samba:1000:XX:NT:[U          ]:LCT-6A000000:\nanna:1001:XX:NT:[U          ]:LCT-6A000001:\nolda:1002:XX:NT:[U          ]:LCT-6A000002:\n")
        (self.fake / "posix").write_text("samba\n")      # the entrypoint made the shared one; a restart lost the rest
        for f in ("argv", "stdin", "admins"):
            (self.fake / f).write_text("")
        self.proc = None

    def tearDown(self):
        if self.proc:
            self.proc.kill()
            self.proc.wait()
        shutil.rmtree(self.root)

    def start(self, members="anna\nben\n", admins=None):
        if members is not None:
            (self.dir / "members").write_text(members)
        if admins is not None:
            (self.dir / "admins").write_text(admins)
        env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}", FAKE=str(self.fake), ACCOUNTS_DIR=str(self.dir), SHARE_USER="samba", TICK="1",
                   PASSDB=str(self.fake / "passdb"))
        self.out = open(self.root / "out", "w+")
        self.proc = subprocess.Popen(["bash", str(ACCOUNTS)], env=env, stdout=self.out, stderr=subprocess.STDOUT)
        self.wait(lambda: "in step" in self.output())

    def output(self):
        return (self.root / "out").read_text()

    def wait(self, ok, what="the script"):
        for _ in range(100):
            if ok():
                return
            time.sleep(0.1)
        self.fail(f"{what} did not get there; output:\n{self.output()}")

    def status(self):
        p = self.dir / "status"
        return dict(l.split() for l in p.read_text().splitlines()) if p.exists() else {}

    def send(self, line):
        fd = os.open(self.dir / "passwords", os.O_WRONLY | os.O_NONBLOCK)
        os.write(fd, line.encode() + b"\n")
        os.close(fd)

    def test_start_keeps_members_and_the_shared_account_and_removes_the_rest(self):
        self.start()
        passdb = (self.fake / "passdb").read_text()
        self.assertIn("anna:", passdb)
        self.assertIn("samba:", passdb)          # the fallback stays
        self.assertNotIn("olda:", passdb)        # left LLDAP's family: gone
        self.assertIn("anna", (self.fake / "posix").read_text().split())   # its login works again after the restart
        self.assertEqual(self.status(), {"anna": str(0x6A000001), "ben": "0"})   # ben has none until he sets one
        self.assertTrue(stat_fifo(self.dir / "passwords"))

    def test_no_member_list_removes_nobody(self):
        self.start(members=None)
        self.assertIn("olda:", (self.fake / "passdb").read_text())

    def test_a_password_reaches_smbpasswd_on_stdin_only(self):
        self.start()
        pw = "Geheim äö 1 -x"
        self.send("set ben " + base64.b64encode(pw.encode()).decode())
        self.wait(lambda: self.status().get("ben", "0") != "0", "ben's password")
        self.assertEqual((self.fake / "stdin").read_text(), f"ben {pw}\n")
        for where in ((self.fake / "argv").read_text(), self.output()):
            self.assertNotIn(pw, where)
            self.assertNotIn(base64.b64encode(pw.encode()).decode(), where)
        self.assertIn("password set: ben", self.output())

    def test_only_members_get_one_and_clear_removes_it(self):
        self.start()
        self.send("set olda " + base64.b64encode(b"x").decode())
        self.send("set ../etc " + base64.b64encode(b"x").decode())
        self.send("clear anna")
        self.wait(lambda: self.status().get("anna") == "0", "anna's reset")
        self.assertEqual((self.fake / "stdin").read_text(), "")
        self.assertIn("refused: olda", self.output())

    def test_a_name_that_leaves_the_list_loses_its_account(self):
        self.start()
        (self.dir / "members").write_text("ben\n")
        self.wait(lambda: "anna:" not in (self.fake / "passdb").read_text(), "anna's removal")
        self.assertIn("samba:", (self.fake / "passdb").read_text())

    def group(self):
        return (self.fake / "admins").read_text().split()

    def test_only_admins_with_an_account_are_in_the_werkstatt_group(self):
        # #666: valid users = @admins on the werkstatt share; the shared account never, ben is family only
        self.start(admins="anna\nsamba\n")
        self.wait(lambda: "werkstatt share" in self.output(), "the group")
        self.assertEqual(self.group(), ["anna"])
        self.assertIn("werkstatt share for: anna", self.output())

    def test_no_admin_list_lets_nobody_in(self):
        self.start()
        self.wait(lambda: "werkstatt share" in self.output(), "the group")
        self.assertEqual(self.group(), [])

    def test_an_admin_who_leaves_the_list_loses_the_share(self):
        self.start(admins="anna\n")
        self.wait(lambda: self.group() == ["anna"], "anna in the group")
        (self.dir / "admins").write_text("")
        self.wait(lambda: self.group() == [], "anna out of the group")
        self.assertIn("anna:", (self.fake / "passdb").read_text())   # family still: her account stays

    def test_a_new_admin_account_gets_the_share_with_its_password(self):
        self.start(admins="anna\nben\n")
        self.send("set ben " + base64.b64encode(b"pw-ben").decode())
        self.wait(lambda: "ben" in self.group(), "ben in the group")


def stat_fifo(p):
    import stat
    return stat.S_ISFIFO(os.stat(p).st_mode) and os.stat(p).st_mode & 0o777 == 0o600


if __name__ == "__main__":
    unittest.main()
