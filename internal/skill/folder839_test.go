package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// #839: a skill is a folder, skill.yml and for a built-in one a run.py.
// Only the image brings a script: a state repo's folder with one is
// refused, and so is a file the old way, which says where it goes now.
func TestASkillIsAFolderAndOnlyABuiltinHasAScript(t *testing.T) {
	const yml = "description: x\ntrigger: {every: 10m}\ntools: [note]\noutput: note\nprompt: p\n"
	builtin := fstest.MapFS{
		"probe/skill.yml":   {Data: []byte(yml + "run: run.py\n")},
		"probe/run.py":      {Data: []byte("def run(s): pass\n")},
		"lost/skill.yml":    {Data: []byte(yml + "run: run.py\n")},
		"plain/skill.yml":   {Data: []byte(yml)},
		"renamed/skill.yml": {Data: []byte(yml + "run: main.py\n")},
	}
	state := t.TempDir()
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(state, p)), 0o755)
		os.WriteFile(filepath.Join(state, p), []byte(s), 0o644)
	}
	write("skills/garten/skill.yml", yml)
	write("skills/schmuggel/skill.yml", yml)
	write("skills/schmuggel/run.py", "import os\n")
	write("skills/plain/skill.yml", yml+"run: run.py\n")
	write("skills/alt.yml", yml)
	want := map[string]string{
		"probe": "", "garten": "", "lost": "fehlt", "renamed": "heißt run.py",
		"schmuggel": "nur eine eingebaute", "plain": "nur eine eingebaute", "alt": "skills/alt/skill.yml",
	}
	sks := Load(builtin, state)
	for name, why := range want {
		s := Find(sks, name)
		if s == nil || why == "" && s.Refused != "" || why != "" && !strings.Contains(s.Refused, why) {
			t.Errorf("%s: %+v, want refused %q", name, s, why)
		}
	}
	if s := Find(sks, "garten"); s.Source != "state" {
		t.Errorf("garten is the state repo's: %+v", s)
	}
	if s := Find(sks, "probe"); s.Source != "builtin" || s.Run != "run.py" {
		t.Errorf("probe is a built-in with its script: %+v", s)
	}
}

// Every built-in skill is a folder whose script, when it names one, is there.
func TestTheBuiltinSkillsAreFolders(t *testing.T) {
	sks := Load(os.DirFS("../../skills"), t.TempDir())
	if len(sks) < 8 {
		t.Fatalf("the built-in skills: %d", len(sks))
	}
	for _, s := range sks {
		if s.Refused != "" {
			t.Errorf("%s is refused: %s", s.Name, s.Refused)
		}
	}
}
