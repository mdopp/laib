package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/skill"
	"github.com/mdopp/laib/internal/state"
)

// skillBox is a state repo with skills/<name>/skill.yml files and the ops hand at
// a ceiling; statuses is policy.yml skills.
func skillBox(t *testing.T, ceiling string, files, statuses map[string]string) (*cliEnv, *state.State) {
	t.Helper()
	dir := t.TempDir()
	for n, c := range files {
		os.MkdirAll(filepath.Join(dir, "skills", n), 0o755)
		if err := os.WriteFile(filepath.Join(dir, "skills", n, "skill.yml"), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st := &state.State{Policy: state.Policy{Hands: map[string]state.Hand{"ops": {Ceiling: ceiling}}, Skills: statuses}}
	return &cliEnv{Paths: boxfs.Paths{State: dir}}, st
}

func findSkill(sks []skill.Skill, name string) *skill.Skill {
	for i := range sks {
		if sks[i].Name == name {
			return &sks[i]
		}
	}
	return nil
}

const probeSkill = `description: Schaut nach und startet neu.
trigger: {every: 5m}
tools: [status, restart, note]
output: note
prompt: Schau nach.
`

// ADR 0023 §4: a skill never reaches a tool above the ops hand's ceiling.
// The kernel cuts it from the skill's tools/list and refuses the call —
// the skill file and the prompt can ask, they cannot get.
func TestASkillNeverGetsAToolAboveTheCeiling(t *testing.T) {
	env, st := skillBox(t, "propose", map[string]string{"probe": probeSkill}, nil)
	sk := findSkill(mcp.SkillsFor(env, st, "ops"), "probe")
	if sk == nil || sk.Status != "active" || sk.Source != "state" {
		t.Fatalf("the added skill is not active: %+v", sk)
	}
	if slices.Contains(sk.Granted, "restart") || sk.Denied["restart"] == "" || !slices.Contains(sk.Granted, "status") {
		t.Fatalf("granted %v, denied %v", sk.Granted, sk.Denied)
	}
	if why := mcp.SkillGate(env, st, "ops", "probe", "restart"); why == "" {
		t.Fatal("the kernel let a skill call restart above the ceiling propose")
	}
	if why := mcp.SkillGate(env, st, "ops", "probe", "status"); why != "" {
		t.Fatalf("status refused: %s", why)
	}
	var listed []string
	for _, tl := range mcp.SkillTools(env, st, "ops", "probe", mcp.Tools(st, "ops")) {
		listed = append(listed, tl["name"].(string))
	}
	if strings.Join(listed, ",") != "laib_status,laib_note" {
		t.Fatalf("tools/list in the skill's name: %v", listed)
	}

	// at ceiling apply restart is granted, and a verb the skill did not name
	// stays refused although the hand could use it
	env, st = skillBox(t, "apply", map[string]string{"probe": probeSkill}, nil)
	if why := mcp.SkillGate(env, st, "ops", "probe", "restart"); why != "" {
		t.Fatalf("restart at ceiling apply: %s", why)
	}
	if why := mcp.SkillGate(env, st, "ops", "probe", "exec"); why == "" {
		t.Fatal("a skill called a tool it does not name")
	}
}

// A skill that names a tool no Operator may hold is refused as a whole:
// none of its calls run, not even the harmless ones.
func TestASkillNamingAToolOutsideTheVocabularyIsRefused(t *testing.T) {
	greedy := strings.Replace(probeSkill, "[status, restart, note]", "[status, approve, note]", 1)
	env, st := skillBox(t, "apply", map[string]string{"gierig": greedy, "stumm": strings.Replace(probeSkill, ", note]", "]", 1)}, nil)
	sks := mcp.SkillsFor(env, st, "ops")
	if sk := findSkill(sks, "gierig"); sk == nil || sk.Status != "refused" || !strings.Contains(sk.Refused, "approve") {
		t.Fatalf("%+v", sk)
	}
	if sk := findSkill(sks, "stumm"); sk == nil || sk.Status != "refused" || !strings.Contains(sk.Refused, "note") {
		t.Fatalf("a skill without note must be refused: %+v", sk)
	}
	if why := mcp.SkillGate(env, st, "ops", "gierig", "status"); why == "" {
		t.Fatal("a refused skill's call ran")
	}
	if tl := mcp.SkillTools(env, st, "ops", "gierig", mcp.Tools(st, "ops")); len(tl) != 0 {
		t.Fatalf("a refused skill sees tools: %v", tl)
	}
}

// A paused skill never runs: the kernel refuses every call in its name.
// Today's three behaviours are built in and active; a name nobody knows is
// refused.
func TestAPausedSkillsCallsAreRefused(t *testing.T) {
	env, st := skillBox(t, "apply", map[string]string{"probe": probeSkill}, map[string]string{"probe": "paused", "morgenbericht": "paused"})
	sks := mcp.SkillsFor(env, st, "ops")
	for name, want := range map[string]string{"probe": "paused", "morgenbericht": "paused", "routineblick": "active", "vorfall": "active"} {
		sk := findSkill(sks, name)
		if sk == nil || sk.Status != want {
			t.Fatalf("%s: %+v, want %s", name, sk, want)
		}
	}
	if why := mcp.SkillGate(env, st, "ops", "probe", "status"); !strings.Contains(why, "ruht") {
		t.Fatalf("a paused skill's call: %q", why)
	}
	if why := mcp.SkillGate(env, st, "ops", "gibtsnicht", "status"); why == "" {
		t.Fatal("a call for an unknown skill ran")
	}
	if v := findSkill(sks, "vorfall"); v.Source != "builtin" || !slices.Contains(v.Granted, "restart") || v.Trigger.Event != "red" {
		t.Fatalf("vorfall: %+v", v)
	}
}

// #286: a skill's `more` tools are checked like its tools — the vocabulary
// and the ceiling — before a capable model gets them.
func TestASkillsMoreToolsAreCheckedLikeItsTools(t *testing.T) {
	wide := strings.Replace(probeSkill, "output: note", "more: [look, plan, restart]\noutput: note", 1)
	env, st := skillBox(t, "propose", map[string]string{"weit": wide, "gierig": strings.Replace(wide, "[look, plan, restart]", "[look, approve]", 1)}, nil)
	sks := mcp.SkillsFor(env, st, "ops")
	if sk := findSkill(sks, "weit"); sk == nil || sk.Status != "active" || !slices.Contains(sk.Granted, "look") || !slices.Contains(sk.Granted, "plan") || slices.Contains(sk.Granted, "restart") {
		t.Fatalf("%+v", sk)
	}
	if sk := findSkill(sks, "gierig"); sk == nil || sk.Status != "refused" || !strings.Contains(sk.Refused, "approve") {
		t.Fatalf("%+v", sk)
	}
	if why := mcp.SkillGate(env, st, "ops", "weit", "plan"); why != "" {
		t.Fatalf("plan: %s", why)
	}
}
