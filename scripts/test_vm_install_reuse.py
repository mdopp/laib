"""#604: scripts/vm-install-reuse.sh lets the release and main's push reuse a
green vm-install of the same code instead of running it again. git is real
(a train head, main's merge of it, the release commit on top); gh is a stub
that answers from JSON files, so nothing here needs GitHub."""
import json, os, stat, subprocess, tempfile, unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "vm-install-reuse.sh"
GH = """#!/usr/bin/env bash
# gh run list ... -> runs.json; gh run view <id> --json jobs -> run-<id>.json
case "$1 $2" in
  "run list") cat "$GH_STUB/runs.json";;
  "run view") cat "$GH_STUB/run-$3.json";;
  *) echo "gh stub: $*" >&2; exit 2;;
esac
"""


class Reuse(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        t = Path(tmp.name)
        self.repo, self.stub = t / "repo", t / "stub"
        self.repo.mkdir(); self.stub.mkdir()
        gh = self.stub / "gh"
        gh.write_text(GH)
        gh.chmod(gh.stat().st_mode | stat.S_IEXEC)
        self.env = dict(os.environ, PATH=f"{self.stub}:{os.environ['PATH']}", GH_STUB=str(self.stub),
                        GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t", GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t")
        self.git("init", "-q", "-b", "main")
        self.write("release-please-config.json", json.dumps({"release-type": "simple", "packages": {".": {"extra-files": ["docs/VERSION.md"]}}}))
        self.write("code.go", "package main\n"); self.write("version.txt", "0.1.0\n")
        self.base = self.commit("base")
        # a train branched off main, main merges it with a merge commit
        self.git("checkout", "-q", "-b", "train/x")
        self.write("code.go", "package main // train\n")
        self.head = self.commit("train head")
        self.git("checkout", "-q", "main")
        self.git("merge", "-q", "--no-ff", "-m", "merge train", "train/x")
        self.merge = self.rev("HEAD")
        self.runs = []

    def git(self, *a):
        return subprocess.run(["git", *a], cwd=self.repo, env=self.env, check=True, capture_output=True, text=True).stdout.strip()

    def write(self, name, text):
        p = self.repo / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)

    def commit(self, msg):
        self.git("add", "-A"); self.git("commit", "-q", "-m", msg)
        return self.rev("HEAD")

    def rev(self, r):
        return self.git("rev-parse", r)

    def tree(self, r):
        return self.rev(f"{r}^{{tree}}")

    def green(self, rid, sha, proven=None, conclusion="success"):
        """a green run on sha; proven = the tree its proven job names (None: no such job)"""
        self.runs.append({"databaseId": rid, "headSha": sha})
        jobs = [{"name": "build", "conclusion": "success"}]
        if proven:
            jobs.append({"name": f"proven: tree {proven}", "conclusion": conclusion})
        (self.stub / f"run-{rid}.json").write_text(json.dumps({"jobs": jobs}))

    def reuse(self, commit="HEAD"):
        (self.stub / "runs.json").write_text(json.dumps(self.runs))
        return subprocess.run(["bash", str(SCRIPT), commit], cwd=self.repo, env=self.env, capture_output=True, text=True)

    def release(self, extra=None):
        self.write("CHANGELOG.md", "## 0.2.0\n"); self.write("version.txt", "0.2.0\n")
        self.write(".release-please-manifest.json", '{".":"0.2.0"}\n'); self.write("docs/VERSION.md", "0.2.0\n")
        if extra:
            self.write(*extra)
        return self.commit("chore(main): release 0.2.0")

    def test_mains_merge_reuses_the_train_heads_run(self):
        self.green(11, self.head, self.tree(self.head))
        r = self.reuse()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(r.stdout.split(), ["11", self.head, self.tree(self.head), "same-tree"])

    def test_the_release_commit_reuses_it_too(self):
        self.green(11, self.head, self.tree(self.head))
        self.release()
        r = self.reuse()
        self.assertEqual(r.returncode, 0, r.stderr)
        rid, sha, _, why = r.stdout.split()
        self.assertEqual((rid, sha), ("11", self.head))
        self.assertTrue(why.startswith("only-release-files:"), why)
        for f in ("CHANGELOG.md", "version.txt", ".release-please-manifest.json", "docs/VERSION.md"):
            self.assertIn(f, why)

    def test_a_release_that_also_changes_code_is_not_reused(self):
        self.green(11, self.head, self.tree(self.head))
        self.release(("code.go", "package main // slipped in\n"))
        r = self.reuse()
        self.assertEqual(r.returncode, 1)
        self.assertIn("code.go", r.stderr)

    def test_main_moved_on_after_the_train(self):
        # a single PR merged after the train: main's tree is not the train's
        self.green(11, self.head, self.tree(self.head))
        self.write("other.go", "package main\n")
        self.commit("fix: a single PR")
        self.assertEqual(self.reuse().returncode, 1)

    def test_a_run_without_a_green_proven_job_proves_nothing(self):
        self.green(11, self.head)                                         # a single PR's `not run`, or a skip
        self.green(12, self.head, self.tree(self.head), "failure")
        self.assertEqual(self.reuse().returncode, 1)

    def test_a_claim_for_a_tree_its_head_does_not_have_is_not_taken(self):
        # a PR run tests the merge with main; when that differs from its head, the claim names another tree
        self.green(11, self.head, self.tree(self.base))
        r = self.reuse()
        self.assertEqual(r.returncode, 1)
        self.assertIn("not taken", r.stderr)

    def test_a_run_far_away_is_not_a_candidate(self):
        # a green run of the same tree, but on a commit outside the walk
        self.git("checkout", "-q", "-b", "elsewhere", self.base)
        self.write("code.go", "package main // train\n")
        away = self.commit("same tree, elsewhere")
        self.git("checkout", "-q", "main")
        self.green(13, away, self.tree(away))
        self.assertEqual(self.reuse().returncode, 1)

    def test_gh_failing_is_no_reuse(self):
        (self.stub / "gh").write_text("#!/bin/sh\necho 'HTTP 401' >&2; exit 1\n")
        r = self.reuse()
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(r.stdout, "")


if __name__ == "__main__":
    unittest.main()
