"""#44: the Werkstatt's wiring — provider id, tokens per container, retries
before escalation, and work_ticket run for real against fakes."""
import importlib.util, json, os, re, subprocess, sys, tempfile, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent          # werkstatt/
ROOT = HERE.parent
spec = importlib.util.spec_from_file_location("autoloop", HERE / "autoloop.py")
al = importlib.util.module_from_spec(spec); spec.loader.exec_module(al)


class ProviderId(unittest.TestCase):
    def test_autoloop_pins_the_provider_the_extension_registers(self):
        js = (HERE / "extensions" / "laib-llama.js").read_text()
        ext_id = re.search(r'PROVIDER_ID\s*=\s*"([^"]+)"', js) or re.search(r'PROVIDER_ID\s*=\s*"([^"]+)"', (HERE / "extensions" / "catalog.mjs").read_text())
        self.assertIsNotNone(ext_id)
        self.assertEqual(al.PROVIDER_ID, ext_id.group(1))
        self.assertEqual(al.PROVIDER_ID, "laib-llama")

    def test_extension_lists_the_leases_real_catalog(self):
        # the lease's /v1/models answers id + allowed_in_mode and no `pi` object
        # (internal/lease catalogue); the extension must still list every preset
        catalog = {"object": "list", "mode": "haushalt", "data": [
            {"id": "gemma-4-e4b", "object": "model", "allowed_in_mode": True},
            {"id": al.CODING_PRESET, "object": "model", "allowed_in_mode": False},
        ]}
        script = (
            "import('./catalog.mjs').then(m => {"
            " const models = m.fromCatalog(JSON.parse(process.argv[1]));"
            " console.log(JSON.stringify(models)); })"
        )
        res = subprocess.run(["node", "--input-type=module", "-e", script, json.dumps(catalog)],
                             cwd=HERE / "extensions", capture_output=True, text=True, timeout=30)
        self.assertEqual(res.returncode, 0, res.stderr)
        models = json.loads(res.stdout)
        self.assertEqual([m["id"] for m in models], ["gemma-4-e4b", al.CODING_PRESET])
        self.assertTrue(all(m["provider"] == al.PROVIDER_ID for m in models))
        self.assertTrue(all(m["contextWindow"] > 0 and m["api"] == "openai-completions" for m in models))


class TokensPerContainer(unittest.TestCase):
    def test_tokens_live_in_the_containers_own_tmpfs_not_on_the_shared_volume(self):
        entry = (HERE / "entrypoint.sh").read_text()
        self.assertNotIn("/workspace/.laib", entry, "a token on the shared volume is the other container's token")
        self.assertIn("/run/laib/token", entry)
        self.assertIn("/run/laib/git-credentials", entry)
        laibctl = (HERE / "laibctl.py").read_text()
        self.assertIn('"/run/laib/token"', laibctl)
        for unit in ("werkstatt.container", "werkstatt-autoloop.container"):
            text = (ROOT / "templates" / "werkstatt" / unit).read_text()
            self.assertRegex(text, r"(?m)^Tmpfs=/run/laib", unit + " needs its own tmpfs for the tokens")
        auto = (ROOT / "templates" / "werkstatt" / "werkstatt-autoloop.container").read_text()
        self.assertIn("WERKSTATT_GIT_TOKEN_FILE=/run/laib/git-credentials", auto)
        self.assertIn("WERKSTATT_GIT_TOKEN=${PI_GIT_TOKEN}", auto, "pi pushes with its own, narrower GitHub token")
        contract = (ROOT / "templates" / "werkstatt" / "laib.yml").read_text()
        self.assertIn("WERKSTATT_PI_GIT_TOKEN", contract)
        self.assertNotIn("--ttl 0", contract, "the claude token keeps the TTL policy.yml declares")

    def test_no_compiled_python_is_tracked(self):
        res = subprocess.run(["git", "ls-files"], cwd=ROOT, capture_output=True, text=True)
        self.assertEqual([l for l in res.stdout.splitlines() if l.endswith(".pyc")], [])


class FakeGitHub:
    """Collects every REST call; answers like GitHub for what work_ticket uses."""
    def __init__(self):
        self.calls = []
        self.pr_number = 9

    def __call__(self, url, method, token="", body=None):
        self.calls.append((method, url, body))
        if url.endswith("/v1/models"):
            return 200, {"data": [{"id": al.CODING_PRESET}]}
        if url.endswith("/pulls") and method == "POST":
            return 201, {"number": self.pr_number, "html_url": f"https://github.com/x/y/pull/{self.pr_number}"}
        return 200, {}

    def labels_added(self):
        return [b["labels"] for m, u, b in self.calls if m == "POST" and u.endswith("/labels")]

    def labels_removed(self):
        return [u.rsplit("/", 1)[1] for m, u, b in self.calls if m == "DELETE" and "/labels/" in u]


