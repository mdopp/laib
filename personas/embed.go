// Package personas ships the built-in personas inside the binary (ADR 0020):
// personas/<name>/ holds persona.yml, style.md, icon.svg and icon.png. A
// family's own persona lives in the state repo or a registry under
// personas/<name>/.
package personas

import "embed"

// FS holds every built-in persona folder.
//
//go:embed joshua
var FS embed.FS
