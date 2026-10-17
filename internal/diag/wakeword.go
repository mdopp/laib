package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WakewordState reads what Stimme's trainer wrote into its output folder
// (stimme/trainer/trainer.py): the model, or <id>.status.json. It says in
// plain German where the house persona's wake word stands; a failed
// training is a yellow finding with the way out (#169).
func WakewordState(dataDir, id, phrase string) (string, *Finding) {
	micro := filepath.Join(dataDir, "stimme", "micro")
	_, errModel := os.Stat(filepath.Join(micro, id+".tflite"))
	_, errMeta := os.Stat(filepath.Join(micro, id+".json"))
	if errModel == nil && errMeta == nil {
		return "Wake-Word „" + phrase + "“ trainiert", nil
	}
	var s struct {
		State, Phrase, Reason, At string
	}
	b, err := os.ReadFile(filepath.Join(micro, id+".status.json"))
	if err != nil || json.Unmarshal(b, &s) != nil {
		return "Wake-Word „" + phrase + "“ noch nicht trainiert", nil
	}
	switch s.State {
	case "training":
		return "Wake-Word „" + phrase + "“ wird trainiert (seit " + s.At + ")", nil
	case "failed":
		d := "Wake-Word „" + phrase + "“: Training fehlgeschlagen: " + s.Reason
		return d, &Finding{Severity: "gelb", Probe: "wakeword", Service: "stimme", Detail: d,
			Fix: "erneut versucht wird erst bei einem neuen Satz oder einer neuen Revision der persona (box.yml persona)"}
	}
	return "Wake-Word „" + phrase + "“ noch nicht trainiert", nil
}
