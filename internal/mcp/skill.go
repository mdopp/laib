package mcp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/skill"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/skills"
)

// The Operator's skills (ADR 0023): the kernel holds the registry and
// decides what a skill may call; the Operator runs them.

// SkillsFor is the registry as hand sees it: a tool is granted when it is
// an MCP verb and the hand's ceiling and verb list reach it.
func SkillsFor(env *cli.Env, st *state.State, hand string) []skill.Skill {
	return skill.Grant(skill.Load(skills.FS, env.Paths.State), st.Policy.Skills, func(v string) string {
		tier := ""
		for _, x := range Verbs {
			if x.Name == v {
				tier = x.Tier
			}
		}
		switch {
		case !Allowed[v]:
			return "kein MCP-Werkzeug"
		case !policy.Allowed(st.Policy, hand, tier):
			return fmt.Sprintf("Stufe %s über der Decke %s von %s", tier, st.Policy.Hands[hand].Ceiling, hand)
		case !policy.MayVerb(st.Policy, hand, v):
			return "policy.yml beschränkt " + hand + " auf " + strings.Join(st.Policy.Hands[hand].Verbs, ", ")
		}
		return ""
	})
}

// SkillGate says why a call made in a skill's name (X-Laib-Skill) may not
// run, or "": an unknown, refused or paused skill runs nothing, a running
// one only its granted tools.
func SkillGate(env *cli.Env, st *state.State, hand, name, verb string) string {
	return gate(SkillsFor(env, st, hand), name, verb)
}

func gate(sks []skill.Skill, name, verb string) string {
	sk := skill.Find(sks, name)
	switch {
	case sk == nil:
		return fmt.Sprintf("die Fähigkeit %q gibt es nicht", name)
	case sk.Status == "refused":
		return "die Fähigkeit " + name + " ist verweigert: " + sk.Refused
	case sk.Status == "paused":
		return "die Fähigkeit " + name + " ruht (policy.yml skills)"
	}
	for _, g := range sk.Granted {
		if g == verb {
			return ""
		}
	}
	if why := sk.Denied[verb]; why != "" {
		return verb + " ist der Fähigkeit " + name + " nicht gewährt: " + why
	}
	return verb + " gehört nicht zur Fähigkeit " + name
}

// SkillTools narrows a tools/list to what the skill may call.
func SkillTools(env *cli.Env, st *state.State, hand, name string, tools []map[string]any) []map[string]any {
	sks := SkillsFor(env, st, hand)
	var out []map[string]any
	for _, t := range tools {
		if gate(sks, name, strings.TrimPrefix(t["name"].(string), "laib_")) == "" {
			out = append(out, t)
		}
	}
	return out
}

// Skill: `laib skill list | enable <name> | pause <name>`. Listing is
// for everyone (the Operator reads it over MCP); the switch is a human's
// and a commit to policy.yml.
func Skill(ctx context.Context, env *cli.Env, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	sks := SkillsFor(env, st, "ops")
	if len(args) == 0 || args[0] == "list" {
		if env.JSON {
			return cli.Emit(env, sks)
		}
		word := map[string]string{"active": "aktiv", "paused": "ruht", "refused": "verweigert"}
		for _, s := range sks {
			fmt.Fprintf(env.W(), "%-10s %-14s %s\n", word[s.Status], s.Name, s.Description)
			if s.Refused != "" {
				fmt.Fprintf(env.W(), "           %s\n", s.Refused)
			}
			for tool, why := range s.Denied {
				fmt.Fprintf(env.W(), "           ohne %s: %s\n", tool, why)
			}
		}
		return cli.ExitOK
	}
	if len(args) != 2 || (args[0] != "enable" && args[0] != "pause") {
		return env.Fail(errors.New("usage: laib skill list | enable <name> | pause <name>"))
	}
	if proposal.Hand(env.Actor) != "human" {
		return cli.Refused(env, "only a human enables or pauses a skill")
	}
	if skill.Find(sks, args[1]) == nil {
		return env.Fail(fmt.Errorf("no skill %q (laib skill list)", args[1]))
	}
	value := map[string]string{"enable": "active", "pause": "paused"}[args[0]]
	if err := state.SetBlock(filepath.Join(env.Paths.State, "policy.yml"), "skills", args[1], value); err != nil {
		return env.Fail(err)
	}
	if err := proposal.CommitDirty(ctx, env.Paths.State, env.Actor); err != nil {
		return env.Fail(err)
	}
	journal.Append(env.Paths.Journal, journal.Entry{Actor: env.Actor, Verb: "skill", Result: "info", Detail: args[0] + " " + args[1]})
	fmt.Fprintf(env.W(), "%s: %s\n", args[1], map[string]string{"active": "aktiv", "paused": "ruht"}[value])
	return cli.ExitOK
}
