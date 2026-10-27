package render

import (
	"regexp"
	"strings"
	"testing"
)

// #828: Haus records the family saying the wake word into
// stimme/recordings/<wake id>/haus, read-write at the one path it reads
// from HAUS_WAKE_RECORDINGS (haus#165, no default); the folder exists
// before Haus starts, 0700, and out of the backups (CACHEDIR.TAG) like the
// false wakes beside it. Haus reads the phrase and the id from the persona's
// persona.json. Stimme's Whisper container, where stimme-names counts them,
// sees the recordings read-only, and stimme-retrain moves the model aside.
func TestHausRecordsTheWakeWordIntoStimmesFolder(t *testing.T) {
	st := shippedBox(t)
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	haus := out["quadlet/haus/haus.container"]
	for _, want := range []string{
		"\nEnvironment=HAUS_WAKE_RECORDINGS=/stimme-aufnahmen\n",
		"\nVolume=/d/stimme/recordings/hey_joshua/haus:/stimme-aufnahmen\n", // read-write; no relabel: SecurityLabelDisable
		"\nVolume=/mnt/data/laib/render/persona:/persona:ro,z\n",
		"\nEnvironment=HAUS_PERSONA=joshua\n", // persona.json is /persona/joshua/persona.json
		"\nExecStartPre=/usr/bin/podman unshare mkdir -p -m 0700 /d/stimme/recordings/hey_joshua/haus\n",
	} {
		if !strings.Contains(haus, want) {
			t.Errorf("haus.container lacks %q", strings.TrimSpace(want))
		}
	}
	if !regexp.MustCompile(`(?m)^ExecStartPre=/usr/bin/podman unshare sh -c 'echo "Signature: 8a477f597d28d172789f06886806bc55" > /d/stimme/recordings/hey_joshua/haus/CACHEDIR.TAG'$`).MatchString(haus) {
		t.Error("haus.container does not keep the recordings out of the backups (CACHEDIR.TAG)")
	}
	if !strings.Contains(out["quadlet/haus/haus.container"], "\nSecurityLabelDisable=true\n") {
		t.Error("haus mounts stimme's folder without a relabel only because its labels are off")
	}
	if w := out["quadlet/stimme/stimme-whisper.container"]; !strings.Contains(w, "\nVolume=/d/stimme/recordings:/recordings:ro\n") {
		t.Error("stimme-names counts the recordings in Whisper's container: it needs them read-only")
	}
	r := out["systemd/stimme-retrain.service"]
	for _, want := range []string{"mkdir -p vorher", "hey_joshua.tflite hey_joshua.json hey_joshua.geflasht.json", "try-restart stimme-wakeword-trainer.service"} {
		if !strings.Contains(r, want) {
			t.Errorf("stimme-retrain.service lacks %q:\n%s", want, r)
		}
	}
	if strings.Contains(r, "[Install]") {
		t.Error("stimme-retrain.service is started by laib retrain only, never by apply")
	}
}
