"""The mail receipt (owner 2026-11-09): a document that came by mail is told to
whoever sent it, one message per mail; a scan or the consume folder is never
told; a refused attachment says so. Against a fake Paperless API and a fake
mailbox, never real ones."""
import importlib.util, os, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("quittung", HERE / "paperless-quittung.py")
Q = importlib.util.module_from_spec(spec)
spec.loader.exec_module(Q)

JETZT = 1_800_000_000
T = "2027-01-15T08:%02d:00+00:00"   # only the order of these times matters


class Fake:
    """Paperless' API as the receipt reads it"""

    def __init__(self, processed, tasks, docs=None, users=None):
        self.processed, self.tasks, self.docs = processed, tasks, docs or {}
        self.users = users if users is not None else [{"username": "mama", "email": "Mama@Example.org"}]
        self.accounts = [{"id": 1, "imap_server": "imap.example.org", "username": "webmaster@example.org"},
                         {"id": 2, "imap_server": "other.example.org", "username": "x@example.org"}]
        self.rules = [{"id": 7, "account": 1}, {"id": 8, "account": 2}]

    def all(self, path):
        return {"/api/mail_accounts/": self.accounts, "/api/mail_rules/": self.rules,
                "/api/processed_mail/": self.processed, "/api/users/": self.users}[path]

    def __call__(self, path, accept="application/json"):
        if path.startswith("/api/tasks/"):
            assert "version=9" in accept
            return self.tasks
        if path.startswith("/api/documents/"):
            return self.docs[int(path.split("/")[3])]
        raise AssertionError(path)


class Postfach:
    def __init__(self, mails):
        self.mails, self.gefragt = mails, []

    def mail(self, ordner, uid):
        self.gefragt.append((ordner, uid))
        return self.mails.get(uid)


def mail(id, uid, status="SUCCESS", rule=7, processed=T % 30, subject="Rechnung"):
    return {"id": id, "rule": rule, "folder": "INBOX", "uid": uid, "subject": subject, "processed": processed, "status": status}


def task(id, datei, status="SUCCESS", doc=None, result="", created=T % 20):
    return {"id": id, "task_file_name": datei, "status": status, "related_document": doc, "result": result, "date_created": created}


