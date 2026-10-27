// Package mcp is the agents' surface (#309, ADR 0023 §4): MCP over
// streamable HTTP, the tool list per hand (the verb table filtered by the
// hand's ceiling and the rows marked MCP), typed schemas, tools/call, and the
// skill gate (X-Laib-Skill narrows, the hand's ceiling caps). Trust kernel.
package mcp

import "github.com/mdopp/laib/internal/cli"

// Verbs is the verb table: the single source for `laib help`, the MCP tool
// list and the tier check. cmd/laib fills it in init.
var Verbs []cli.Verb
