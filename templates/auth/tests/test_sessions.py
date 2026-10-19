"""#369: Authelia kept its sessions in memory, so every restart of the auth
pod (an apply, an image update, a reboot) logged the whole household out of
every forward-auth app. The sessions live in a Valkey in the auth pod, its
data on disk, and Authelia's config points at it. ServiceBay had the same
in-memory sessions; the VM run (vm-install, logins) proves a login survives
a restart of the pod."""
import os, re, subprocess, sys, tempfile, unittest
from pathlib import Path

T = Path(__file__).resolve().parent.parent


class Sessions(unittest.TestCase):
    def test_authelia_keeps_sessions_in_the_pods_valkey(self):
        with tempfile.TemporaryDirectory() as d:
            (Path(d) / "auth" / "authelia-config").mkdir(parents=True)
            (Path(d) / "auth" / "authelia-config" / "oidc.pem").write_text("-----BEGIN RSA PRIVATE KEY-----\nx\n-----END RSA PRIVATE KEY-----\n")
            env = dict(os.environ, LAIB_DATA_DIR=d, LAIB_DOMAIN="vm.test", LAIB_RENDER_DIR=str(Path(d) / "render"),
                       LAIB_START_URL="https://laib.vm.test/", BASE_DN="dc=vm,dc=test",
                       **{"LAIB_SECRET_" + n: "x" for n in ("AUTHELIA_JWT_SECRET", "AUTHELIA_SESSION_SECRET", "AUTHELIA_STORAGE_KEY",
                                                           "AUTHELIA_OIDC_HMAC_SECRET", "LLDAP_ADMIN_PASSWORD")})
            p = subprocess.run([sys.executable, str(T / "pre-start")], env=env, capture_output=True, text=True, timeout=60)
            self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
            conf = (Path(d) / "auth" / "authelia-config" / "configuration.yml").read_text()
        session = conf.split("\nsession:\n", 1)[1].split("\nstorage:", 1)[0]
        self.assertRegex(session, r"(?m)^  redis:\n    host: '127\.0\.0\.1'\n    port: 6379$", "Authelia's sessions are not in the pod's Valkey")

    def test_the_valkey_writes_to_disk_inside_the_pod(self):
        unit = (T / "auth-sessions.container").read_text()
        self.assertIn("Pod=auth.pod", unit)
        self.assertRegex(unit, r"(?m)^Volume=\$\{DATA_DIR\}/auth/sessions:/data:z$", "the sessions are not on disk")
        exec_line = re.search(r"(?m)^Exec=(.*)$", unit).group(1)
        for flag in ("--bind 127.0.0.1", "--appendonly yes", "--dir /data"):
            self.assertIn(flag, exec_line)
        self.assertNotIn("6379", (T / "auth.pod").read_text(), "the session store must stay inside the pod")
        self.assertIn("auth-sessions.service", re.search(r"(?m)^After=(.*)$", (T / "auth-authelia.container").read_text()).group(1))
        contract = (T / "laib.yml").read_text()
        self.assertIn("path: ${DATA_DIR}/auth/sessions", contract)
        self.assertRegex((T / "values.example.yml").read_text(), r"(?m)^SESSIONS_IMAGE: \S+$")


if __name__ == "__main__":
    unittest.main()