class Receipt(unittest.TestCase):
    def setUp(self):
        os.environ.update(PAPERLESS_MAIL_HOST="imap.example.org", PAPERLESS_MAIL_USER="webmaster@example.org",
                          NOTIFY_PERSON="papa", PAPERLESS_URL="https://paperless.example.org")

    def run_(self, fake, box, stand=None):
        stand = {"bis": 0} if stand is None else stand
        return Q.quittung(fake, stand, JETZT, box), stand

    def test_a_document_by_mail_is_told_to_its_sender(self):
        fake = Fake([mail(1, 101)], [task(11, "Rechnung März.pdf", doc=5)],
                    docs={5: {"id": 5, "title": "Stromrechnung März", "page_count": 2}})
        box = Postfach({101: ("mama@example.org", ["Rechnung März.pdf"])})
        zeilen, stand = self.run_(fake, box)
        self.assertEqual(zeilen, [{"an": "mama", "at": JETZT, "text": "Per Mail abgelegt: Stromrechnung März (von mama@example.org), "
                                   "2 Seiten – https://paperless.example.org/documents/5/details"}])
        self.assertEqual(stand["bis"], 1)
        self.assertEqual(self.run_(fake, box, stand)[0], [], "the same mail was told twice")

    def test_a_scan_or_the_consume_folder_is_never_told(self):
        # a consume task with no mail record of Paperless' (a scan, the consume folder, an upload)
        fake = Fake([], [task(11, "scan-2027-01-15.pdf", doc=5)], docs={5: {"id": 5, "title": "Brief", "page_count": 1}})
        self.assertEqual(self.run_(fake, Postfach({}))[0], [])
        # a mail of the same time whose attachment is another file: the scan's task is not its document
        fake.processed = [mail(1, 101)]
        zeilen, _ = self.run_(fake, Postfach({101: ("mama@example.org", ["anderes.pdf"])}))
        self.assertNotIn("Brief", zeilen[0]["text"])

    def test_a_duplicate_is_told_as_refused(self):
        for result, status in (("Not consuming rechnung.pdf: It is a duplicate of Strom (#4)", "FAILURE"),   # Paperless 2.x
                               ("Not consuming: It is a duplicate of document #4", "SUCCESS")):          # 3.x, related_document is the old one
            fake = Fake([mail(1, 101, status="FAILED")], [task(11, "rechnung.pdf", status=status, doc=4, result=result)])
            zeilen, _ = self.run_(fake, Postfach({101: ("mama@example.org", ["rechnung.pdf"])}))
            self.assertEqual([z["text"] for z in zeilen], ["Per Mail nicht abgelegt: rechnung.pdf (von mama@example.org): schon vorhanden"])

    def test_several_attachments_are_one_message(self):
        fake = Fake([mail(1, 101)], [task(11, "a.pdf", doc=5), task(12, "b.pdf", status="FAILURE", result="Error: no text"),
                                     task(13, "c.pdf", doc=6)],
                    docs={5: {"id": 5, "title": "A", "page_count": 1}, 6: {"id": 6, "title": "C", "page_count": 3}})
        zeilen, _ = self.run_(fake, Postfach({101: ("mama@example.org", ["a.pdf", "b.pdf", "c.pdf"])}))
        self.assertEqual(len(zeilen), 1)
        self.assertEqual(zeilen[0]["text"], "Per Mail (von mama@example.org):\n"
                         "abgelegt: A, 1 Seite – https://paperless.example.org/documents/5/details\n"
                         "abgelegt: C, 3 Seiten – https://paperless.example.org/documents/6/details\n"
                         "nicht abgelegt: b.pdf: nicht lesbar")

    def test_a_stranger_goes_to_the_notify_person_and_a_name_is_matched_as_paperless_keeps_it(self):
        fake = Fake([mail(1, 101)], [task(11, "Rechnung_Marz.pdf", doc=5)], docs={5: {"id": 5, "title": "R", "page_count": None}})
        zeilen, _ = self.run_(fake, Postfach({101: ("shop@example.com", ["Rechnung Marz.pdf"])}))
        self.assertEqual(zeilen[0]["an"], "papa")
        self.assertEqual(zeilen[0]["text"], "Per Mail abgelegt: R (von shop@example.com) – https://paperless.example.org/documents/5/details")

    def test_a_mail_whose_document_is_still_filed_waits(self):
        fake = Fake([mail(1, 101)], [task(11, "a.pdf", status="STARTED")])
        box = Postfach({101: ("mama@example.org", ["a.pdf"])})
        zeilen, stand = self.run_(fake, box)
        self.assertEqual(zeilen, [])
        self.assertIn("1", stand["offen"])
        fake.tasks, fake.docs = [task(11, "a.pdf", doc=5)], {5: {"id": 5, "title": "A", "page_count": 1}}
        zeilen, stand = self.run_(fake, box, stand)
        self.assertEqual(len(zeilen), 1)
        self.assertEqual(stand["offen"], {})

    def test_the_first_run_tells_nothing_of_before(self):
        fake = Fake([mail(3, 103), mail(2, 102)], [task(11, "a.pdf", doc=5)])
        zeilen, stand = self.run_(fake, Postfach({}), stand={})
        self.assertEqual((zeilen, stand["bis"]), ([], 3))

    def test_another_accounts_rule_and_a_strangers_mail_without_attachment_are_not_told(self):
        fake = Fake([mail(1, 101, rule=8), mail(2, 102, status="PROCESSED_WO_CONSUMPTION")], [])
        box = Postfach({101: ("mama@example.org", ["a.pdf"]), 102: ("newsletter@example.com", [])})
        self.assertEqual(self.run_(fake, box)[0], [])
        self.assertEqual(box.gefragt, [("INBOX", 102)], "a mail of another account's rule was looked up")
        fake.processed = [mail(3, 103, status="PROCESSED_WO_CONSUMPTION", subject="Hallo")]
        zeilen, _ = self.run_(fake, Postfach({103: ("mama@example.org", [])}))
        self.assertEqual(zeilen[0]["text"], "Per Mail nicht abgelegt: Hallo (von mama@example.org): kein Anhang, den Paperless ablegen kann")


class Mailbox(unittest.TestCase):
    def test_the_sender_and_the_attachments_names_come_from_the_mail(self):
        from email.message import EmailMessage
        m = EmailMessage()
        m["From"], m["To"], m["Subject"] = "Mama <Mama@Example.org>", "dokumente@example.org", "Rechnung"
        m.set_content("anbei")
        m.add_attachment(b"%PDF-1.4", maintype="application", subtype="pdf", filename="Rechnung März.pdf")
        roh = m.as_bytes()

        class Imap:
            def __init__(self):
                self.calls = []

            def select(self, ordner, readonly=False):
                self.calls.append(("select", ordner, readonly))
                return "OK", [b"1"]

            def uid(self, *a):
                self.calls.append(a)
                return "OK", [(b"1 (UID 101 BODY[] {%d}" % len(roh), roh), b")"]

        p = Q.Postfach()
        p.imap = Imap()
        self.assertEqual(p.mail("INBOX", 101), ("Mama@Example.org", ["Rechnung März.pdf"]))
        self.assertEqual(p.imap.calls, [("select", '"INBOX"', True), ("FETCH", "101", "(BODY.PEEK[])")],
                         "the mailbox is read with PEEK, read-only: no flag of the mail changes")


class Unit(unittest.TestCase):
    def test_the_bearer_never_reaches_the_container(self):
        sh = (HERE / "paperless-quittung.sh").read_text()
        exec_line = sh[sh.index("exec -i"):sh.index("python3 - quittung")]
        self.assertIn("-e PAPERLESS_MAIL_PASSWORD", exec_line)
        self.assertNotIn("NOTIFY_TOKEN", exec_line)
        self.assertIn("--user paperless", exec_line)


if __name__ == "__main__":
    unittest.main()
