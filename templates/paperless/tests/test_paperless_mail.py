"""#521: Paperless' mail inbox from values. paperless-mail.py setup makes or
takes over the IMAP account and the rule through Paperless' API and is
idempotent; check writes laib's status from Paperless' own connection test.
Against a fake Paperless API, never a real one."""
import base64, json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "paperless-mail.py"
AUTH = "Basic " + base64.b64encode(b"admin:adminpw").decode()
# what a Paperless superuser's /api/ui_settings/ lists (codenames, app label stripped): Paperless'
# own models and the framework's, which no household group needs
MODELS = ["document", "tag", "correspondent", "documenttype", "storagepath", "savedview", "paperlesstask",
          "applicationconfiguration", "uisettings", "note", "mailaccount", "mailrule", "processedmail", "user", "group",
          "sharelink", "sharelinkbundle", "customfield", "workflow", "logentry"]
PERMS = ["%s_%s" % (a, m) for m in MODELS for a in ("view", "add", "change", "delete")] + [
    "view_global_statistics", "view_system_monitoring", "view_session", "add_session", "view_contenttype",
    "view_groupobjectpermission", "view_socialaccount", "add_token"]


class Fake:
    """Paperless' mail endpoints: accounts and rules in memory, the test answers ok/refused"""

    def __init__(self, accounts=None, rules=None, test_ok=True, tasks=None, groups=None, perms=None, config=None, fields=None,
                 users=None, workflows=None, documents=None):
        self.accounts, self.rules, self.test_ok, self.calls, self.tested = accounts or [], rules or [], test_ok, [], []
        self.config, self.fields = config if config is not None else [{"id": 1}], fields if fields is not None else []
        self.tasks, self.queries, self.processed = tasks or [], [], []
        self.groups, self.perms = groups if groups is not None else [], PERMS if perms is None else perms
        # ADR 0028: Paperless' users with their passwords, its tokens (one per user, made once), workflows, documents
        self.users, self.workflows, self.documents = users if users is not None else [], workflows or [], documents or []
        self.passwords, self.tokens, self.bulk = {}, {}, []
        fake = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def send(self, code, body):
                b = json.dumps(body).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(b)))
                self.end_headers()
                self.wfile.write(b)

            def handle_any(self, method):
                if self.headers.get("Authorization") != AUTH:
                    return self.send(401, {"detail": "no"})
                n = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(n)) if n else None
                path, _, query = self.path.partition("?")
                fake.calls.append((method, path, body))
                fake.queries.append((path, query, self.headers.get("Accept", "")))
                coll = {"/api/mail_accounts/": fake.accounts, "/api/mail_rules/": fake.rules, "/api/groups/": fake.groups,
                        "/api/custom_fields/": fake.fields, "/api/config/": fake.config, "/api/users/": fake.users,
                        "/api/workflows/": fake.workflows}
                if path == "/api/users/" and method == "POST" or path.startswith("/api/users/") and method == "PATCH":
                    if "password" in body:   # Paperless keeps it hashed and answers with stars
                        fake.passwords[body.get("username") or next(u["username"] for u in fake.users if u["id"] == int(path.split("/")[3]))] = body.pop("password")
                    unknown = [c for c in body.get("user_permissions", []) if c not in fake.perms]
                    if unknown:
                        return self.send(400, {"user_permissions": ["Object with codename=%s does not exist." % unknown[0]]})
                if path == "/api/token/" and method == "POST":
                    if fake.passwords.get(body["username"]) != body["password"]:
                        return self.send(400, {"non_field_errors": ["Unable to log in with provided credentials."]})
                    return self.send(200, {"token": fake.tokens.setdefault(body["username"], "tok-%s-%d" % (body["username"], len(fake.tokens) + 1))})
                if path == "/api/documents/" and method == "GET":
                    owned = [d for d in fake.documents if d.get("owner") is not None] if "owner__isnull=false" in query else fake.documents
                    if "full_perms=true" not in query:
                        owned = [{k: v for k, v in d.items() if k != "permissions"} for d in owned]
                    return self.send(200, {"count": len(owned), "next": None, "results": owned})
                if path == "/api/documents/bulk_edit/" and method == "POST":
                    fake.bulk.append(body)
                    p = body["parameters"]["set_permissions"]
                    for d in fake.documents:
                        if d["id"] in body["documents"]:
                            for act in ("view", "change"):
                                have = d.setdefault("permissions", {}).setdefault(act, {"users": [], "groups": []})
                                have["users"] = sorted(set(have["users"]) | set(p[act]["users"])) if body["parameters"]["merge"] else p[act]["users"]
                    return self.send(200, {"result": "OK"})
                for base, items in coll.items():
                    if path.startswith(base) and path != base and method == "DELETE":
                        items[:] = [o for o in items if o["id"] != int(path[len(base):].strip("/"))]
                        self.send_response(204)
                        self.end_headers()
                        return
                if path == "/api/config/" and method == "GET":   # Paperless' settings: one row, a plain list
                    return self.send(200, fake.config)
                if path == "/api/ui_settings/" and method == "GET":
                    return self.send(200, {"user": {"id": 1, "username": "admin", "is_superuser": True}, "settings": {}, "permissions": fake.perms})
                if path == "/api/groups/" and method in ("POST", "PATCH") or path.startswith("/api/groups/") and method == "PATCH":
                    unknown = [c for c in body.get("permissions", []) if c not in fake.perms]
                    if unknown:   # Paperless' SlugRelatedField: a codename it has not is a 400
                        return self.send(400, {"permissions": ["Object with codename=%s does not exist." % unknown[0]]})
                if path == "/api/mail_accounts/test/":
                    fake.tested.append(body)
                    return self.send(200, {"success": True}) if fake.test_ok else self.send(400, "Unable to connect to server")
                if path == "/api/tasks/" and method == "GET":
                    # API version 9 (Paperless 2.x, and 3.x for old clients): a plain list, uppercase states
                    if "version=9" not in self.headers.get("Accept", ""):
                        return self.send(406, {"detail": "the probe must pin version 9"})
                    ack = "acknowledged=false" in query
                    return self.send(200, [t for t in fake.tasks if not (ack and t["acknowledged"])])
                if path.startswith("/api/mail_accounts/") and path.endswith("/process/") and method == "POST":
                    fake.processed.append(int(path.split("/")[3]))
                    return self.send(200, {"result": "OK"})
                if path in coll and method == "GET":
                    return self.send(200, {"count": len(coll[path]), "next": None, "results": coll[path]})
                if path in coll and method == "POST":
                    obj = {**body, "id": len(coll[path]) + 1}
                    if path == "/api/mail_accounts/":
                        obj["password"] = "**********"
                    coll[path].append(obj)
                    return self.send(201, obj)
                for base, items in coll.items():
                    if path.startswith(base) and method == "PATCH":
                        obj = next(o for o in items if o["id"] == int(path[len(base):].strip("/")))
                        obj.update(body)
                        if base == "/api/mail_accounts/":
                            obj["password"] = "**********"
                        return self.send(200, obj)
                self.send(404, {})

            def do_GET(self):
                self.handle_any("GET")

            def do_POST(self):
                self.handle_any("POST")

            def do_PATCH(self):
                self.handle_any("PATCH")

            def do_DELETE(self):
                self.handle_any("DELETE")

        self.srv = HTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = "http://127.0.0.1:%d" % self.srv.server_port

    def writes(self):
        return [c for c in self.calls if c[0] != "GET" and c[1] != "/api/mail_accounts/test/" and not c[1].endswith("/process/")]

    def mail_calls(self):
        return [c for c in self.calls if c[1].startswith("/api/mail_")]

    def group(self, name):
        return next(g for g in self.groups if g["name"] == name)


