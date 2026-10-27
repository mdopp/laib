// Package skill is the registry of the Operator's skills (ADR 0023): the
// built-in folders and the state repo's skills/, each checked against the
// skill vocabulary and granted only the tools the hand's ceiling reaches.
// The Operator runs skills; the kernel decides what they may call.
package skill

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Trigger is when a skill runs: exactly one of the four.
type Trigger struct {
	Every  string `yaml:"every" json:"every,omitempty"`   // "10m", "${TICK}"
	Daily  string `yaml:"daily" json:"daily,omitempty"`   // "07:00", "${REPORT_AT}"
	Weekly string `yaml:"weekly" json:"weekly,omitempty"` // "Mo 08:00"
	Event  string `yaml:"event" json:"event,omitempty"`   // "red"
}

// Skill is one file plus the kernel's verdict on it.
type Skill struct {
	Name        string   `yaml:"-" json:"name"`
	Source      string   `yaml:"-" json:"source"` // builtin | state
	Description string   `yaml:"description" json:"description"`
	Trigger     Trigger  `yaml:"trigger" json:"trigger"`
	Tools       []string `yaml:"tools" json:"tools"`
	More        []string `yaml:"more" json:"more,omitempty"` // tools only a capable model gets (#286), checked like tools
	Context     string   `yaml:"context" json:"context"`     // box | usage | none
	Output      string   `yaml:"output" json:"output"`       // note | report | propose
	Prompt      string   `yaml:"prompt" json:"prompt"`
	// The mechanics the Operator's runner enforces, never prompt prose
	// (#376, CLAUDE.md rule 1): a cap per tool and session, and for an
	// updates skill the one level it may propose (patch); the rest of the
	// updates go into its note for a human.
	Limits  map[string]int `yaml:"limits" json:"limits,omitempty"`
	Updates string         `yaml:"updates" json:"updates,omitempty"`
	// Lessons: vorfall — the runner proposes a lesson for each incident
	// session whose proposal made its finding go away (#544), no model
	Lessons string `yaml:"lessons" json:"lessons,omitempty"`
	// Run: run.py — the skill's own script (#839), the deterministic steps
	// before the model; only a built-in skill has one, shipped in the
	// Operator's image, and it calls nothing the skill is not granted
	Run string `yaml:"run" json:"run,omitempty"`
	// Modell: where the model decides (#839, owner: „die
	// Problemlösungskompetenzen eines LLMs behalten“) — frei, the default:
	// the agentic loop with every granted tool, as before scripts; urteil:
	// one judgement on what the script prepared, with only the tools that
	// write it; nein: the script alone, no model
	Modell string `yaml:"modell" json:"modell"`

	Status  string            `yaml:"-" json:"status"` // active | paused | refused
	Granted []string          `yaml:"-" json:"granted"`
	Denied  map[string]string `yaml:"-" json:"denied,omitempty"` // tool → why it is not granted
	Refused string            `yaml:"-" json:"refused,omitempty"`
}

// Vocabulary is every kernel verb a skill may name. approve, rollback,
// restore, remove, token, secret and the rest are not in it: no skill gets
// them, whatever the hand's ceiling. apply is (owner, #286, 2026-10-17):
// for the ops hand the kernel lets it free only its own proposal inside the
// envelope (cmd/laib opsApply).
var Vocabulary = []string{"status", "diag", "logs", "inspect", "look", "read", "journal", "context", "inbox", "plan", "apply", "updates", "backup", "note", "escalate", "propose", "wish", "repair", "restart", "exec", "write"}

var (
	nameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)
	everyRe  = regexp.MustCompile(`^([1-9][0-9]*[mh]|\$\{TICK\})$`)
	dailyRe  = regexp.MustCompile(`^(([01][0-9]|2[0-3]):[0-5][0-9]|\$\{REPORT_AT\})$`)
	weeklyRe = regexp.MustCompile(`^(Mo|Di|Mi|Do|Fr|Sa|So) ([01][0-9]|2[0-3]):[0-5][0-9]$`)
)

