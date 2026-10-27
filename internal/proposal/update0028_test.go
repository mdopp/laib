package proposal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR 0028: the contract helper's values moved from paperless to scan. A box's scan values.yml
// from before (the old example as laib add wrote it) gets them from the new example on the
// template update, with the values the box had in paperless, so no hand edit is needed; its own
// values stay, the image moves by its new key.
func TestScanUpdateCarriesTheHelpersValues(t *testing.T) {
	old := `SCAN_IMAGE: ghcr.io/mdopp/laib-scan:latest
SCANNER: fujitsu
SCAN_DPI: "300"
SCAN_MODE: Gray
SCAN_AREA: 210x297
SCAN_DUPLEX: "true"
SCAN_BACK_FIRST: "false"
SCAN_TAG: Stapel
SCAN_NOTIFY: ""
SCAN_KEEP_DAYS: "14"
MODEL_NAME: qwen3.6-35b-a3b
NOTIFY_TOKEN: !secret BOTE_NOTIFY_TOKEN
`
	ex, err := os.ReadFile("../../templates/scan/values.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "values.yml")
	os.WriteFile(p, []byte(old), 0o644)
	declared := map[string]bool{"BOTE_NOTIFY_TOKEN": true, "PAPERLESS_TOKEN": true, "DOKUMENTE_WEBHOOK_TOKEN": true, "HAUS_HAUSHALT_TOKEN": true}
	if err := newValues(p, ex, declared); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	for _, want := range []string{"\nSCANNER: fujitsu\n", "\nSCAN_BACK_FIRST: \"false\"\n", "\nDOKUMENTE_IMAGE: ghcr.io/mdopp/dokumente:latest",
		"\nVERTRAG_ARTEN: Vertrag, Versicherung, Bescheid\n", "\nVERTRAG_VORLAUF_TAGE: \"28\"\n", "\nRECHNUNG_ARTEN: Rechnung ",
		"\nHAUSHALT_NAMEN: \"\" ", "\nPAPERLESS_TOKEN: !secret PAPERLESS_TOKEN ", "\nDOKUMENTE_WEBHOOK_TOKEN: !secret DOKUMENTE_WEBHOOK_TOKEN ",
		"\nHAUSHALT_TOKEN: !secret HAUS_HAUSHALT_TOKEN\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("scan's values.yml after the update lacks %q:\n%s", want, b)
		}
	}
}