MAIL = {"PAPERLESS_MAIL_HOST": "imap.strato.de", "PAPERLESS_MAIL_PORT": "993", "PAPERLESS_MAIL_SECURITY": "ssl",
        "PAPERLESS_MAIL_USER": "webmaster@example.org", "PAPERLESS_MAIL_FILTER_TO": "dokumente@example.org", "PAPERLESS_MAIL_FOLDER": "INBOX"}


class PaperlessMail(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        os.makedirs(os.path.join(self.tmp, "paperless", "data"))

    def run_setup(self, fake, values, password="mailpw"):
        env = {"PATH": os.environ["PATH"], "PAPERLESS_API": fake.url, "ADMIN_USER": "admin", "LAIB_DATA_DIR": self.tmp,
               "LAIB_SECRET_PAPERLESS_ADMIN_PASSWORD": "adminpw", "LAIB_SECRET_PAPERLESS_MAIL_PASSWORD": password, **values}
        return subprocess.run([sys.executable, str(SCRIPT), "setup"], env=env, capture_output=True, text=True, timeout=60)

    def status(self):
        with open(os.path.join(self.tmp, "paperless", "data", "laib-status.json")) as f:
            return json.load(f)

    def test_makes_the_account_and_the_rule_then_changes_nothing(self):
        fake = Fake()
        r = self.run_setup(fake, MAIL)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(len(fake.accounts), 1)
        acc = fake.accounts[0]
        self.assertEqual((acc["imap_server"], acc["imap_port"], acc["imap_security"], acc["username"]), ("imap.strato.de", 993, 2, "webmaster@example.org"))
        posted = next(c[2] for c in fake.calls if c[:2] == ("POST", "/api/mail_accounts/"))
        self.assertEqual(posted["password"], "mailpw")
        rule = fake.rules[0]
        # the owner's decision (#521): only mail to dokumente@, attachments only, flag after processing
        self.assertEqual((rule["account"], rule["filter_to"], rule["attachment_type"], rule["consumption_scope"], rule["action"]), (1, "dokumente@example.org", 1, 1, 4))
        self.assertEqual((rule["assign_title_from"], rule["assign_owner_from_rule"], rule["folder"]), (1, False, "INBOX"))
        self.assertNotIn("mailpw", r.stdout + r.stderr)
        self.assertEqual(self.status()["findings"], [])
        self.assertIn("dokumente@example.org", self.status()["detail"])
        # again: the same account and rule, the rule untouched, the password set again (it cannot be compared)
        fake.calls.clear()
        r = self.run_setup(fake, MAIL)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual((len(fake.accounts), len(fake.rules)), (1, 1))
        self.assertEqual([c[:2] for c in fake.writes()], [("PATCH", "/api/mail_accounts/1/")])
        self.assertIn("Regel unverändert", r.stdout)

    def test_takes_over_the_hand_made_ones(self):
        # paperless-mail.sh's account and rule (2026-10-21): other names, the rule marking read
        fake = Fake(accounts=[{"id": 7, "name": "Strato", "imap_server": "IMAP.strato.de", "imap_port": 993, "imap_security": 2,
                               "username": "Webmaster@example.org", "password": "**********", "character_set": "UTF-8", "account_type": 1, "is_token": False}],
                    rules=[{"id": 3, "name": "dokumente", "account": 7, "filter_to": "dokumente@example.org", "action": 3, "enabled": True}])
        r = self.run_setup(fake, MAIL)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual((len(fake.accounts), len(fake.rules)), (1, 1))
        self.assertEqual(fake.rules[0]["action"], 4)
        self.assertEqual(fake.accounts[0]["imap_server"], "imap.strato.de")

    def test_without_a_host_nothing_changes(self):
        fake = Fake(accounts=[{"id": 1, "imap_server": "x", "username": "y"}])
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": ""}, password="")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual([c for c in fake.mail_calls() if c[0] != "GET"], [])
        self.assertEqual(self.status(), {**self.status(), "detail": "", "findings": []})

    def test_without_a_host_the_mail_api_is_not_asked(self):
        # no inbox: nothing of Paperless' mail is read or written; the groups are made all the same
        fake = Fake()
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": ""}, password="")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(fake.mail_calls(), [])
        self.assertEqual(sorted(g["name"] for g in fake.groups), ["admins", "family"])
        # a box moved from ServiceBay keeps ServiceBay's admin: a refused one is a finding, not a
        # red apply (the rehearsal's paperless step, train 28): Paperless runs, the groups wait
        env = {"PATH": os.environ["PATH"], "PAPERLESS_API": fake.url, "ADMIN_USER": "admin", "LAIB_DATA_DIR": self.tmp,
               "LAIB_SECRET_PAPERLESS_ADMIN_PASSWORD": "wrong"}
        r = subprocess.run([sys.executable, str(SCRIPT), "setup"], env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual([f["severity"] for f in self.status()["findings"]], ["rot"])
        self.assertIn("lehnt den Admin admin ab (401)", self.status()["findings"][0]["detail"])

    def test_setup_makes_the_household_groups_with_their_permissions(self):
        # 2026-10-22: mdopp logged in through Authelia, Paperless synced his groups admins and family,
        # which had no permissions: "Error loading settings", Forbidden /api/ui_settings/
        fake = Fake()
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": ""}, password="")
        self.assertEqual(r.returncode, 0, r.stderr)
        admins, family = set(fake.group("admins")["permissions"]), set(fake.group("family")["permissions"])
        # admins: everything of Paperless' own, users, groups, mail and workflows included
        own = set(PERMS) - {"view_session", "add_session", "view_contenttype", "view_groupobjectpermission", "view_socialaccount", "add_token"}
        self.assertEqual(admins, own)
        # family: the documents and what files them, their own settings and tasks; not the inbox,
        # the workflows, the custom fields' definitions, the users or the groups
        for c in ["view_document", "add_document", "change_document", "delete_document", "view_tag", "add_tag",
                  "view_correspondent", "change_documenttype", "view_storagepath", "add_savedview", "view_note", "add_note",
                  "view_uisettings", "add_uisettings", "change_uisettings", "view_paperlesstask", "change_paperlesstask",
                  "view_customfield", "add_sharelink", "view_sharelink"]:
            self.assertIn(c, family)
        for c in ["view_mailaccount", "change_mailrule", "add_workflow", "add_customfield", "delete_customfield", "view_user",
                  "change_group", "view_applicationconfiguration", "view_logentry", "view_system_monitoring", "view_session"]:
            self.assertNotIn(c, family)
        self.assertIn("Gruppe family angelegt", r.stdout)
        # again: nothing written
        fake.calls.clear()
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": ""}, password="")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(fake.writes(), [])

    def test_takes_over_hand_made_groups_and_keeps_what_the_owner_added(self):
        fake = Fake(groups=[{"id": 4, "name": "family", "permissions": ["view_document", "view_mailrule"]}])
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": ""}, password="")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(len([g for g in fake.groups if g["name"] == "family"]), 1)
        family = fake.group("family")["permissions"]
        self.assertIn("view_mailrule", family)       # the owner's addition stays
        self.assertIn("change_uisettings", family)

    def test_a_paperless_without_a_permission_does_not_fail_the_apply(self):
        # an older or newer Paperless without, say, share link bundles: given what it has
        fake = Fake(perms=[c for c in PERMS if "sharelink" not in c and "uisettings" not in c])
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": ""}, password="")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("view_document", fake.group("family")["permissions"])

    def test_a_refused_admin_is_a_finding_not_a_red_apply(self):
        fake = Fake()
        env = {"PATH": os.environ["PATH"], "PAPERLESS_API": fake.url, "ADMIN_USER": "admin", "LAIB_DATA_DIR": self.tmp,
               "LAIB_SECRET_PAPERLESS_ADMIN_PASSWORD": "wrong", "LAIB_SECRET_PAPERLESS_MAIL_PASSWORD": "mailpw", **MAIL}
        r = subprocess.run([sys.executable, str(SCRIPT), "setup"], env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(r.returncode, 0, r.stderr)   # Paperless runs; only the inbox waits
        st = self.status()
        self.assertEqual(st["findings"][0]["severity"], "rot")
        self.assertIn("lehnt den Admin admin ab (401)", st["findings"][0]["detail"])
        self.assertIn("laib secret set PAPERLESS_ADMIN_PASSWORD", st["findings"][0]["fix"])
        self.assertNotIn("wrong", r.stdout + r.stderr + json.dumps(st))
        # the check inside the container says the same, not "antwortet nicht"
        out = os.path.join(self.tmp, "st.json")
        env = {"PATH": os.environ["PATH"], "PAPERLESS_API": fake.url, "PAPERLESS_ADMIN_USER": "admin", "PAPERLESS_ADMIN_PASSWORD": "wrong",
               "LAIB_STATUS_FILE": out, **MAIL}
        r = subprocess.run([sys.executable, "-", "check"], input=SCRIPT.read_text(), env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(r.returncode, 0, r.stderr)
        with open(out) as f:
            self.assertIn("lehnt den Admin admin ab", json.load(f)["findings"][0]["detail"])

    def test_a_host_without_a_password_is_refused(self):
        r = self.run_setup(Fake(), MAIL, password="")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("PAPERLESS_MAIL_PASSWORD", r.stderr)

    def test_a_refused_login_is_a_red_finding(self):
        fake = Fake(test_ok=False)
        r = self.run_setup(fake, MAIL)
        self.assertEqual(r.returncode, 0, r.stderr)   # the account is written; the mailbox's trouble is diag's, not apply's
        st = self.status()
        self.assertEqual(st["findings"][0]["severity"], "rot")
        self.assertIn("webmaster@example.org auf imap.strato.de", st["findings"][0]["detail"])
        self.assertIn("laib secret set PAPERLESS_MAIL_PASSWORD", st["findings"][0]["fix"])
        # Paperless tests with its stored password: the id and the mask, never the secret
        self.assertEqual((fake.tested[-1]["id"], fake.tested[-1]["password"]), (1, "**********"))

    def test_check_inside_the_container(self):
        # the account exists, the rule is gone: red, and what to do
        fake = Fake(accounts=[{"id": 1, "imap_server": "imap.strato.de", "username": "webmaster@example.org", "imap_port": 993}])
        out = os.path.join(self.tmp, "st.json")
        env = {"PATH": os.environ["PATH"], "PAPERLESS_API": fake.url, "PAPERLESS_ADMIN_USER": "admin", "PAPERLESS_ADMIN_PASSWORD": "adminpw",
               "LAIB_STATUS_FILE": out, **MAIL}
        r = subprocess.run([sys.executable, "-", "check"], input=SCRIPT.read_text(), env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(r.returncode, 0, r.stderr)
        with open(out) as f:
            st = json.load(f)
        self.assertEqual(st["findings"][0]["severity"], "rot")
        self.assertIn("die Regel", st["findings"][0]["detail"])
        self.assertEqual(fake.writes(), [])   # check only looks

    def run_check(self, fake, values):
        out = os.path.join(self.tmp, "st.json")
        env = {"PATH": os.environ["PATH"], "PAPERLESS_API": fake.url, "PAPERLESS_ADMIN_USER": "admin", "PAPERLESS_ADMIN_PASSWORD": "adminpw",
               "LAIB_STATUS_FILE": out, **values}
        r = subprocess.run([sys.executable, "-", "check"], input=SCRIPT.read_text(), env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(r.returncode, 0, r.stderr)
        with open(out) as f:
            return json.load(f)

    def test_a_failed_consume_is_a_yellow_finding(self):
        # 2026-10-22: a mail was fetched, its attachment not filed (PermissionError in the scratch
        # dir); the inbox check stayed green. A failed consume Paperless still lists is yellow.
        accounts = [{"id": 1, "name": "x", "imap_server": "imap.strato.de", "username": "webmaster@example.org", "imap_port": 993,
                     "imap_security": 2, "character_set": "UTF-8", "account_type": 1, "is_token": False}]
        rules = [{"id": 1, "account": 1, "filter_to": "dokumente@example.org", "enabled": True, "name": "r"}]
        failed = {"id": 9, "task_name": "consume_file", "task_file_name": "Rechnung_1117583.pdf", "status": "FAILURE",
                  "result": "[Errno 13] Permission denied: '/tmp/paperless/paperless-mail-43qxltum/Rechnung_1117583.pdf'",
                  "acknowledged": False, "date_created": "2026-10-22T14:50:30Z"}
        fine = [{**failed, "id": 10, "status": "SUCCESS", "task_file_name": "ok.pdf"},
                {**failed, "id": 11, "acknowledged": True, "task_file_name": "dismissed.pdf"},
                {**failed, "id": 12, "task_name": "train_classifier", "task_file_name": None}]
        st = self.run_check(Fake(accounts=accounts, rules=rules, tasks=[failed] + fine), MAIL)
        self.assertEqual([f["severity"] for f in st["findings"]], ["gelb"])
        self.assertIn("Rechnung_1117583.pdf", st["findings"][0]["detail"])
        self.assertIn("Permission denied", st["findings"][0]["detail"])
        self.assertNotIn("ok.pdf", json.dumps(st))
        self.assertNotIn("dismissed.pdf", json.dumps(st))
        # and the probe can be green: the same box without the failure
        self.assertEqual(self.run_check(Fake(accounts=accounts, rules=rules, tasks=fine), MAIL)["findings"], [])
        # a failed consume from the consume folder counts too, also without a mail inbox
        st = self.run_check(Fake(tasks=[failed]), {"PAPERLESS_MAIL_HOST": ""})
        self.assertEqual([f["severity"] for f in st["findings"]], ["gelb"])

    def test_setup_fetches_once_through_paperless_worker(self):
        # a fetch run by hand as root (python3 manage.py mail_fetcher under podman exec) writes the
        # attachment into a 0700 root dir the worker (uid 1000) cannot read: setup asks Paperless'
        # API to fetch now, so the worker fetches and nobody has a reason to
        fake = Fake()
        r = self.run_setup(fake, MAIL)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(fake.processed, [1])

    def test_laibs_exec_never_runs_as_root(self):
        unit = (SCRIPT.parent / "paperless-check.service").read_text()
        execs = [l for l in unit.splitlines() if l.startswith("ExecStart=") and "podman exec" in l]
        self.assertTrue(execs)
        for l in execs:
            self.assertIn(" --user paperless ", l)


class Dokumente(unittest.TestCase):
    """ADR 0028: Paperless' side of Dokumente, made by setup on every apply. The narrowest user (no
    administrator, exactly its rights), its token handed back as PAPERLESS_TOKEN only when it differs,
    the workflow „Dokument hinzugefügt“ with the webhook to 8720 and the box's token, and view and
    change on the documents a person owns. A second run writes nothing."""
    setUp, run_setup = PaperlessMail.setUp, PaperlessMail.run_setup
    KEYS = {"LAIB_SECRET_PAPERLESS_DOKUMENTE_PASSWORD": "dok-pw", "LAIB_SECRET_DOKUMENTE_WEBHOOK_TOKEN": "hook-tok"}

    def setup(self, fake, **extra):
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": "", **self.KEYS, **extra}, password="")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        return r

    def test_makes_the_narrowest_user_its_token_and_the_workflow_then_nothing(self):
        fake = Fake(documents=[{"id": 1, "owner": None}, {"id": 2, "owner": 3, "permissions": {"view": {"users": [3], "groups": []},
                                                                                                "change": {"users": [3], "groups": []}}}])
        r = self.setup(fake)
        [u] = fake.users
        self.assertEqual((u["username"], u["is_superuser"], u["is_staff"], u["groups"]), ("dokumente", False, False, []))
        self.assertEqual(set(u["user_permissions"]), {"view_document", "change_document", "view_note", "add_note", "view_tag", "add_tag",
                                                      "view_correspondent", "view_documenttype", "view_customfield", "view_user"})
        for c in u["user_permissions"]:
            self.assertFalse(c.startswith(("delete_", "add_document")) or "mail" in c or "workflow" in c or "group" in c or c == "change_user", c)
        self.assertEqual(fake.passwords["dokumente"], "dok-pw")
        self.assertIn("laib-secret: PAPERLESS_TOKEN=" + fake.tokens["dokumente"], r.stdout.splitlines())
        [w] = fake.workflows
        self.assertEqual((w["name"], w["enabled"], [t["type"] for t in w["triggers"]]), ("Dokument hinzugefügt", True, [2]))
        assign, hook = w["actions"]
        self.assertEqual((assign["type"], assign["assign_view_users"], assign["assign_change_users"]), (1, [u["id"]], [u["id"]]))
        self.assertEqual((hook["type"], hook["webhook"]["url"], hook["webhook"]["headers"]),
                         (4, "http://host.containers.internal:8720/hook", {"X-Dokumente-Token": "hook-tok"}))
        # the document a person owns: Dokumente may see and change it, the owner keeps theirs
        self.assertEqual(sorted(fake.documents[1]["permissions"]["change"]["users"]), sorted([3, u["id"]]))
        self.assertEqual([b["documents"] for b in fake.bulk], [[2]])
        # the next apply, with the token handed back: nothing written, nothing handed back
        fake.calls.clear()
        r = self.setup(fake, LAIB_SECRET_PAPERLESS_TOKEN=fake.tokens["dokumente"])
        self.assertEqual([c for c in fake.writes() if c[1] != "/api/token/"], [])
        self.assertNotIn("laib-secret:", r.stdout)

    def test_a_widened_user_is_narrowed_and_a_foreign_password_set_back(self):
        fake = Fake(users=[{"id": 5, "username": "dokumente", "is_superuser": True, "is_staff": True, "is_active": True, "groups": [1],
                            "user_permissions": ["delete_document"]}])
        fake.passwords["dokumente"] = "by-hand"
        r = self.setup(fake)
        u = fake.users[0]
        self.assertEqual((u["is_superuser"], u["is_staff"], u["groups"]), (False, False, []))
        self.assertNotIn("delete_document", u["user_permissions"])
        self.assertEqual(fake.passwords["dokumente"], "dok-pw")
        self.assertIn("laib-secret: PAPERLESS_TOKEN=", r.stdout)

    def test_a_workflow_that_differs_is_made_again(self):
        fake = Fake()
        self.setup(fake)
        fake.workflows[0]["actions"][1]["webhook"]["headers"] = {"X-Dokumente-Token": "old"}
        self.setup(fake, LAIB_SECRET_DOKUMENTE_WEBHOOK_TOKEN="hook-new")
        [w] = fake.workflows
        self.assertEqual(w["actions"][1]["webhook"]["headers"], {"X-Dokumente-Token": "hook-new"})

    def test_without_the_hook_token_no_workflow(self):
        fake = Fake()
        r = self.setup(fake, LAIB_SECRET_DOKUMENTE_WEBHOOK_TOKEN="")
        self.assertEqual(fake.workflows, [])
        self.assertIn("erst zur vollen Stunde", r.stdout)
        self.assertIn("laib-secret: PAPERLESS_TOKEN=", r.stdout, "the hourly run still gets its token")


class PaperlessAI(unittest.TestCase):
    """#567: Paperless' AI and OCR are values. Paperless takes a field of its own settings over the
    environment, so setup clears the fields the template sets (the owner switched the AI on by hand
    there, 2026-10-22); the contract fields exist for the family to fill; AI without a model stops."""
    setUp, run_setup = PaperlessMail.setUp, PaperlessMail.run_setup

    def test_hand_settings_give_way_to_the_values(self):
        fake = Fake(config=[{"id": 1, "ai_enabled": True, "llm_backend": "openai-like", "llm_api_key": "**********",
                             "llm_embedding_backend": "huggingface", "mode": "force", "deskew": True,
                             "rotate_pages": None, "output_type": "pdfa", "barcodes_enabled": True}])
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": "", "AI_ENABLED": "true", "MODEL_URL": "http://host.containers.internal:11435/v1"}, password="")
        self.assertEqual(r.returncode, 0, r.stderr)
        patch = [c[2] for c in fake.calls if c[:2] == ("PATCH", "/api/config/1/")]
        self.assertEqual(patch, [{"ai_enabled": None, "deskew": None, "llm_api_key": None, "llm_backend": None,
                                  "llm_embedding_backend": None, "mode": None}])
        # what the template does not set stays the owner's
        self.assertEqual((fake.config[0]["output_type"], fake.config[0]["barcodes_enabled"]), ("pdfa", True))
        fake.calls.clear()
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": ""}, password="")
        self.assertEqual([c for c in fake.writes() if c[1].startswith("/api/config/")], [])

    def test_the_contract_fields_are_made_once(self):
        fake = Fake(fields=[{"id": 1, "name": "vertragsnummer", "data_type": "string"}])
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": ""}, password="")
        self.assertEqual(r.returncode, 0, r.stderr)
        names = {f["name"]: f["data_type"] for f in fake.fields}
        self.assertEqual(len(fake.fields), 11, names)   # the hand-made one taken, not doubled
        self.assertEqual((names["Nächster Kündigungstermin"], names["Betrag"], names["vertragsnummer"]), ("date", "monetary", "string"))
        self.assertEqual((names["Rechnungsnummer"], names["Fällig am"], names["Verwendungszweck"]), ("string", "date", "string"),
                         "#818, #820: the invoice fields")
        fake.calls.clear()
        self.run_setup(fake, {"PAPERLESS_MAIL_HOST": ""}, password="")
        self.assertEqual([c for c in fake.writes() if c[1] == "/api/custom_fields/"], [])

    def test_ai_without_a_model_stops_before_paperless_is_asked(self):
        fake = Fake()
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": "", "AI_ENABLED": "true", "MODEL_URL": ""}, password="")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("AI_ENABLED", r.stderr)
        self.assertEqual(fake.calls, [])


if __name__ == "__main__":
    unittest.main()