def fake_box(test):
    """A clone that is a real local repo, a pi that edits a file, gates that pass, a push that succeeds."""
    work = Path(tempfile.mkdtemp()); test.addCleanup(lambda: subprocess.run(["rm", "-rf", str(work)]))
    cfg = al.config_from_env({"PI_AUTOLOOP_PROTOCOL_DIR": str(work / "protocol"), "PI_AUTOLOOP_WORKROOT": str(work / "root"),
                              "PI_AUTOLOOP_REPOS": "mdopp/x", "PI_AUTOLOOP_API_URL": "https://api", "MODEL_URL": "http://m/v1"})
    real_git = al.git

    def clone(repo, branch, dest):
        dest.mkdir(parents=True, exist_ok=True)
        subprocess.run(["git", "init", "-q", "-b", branch, str(dest)], check=True)
        (dest / "README").write_text("x\n")
        real_git(["add", "-A"], dest); real_git(["-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base"], dest)
        return True

    def run_pi(prompt, model, cwd, cap):
        (cwd / "fix.txt").write_text("worked\n")
        return al.summarise_events(['{"type":"assistant","text":"done"}']), False, ""

    def git(args, cwd, timeout=600.0):
        if args and args[0] == "push":
            return subprocess.CompletedProcess(args, 0, "", "")
        return real_git(args, cwd, timeout)

    al.clone, al.run_pi, al.git, al.run_gates = clone, run_pi, git, lambda cwd: []
    test.addCleanup(lambda: setattr(al, "git", real_git))
    return cfg, work


class WorkTicket(unittest.TestCase):
    def test_a_ticket_goes_through_to_a_pull_request(self):
        cfg, work = fake_box(self)
        gh = FakeGitHub()
        al.work_ticket(cfg, "mdopp/x", {"number": 7, "title": "Fix the thing", "body": "do it"}, "tok", "main", gh)
        prs = [b for m, u, b in gh.calls if u.endswith("/pulls")]
        self.assertEqual(len(prs), 1, gh.calls)
        self.assertEqual(prs[0]["base"], "main"); self.assertIn("pi/7", prs[0]["head"]); self.assertFalse(prs[0]["draft"])
        self.assertEqual(gh.labels_added(), [], "a worked ticket is not escalated")
        self.assertEqual(gh.labels_removed(), [])
        self.assertTrue(al.protocol_path(cfg["protocol_dir"], "mdopp/x", 7).exists())
        comments = [b["body"] for m, u, b in gh.calls if u.endswith("/issues/9/comments")]
        self.assertEqual(len(comments), 1, "the protocol is a comment on the PR")

    def test_a_transient_failure_is_retried_before_the_ticket_is_escalated(self):
        cfg, work = fake_box(self)
        al.clone = lambda repo, branch, dest: False          # the network is down this pass
        gh = FakeGitHub()
        issue = {"number": 8, "title": "Flaky", "body": ""}
        for n in range(1, al.TRANSIENT_ATTEMPTS):
            al.work_ticket(cfg, "mdopp/x", issue, "tok", "main", gh)
            self.assertEqual(gh.labels_added(), [], f"pass {n}: a transient failure keeps pi:ready")
            self.assertEqual(gh.labels_removed(), [])
        al.work_ticket(cfg, "mdopp/x", issue, "tok", "main", gh)
        self.assertEqual(gh.labels_added(), [[cfg["escalate_label"]]], "after the last attempt the ticket goes to Claude")
        self.assertEqual(gh.labels_removed(), [cfg["label"]])
        self.assertTrue(any("Versuch" in (b or {}).get("body", "") for m, u, b in gh.calls if u.endswith("/issues/8/comments")),
                        "the hand-over says how many attempts there were")

    def test_a_run_without_a_change_is_escalated_at_once(self):
        cfg, work = fake_box(self)
        al.run_pi = lambda prompt, model, cwd, cap: (al.summarise_events([]), False, "")   # pi ran, changed nothing
        gh = FakeGitHub()
        al.work_ticket(cfg, "mdopp/x", {"number": 9, "title": "Nothing", "body": ""}, "tok", "main", gh)
        self.assertEqual(gh.labels_added(), [[cfg["escalate_label"]]])


if __name__ == "__main__":
    unittest.main()
