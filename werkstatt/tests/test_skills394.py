"""#394: pi's handbook and skills came from ServiceBay's agent kit, a git
checkout ServiceBay refreshed hourly into its own data dir. ServiceBay is
off and that dir goes after 30 days. The Werkstatt carries the kit itself:
the guide and its skills are `werkstatt/guide/` in this repo, in the image
that is rebuilt on every push to main and auto-updated on the box (no
release, no timer, one source). At every start `werkstatt-skills` links
each skill where pi (terminal and web) and Claude Code look for skills."""
import os, re, subprocess, tempfile, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
SKILLS = HERE / "guide" / "skills"
SCRIPT = HERE / "skills.sh"


def front(text):
    m = re.match(r"---\n(.*?)\n---\n", text, re.S)
    return dict(re.findall(r"(?m)^(\w+): (.+)$", m.group(1))) if m else {}


class Skills(unittest.TestCase):
    def test_every_skill_is_an_agent_skill(self):
        dirs = sorted(p for p in SKILLS.iterdir() if p.is_dir())
        self.assertGreaterEqual(len(dirs), 5, "the general entries of ServiceBay's catalog are not carried")
        for d in dirs:
            f = front((d / "SKILL.md").read_text())
            self.assertEqual(f.get("name"), d.name, d)
            self.assertGreater(len(f.get("description", "")), 40, f"{d.name}: the description decides whether an agent opens it")

    def test_the_skills_are_linked_for_pi_and_claude_and_stale_links_go(self):
        with tempfile.TemporaryDirectory() as home:
            h = Path(home)
            own = h / ".claude" / "skills" / "mine"
            own.mkdir(parents=True)
            (own / "SKILL.md").write_text("---\nname: mine\ndescription: x\n---\n")
            os.symlink(str(SKILLS / "gone-skill"), h / ".claude" / "skills" / "gone-skill")   # a skill a newer image dropped
            env = dict(os.environ, HOME=home, WERKSTATT_SKILLS=str(SKILLS))
            for _ in range(2):   # every start, idempotent
                p = subprocess.run(["bash", str(SCRIPT)], env=env, capture_output=True, text=True, timeout=30)
                self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
            for d in (p for p in SKILLS.iterdir() if p.is_dir()):
                self.assertTrue((h / ".claude" / "skills" / d.name / "SKILL.md").is_file(), d.name)
            for agent in (".pi/agent", ".pi/web"):
                self.assertEqual(os.readlink(h / agent / "skills" / "laib"), str(SKILLS), agent)
            self.assertFalse(os.path.lexists(h / ".claude" / "skills" / "gone-skill"), "a dropped skill stays linked")
            self.assertTrue((own / "SKILL.md").exists(), "a person's own skill was touched")

    def test_the_image_and_the_start_carry_them(self):
        self.assertIn("COPY skills.sh /usr/local/bin/werkstatt-skills", (HERE / "Containerfile").read_text())
        self.assertIn("werkstatt-skills", (HERE / "entrypoint.sh").read_text())
        guide = (HERE / "guide" / "AGENTS.md").read_text()
        self.assertIn("skills/", guide)
        self.assertIn("/workspace/servicebay/assists", guide, "the guide does not say where ServiceBay's catalog is read now")


if __name__ == "__main__":
    unittest.main()