// Load reads the built-in skills and the state repo's skills/<name>/skill.yml;
// a state folder with a built-in's name replaces it. A file that does not
// parse is a refused skill, never an error for the box; so is a state
// folder with a script (only the image brings one) and a file the old way,
// skills/<name>.yml, which says where it goes now.
func Load(builtin fs.FS, stateDir string) []Skill {
	byName := map[string]Skill{}
	files, _ := fs.Glob(builtin, "*/skill.yml")
	for _, f := range files {
		name := path.Dir(f)
		b, err := fs.ReadFile(builtin, f)
		s := parse(name, "builtin", b, err)
		if _, err := fs.Stat(builtin, name+"/run.py"); s.Run != "" && err != nil && s.Refused == "" {
			s.Refused = "run: " + s.Run + " fehlt im Ordner der Fähigkeit"
		}
		byName[name] = s
	}
	local, _ := filepath.Glob(filepath.Join(stateDir, "skills", "*", "skill.yml"))
	for _, f := range local {
		b, err := os.ReadFile(f)
		name := filepath.Base(filepath.Dir(f))
		s := parse(name, "state", b, err)
		if _, err := os.Stat(filepath.Join(filepath.Dir(f), "run.py")); (s.Run != "" || err == nil) && s.Refused == "" {
			s.Refused = "ein Skript (run.py) bringt nur eine eingebaute Fähigkeit mit, im Image des Operators (#839)"
		}
		byName[name] = s
	}
	flat, _ := filepath.Glob(filepath.Join(stateDir, "skills", "*.yml"))
	for _, f := range flat {
		name := strings.TrimSuffix(filepath.Base(f), ".yml")
		byName[name] = Skill{Name: name, Source: "state", Refused: "eine Fähigkeit ist ein Ordner: skills/" + name + "/skill.yml statt skills/" + name + ".yml (#839)"}
	}
	out := make([]Skill, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func parse(name, source string, b []byte, err error) Skill {
	s := Skill{Name: name, Source: source}
	if err == nil {
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		err = dec.Decode(&s)
		s.Name, s.Source = name, source
	}
	if err != nil {
		s.Refused = "die Datei ist nicht lesbar: " + err.Error()
	} else {
		s.Refused = check(&s)
	}
	return s
}

// check returns why a skill may not run at all, in German, or "".
func check(s *Skill) string {
	if !nameRe.MatchString(s.Name) {
		return fmt.Sprintf("%q ist kein Name (Kleinbuchstaben, Ziffern, Bindestrich)", s.Name)
	}
	if strings.TrimSpace(s.Description) == "" || strings.TrimSpace(s.Prompt) == "" {
		return "description und prompt sind Pflicht"
	}
	t := s.Trigger
	set := 0
	for _, v := range []string{t.Every, t.Daily, t.Weekly, t.Event} {
		if v != "" {
			set++
		}
	}
	switch {
	case set != 1:
		return "trigger braucht genau eines von every, daily, weekly, event"
	case t.Every != "" && !everyRe.MatchString(t.Every):
		return fmt.Sprintf("trigger every %q ist keine Dauer wie 10m oder 2h", t.Every)
	case t.Daily != "" && !dailyRe.MatchString(t.Daily):
		return fmt.Sprintf("trigger daily %q ist keine Uhrzeit wie 07:00", t.Daily)
	case t.Weekly != "" && !weeklyRe.MatchString(t.Weekly):
		return fmt.Sprintf("trigger weekly %q ist nicht wie \"Mo 08:00\"", t.Weekly)
	case t.Event != "" && t.Event != "red":
		return fmt.Sprintf("trigger event %q: es gibt nur red", t.Event)
	}
	for _, tool := range append(slices.Clone(s.Tools), s.More...) {
		if !slices.Contains(Vocabulary, tool) {
			return fmt.Sprintf("%s gehört nicht zu den Werkzeugen einer Fähigkeit (%s)", tool, strings.Join(Vocabulary, ", "))
		}
	}
	if !slices.Contains(s.Tools, "note") {
		return "tools braucht note: jede Sitzung endet mit einer Notiz"
	}
	for tool, n := range s.Limits {
		if n < 1 || !slices.Contains(append(slices.Clone(s.Tools), s.More...), tool) {
			return fmt.Sprintf("limits %s: %d ist keine Obergrenze für ein Werkzeug der Fähigkeit", tool, n)
		}
	}
	if s.Updates != "" && (s.Updates != "patch" || !slices.Contains(s.Tools, "updates") || !slices.Contains(s.Tools, "propose")) {
		return fmt.Sprintf("updates %q: nur patch, und tools braucht updates und propose", s.Updates)
	}
	if s.Lessons != "" && (s.Lessons != "vorfall" || !slices.Contains(s.Tools, "propose")) {
		return fmt.Sprintf("lessons %q: nur vorfall, und tools braucht propose", s.Lessons)
	}
	if s.Context == "" {
		s.Context = "box"
	}
	if s.Context != "box" && s.Context != "usage" && s.Context != "none" {
		return fmt.Sprintf("context %q: box, usage oder none", s.Context)
	}
	if s.Run != "" && s.Run != "run.py" {
		return fmt.Sprintf("run %q: das Skript einer Fähigkeit heißt run.py", s.Run)
	}
	if s.Modell == "" {
		s.Modell = "frei"
	}
	switch {
	case s.Modell != "frei" && s.Modell != "urteil" && s.Modell != "nein":
		return fmt.Sprintf("modell %q: frei, urteil oder nein", s.Modell)
	case s.Modell != "frei" && s.Run == "":
		return "modell " + s.Modell + " braucht ein Skript (run: run.py), das vorbereitet, was das Modell beurteilt"
	}
	switch s.Output {
	case "note", "report":
	case "propose":
		if !slices.Contains(s.Tools, "propose") && !slices.Contains(s.Tools, "wish") {
			return "output propose braucht propose oder wish in tools"
		}
	default:
		return fmt.Sprintf("output %q: note, report oder propose", s.Output)
	}
	return ""
}

// Grant sets each skill's status from policy.yml's skills switch and its
// granted tools from reach, which says why a verb is out of the hand's
// reach ("" = in reach).
func Grant(sks []Skill, switches map[string]string, reach func(verb string) string) []Skill {
	for i := range sks {
		s := &sks[i]
		s.Granted, s.Denied = nil, nil
		switch {
		case s.Refused != "":
			s.Status = "refused"
			continue
		case switches[s.Name] == "paused":
			s.Status = "paused"
		default:
			s.Status = "active"
		}
		for _, tool := range append(slices.Clone(s.Tools), s.More...) {
			if why := reach(tool); why != "" {
				if s.Denied == nil {
					s.Denied = map[string]string{}
				}
				s.Denied[tool] = why
				continue
			}
			s.Granted = append(s.Granted, tool)
		}
	}
	return sks
}

// Find returns the named skill, or nil.
func Find(sks []Skill, name string) *Skill {
	for i := range sks {
		if sks[i].Name == name {
			return &sks[i]
		}
	}
	return nil
}
