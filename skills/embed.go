// Package skills ships the Operator's built-in skills inside the laib
// binary (ADR 0023): data the kernel checks and lists, as templates/ holds
// the built-in templates. A file skills/<name>.yml in the state repo adds
// a skill or replaces a built-in one.
package skills

import "embed"

// FS holds every built-in skill file.
//
//go:embed *.yml
var FS embed.FS
