// Package skills ships the Operator's built-in skills inside the laib
// binary (ADR 0023): data the kernel checks and lists, as templates/ holds
// the built-in templates. A skill is a folder (#839): <name>/skill.yml,
// and for a built-in one, where it helps, <name>/run.py, the deterministic
// steps the Operator runs in its container (laibskill.py is their one door
// to the box). A folder skills/<name>/ in the state repo adds a skill or
// replaces a built-in one, without a script.
package skills

import "embed"

// FS holds every built-in skill's folder: its skill.yml and its run.py.
//
//go:embed */skill.yml */run.py
var FS embed.FS
