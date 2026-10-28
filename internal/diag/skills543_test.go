package diag

import (
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/skill"
	"github.com/mdopp/laib/skills"
)

// #543: the Kraftwerk's policy.yml gives ops a verb list without backup,
// wish and restart. laib skill list showed "denied"; diag said nothing, and
// no restore test ran for 35 days. Now each built-in skill kept from its
// own tools is one yellow for the owner, naming the choice; a capable
// model's more: tools and a paused skill are not.
func TestDiagSaysWhenThePolicyKeepsASkillFromItsJob(t *testing.T) {
	verbs := []string{"status", "diag", "journal", "logs", "inspect", "note", "propose", "escalate", "repair", "updates", "inbox"}
	reach := func(v string) string {
		for _, x := range verbs {
			if x == v {
				return ""
			}
		}
		return "policy.yml beschränkt ops auf " + strings.Join(verbs, ", ")
	}
	sks := skill.Grant(skill.Load(skills.FS, t.TempDir()), map[string]string{"nutzung": "paused"}, reach)
	got := map[string]Finding{}
	for _, f := range skillFindings(sks) {
		name := strings.Fields(f.Detail)[1]
		got[name] = f
		if f.Severity != "gelb" || f.Probe != "skill" || f.Repair != nil {
			t.Errorf("an owner's item, yellow, without a repair: %+v", f)
		}
	}
	s, ok := got["sicherung"]
	if !ok || !strings.Contains(s.Detail, "ohne backup") || !strings.Contains(s.Detail, "probeweise wiederherstellen") || !strings.Contains(s.Detail, "policy.yml beschränkt ops") {
		t.Fatalf("sicherung without backup: %+v", got)
	}
	// the owner's command, not a hand edit of policy.yml (#575); diag grants nothing itself
	for _, want := range []string{`laib propose --grant ops backup "Fähigkeit sicherung braucht backup"`, "laib skill pause sicherung", "skills/sicherung/skill.yml"} {
		if !strings.Contains(s.Fix, want) {
			t.Errorf("the fix lacks the choice %q: %s", want, s.Fix)
		}
	}
	if v := got["vorfall"]; !strings.Contains(v.Detail, "ohne restart") || strings.Contains(v.Detail, "look") {
		t.Errorf("vorfall: restart is its job, look only a capable model's extra: %+v", v)
	}
	if v := got["vorfall"]; !strings.Contains(v.Fix, "laib propose --grant ops restart") || strings.Contains(v.Fix, "policy.yml") {
		t.Errorf("vorfall's fix names the grant command, not policy.yml: %s", v.Fix)
	}
	if _, ok := got["nutzung"]; ok {
		t.Error("a paused skill is the owner's answer already")
	}
	if _, ok := got["routineblick"]; ok {
		t.Error("routineblick has all it needs")
	}
	// a box whose policy reaches everything: nothing to say
	if f := skillFindings(skill.Grant(skill.Load(skills.FS, t.TempDir()), nil, func(string) string { return "" })); len(f) != 0 {
		t.Fatalf("an unrestricted ops hand got findings: %+v", f)
	}
}

// #543: a look that found nothing new writes no note; the Operator's
// /ready says when it last looked, and that keeps the heartbeat.
func TestALookWithoutANoteIsAHeartbeat(t *testing.T) {
	now := time.Now()
	f := OpFacts{Enabled: true, UnitActive: true, Answers: true, LastNote: now.Add(-5 * time.Hour), Looked: now.Add(-3 * time.Minute), Now: now, Tick: 10 * time.Minute}
	if state, finding := OperatorState(f); state != "läuft" || finding != nil {
		t.Fatalf("a fresh look is a heartbeat: %s %+v", state, finding)
	}
	f.Looked = now.Add(-45 * time.Minute)
	if state, finding := OperatorState(f); state != "stumm" || finding == nil || !strings.Contains(finding.Detail, "letzter Blick") {
		t.Fatalf("an old look is silence: %s %+v", state, finding)
	}
}

// #858: a slot that waits for the graphics card writes one line, an ops
// note; that note is no heartbeat. While the Operator says when it last did
// work (/ready's looked), that is the heartbeat; a note counts only when it
// cannot say, right after a restart.
func TestANoteIsNoHeartbeatWhileTheOperatorSaysWhenItWorked(t *testing.T) {
	now := time.Now()
	f := OpFacts{Enabled: true, UnitActive: true, Answers: true, LastNote: now.Add(-2 * time.Minute), Looked: now.Add(-45 * time.Minute), Now: now, Tick: 10 * time.Minute}
	if state, finding := OperatorState(f); state != "stumm" || finding == nil {
		t.Fatalf("a skip line hid an Operator that did no work for 45 minutes: %s %+v", state, finding)
	}
	f.Looked = time.Time{}
	if state, _ := OperatorState(f); state != "läuft" {
		t.Fatalf("without /ready's looked a fresh note is the heartbeat: %s", state)
	}
}
