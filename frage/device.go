package frage

import (
	"slices"
	"strings"
)

// A device command reaches Frage only when HA's intents missed it (#760:
// „Schalte Sofa-Licht aus“, the light is „Sofalicht“). Frage never switches
// (ADR 0015) and a search would shrug: it says what it did not find and names
// the nearest of the devices in the prompt's line „Geräte: a; b“ (post-deploy).
const (
	verbs     = " schalte schalt mach mache stell stelle dreh drehe dimm dimme öffne schließe fahr fahre aktiviere deaktiviere "
	particles = " an aus ein ab auf zu hoch runter "
	questions = " was wer wie wo wann warum wieso welche welcher welches ist sind gibt hast kannst "
	fillers   = " bitte mal doch den die das der dem des einen eine mein meine in im am "
)

func in(list, w string) bool { return strings.Contains(list, " "+strings.ToLower(w)+" ") }

// key: „Sofa-Licht“, „Sofa Licht“ and „Sofalicht“ are one
func key(s string) string { return strings.NewReplacer(" ", "", "-", "").Replace(strings.ToLower(s)) }

// device is the answer to a missed device command; ok is false for anything else.
func device(q, system string) (answer string, ok bool) {
	ws := strings.FieldsFunc(q, func(r rune) bool { return strings.ContainsRune(" .,!?;:„“\"", r) })
	if len(ws) < 2 || !in(verbs, ws[0]) && (!in(particles, ws[len(ws)-1]) || in(questions, ws[0])) {
		return "", false
	}
	obj := slices.DeleteFunc(slices.Clone(ws), func(w string) bool { return in(fillers, w) || in(verbs, w) || in(particles, w) })
	heard, near := strings.Join(obj, " "), []string{}
	if heard == "" {
		return "Welches Gerät meinst du?", true
	}
	_, list, found := strings.Cut(system, "\nGeräte:")
	list, _, _ = strings.Cut(list, "\n")
	for _, n := range strings.Split(list, ";") {
		// every word of the name in what was heard, or one letter apart (Whisper's „Sofa-Dicht“)
		if n = strings.TrimSpace(n); found && n != "" && len(near) < 2 && (contains(key(heard), n) || oneOff(key(heard), key(n))) {
			near = append(near, "„"+n+"“")
		}
	}
	if len(near) == 0 {
		return "„" + heard + "“ habe ich nicht gefunden: welches Gerät meinst du?", true
	}
	return "„" + heard + "“ kenne ich nicht. Meinst du " + strings.Join(near, " oder ") + "?", true
}

func contains(heard, name string) bool {
	return !slices.ContainsFunc(strings.Fields(name), func(w string) bool { return !strings.Contains(heard, key(w)) })
}

// oneOff: the same length, one letter apart
func oneOff(a, b string) bool {
	x, y, diff := []rune(a), []rune(b), 0
	for i := 0; len(x) == len(y) && i < len(x); i++ {
		if x[i] != y[i] {
			diff++
		}
	}
	return len(x) == len(y) && len(x) > 3 && diff == 1
}
