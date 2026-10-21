package skill

import (
	"os"
	"strings"
	"testing"
)

// #544: lehre declares its mechanics as a field the kernel checks — the
// runner proposes lessons from incident sessions; nothing else is a
// lessons skill.
func TestTheLessonsSkillIsChecked(t *testing.T) {
	var lehre *Skill
	sks := Load(os.DirFS("../../skills"), t.TempDir())
	if lehre = Find(sks, "lehre"); lehre == nil || lehre.Refused != "" || lehre.Lessons != "vorfall" {
		t.Fatalf("the built-in lehre: %+v", lehre)
	}
	for yml, why := range map[string]string{
		"description: x\ntrigger: {daily: \"07:00\"}\ntools: [note]\nlessons: vorfall\nprompt: p\n":                           "propose",
		"description: x\ntrigger: {daily: \"07:00\"}\ntools: [note, propose]\nlessons: alles\nprompt: p\n":                    "lessons",
		"description: x\ntrigger: {daily: \"07:00\"}\ntools: [note, propose]\nlessons: vorfall\noutput: propose\nprompt: p\n": "",
	} {
		s := parse("x", "state", []byte(yml), nil)
		if why == "" && s.Refused != "" || why != "" && !strings.Contains(s.Refused, why) {
			t.Errorf("%q: refused %q, want %q", yml, s.Refused, why)
		}
	}
}
