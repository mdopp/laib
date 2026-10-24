package main

import (
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/registry"
)

// #627, after #571: systemd splits an unquoted Environment= value at
// whitespace. The house persona's "Hey Joshua" reached the wake-word
// trainer as WAKE_PHRASE=Hey; an AirScan device's name lost its tail the
// same way. Lint renders with the built-in persona, whose phrase has a
// space, and with values.example.yml, so the rendered line shows it.
func TestLintRefusesAnUnquotedEnvironmentValueWithASpace(t *testing.T) {
	root := t.TempDir()
	c := "name: t\ntemplate: {registry: builtin, name: t, version: 1}\nroutes: []\nsecrets: []\nhealth: {none: test}\n"
	for why, unit := range map[string]string{
		"the persona's phrase": "Environment=WAKE_PHRASE=${PERSONA_PHRASE}\n",
		"a values entry":       "Environment=SCANNER=${SCANNER}\n",
		"a literal":            "Environment=GREETING=guten Tag\n",
	} {
		dir := writeTemplate(t, root, "t", c, map[string]string{"t.container": "[Container]\nImage=img\n" + unit, "values.example.yml": "SCANNER: airscan:e0:Canon TS5300 series\n"})
		if registry.LintTemplate(&cliEnv{}, dir) == cli.ExitOK {
			t.Errorf("%s unquoted passed lint", why)
		}
	}
	dir := writeTemplate(t, root, "t", c, map[string]string{"t.container": "[Container]\nImage=img\nEnvironment=\"WAKE_PHRASE=${PERSONA_PHRASE}\"\nEnvironment=\"SCANNER=${SCANNER}\"\nEnvironment=TZ=${TZ}\n", "values.example.yml": "SCANNER: airscan:e0:Canon TS5300 series\n"})
	if registry.LintTemplate(&cliEnv{}, dir) != cli.ExitOK {
		t.Fatal("quoted values with spaces, and an unquoted one without, are fine")
	}
}
