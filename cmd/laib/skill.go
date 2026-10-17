package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/skill"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/skills"
)

// The Operator's skills (ADR 0023): the kernel holds the registry and
// decides what a skill may call; the Operator runs them.

// skillsFor is the registry as hand sees it: a tool is granted when it is
// an MCP verb and the hand's ceiling and verb list reach it.
func skillsFor(env *cliEnv, st *state.State, hand string) []skill.Skill {
	return skill.Grant(skill.Load(skills.FS, env.paths.State), st.Policy.Skills, func(v string) string {
		tier := ""
		for _, x := range verbs {
			if x.name == v {
				tier = x.tier
			}
		}
		switch {
		case !mcpVerbs[v]:
			return "kein MCP-Werkzeug"
		case !policy.Allowed(st.Policy, hand, tier):
			return fmt.Sprintf("Stufe %s über der Decke %s von %s", tier, st.Policy.Hands[hand].Ceiling, hand)
		case !policy.MayVerb(st.Policy, hand, v):
			return "policy.yml beschränkt " + hand + " auf " + strings.Join(st.Policy.Hands[hand].Verbs, ", ")
		}
		return ""
	})
}

// skillGate says why a call made in a skill's name (X-Laib-Skill) may not
// run, or "": an unknown, refused or paused skill runs nothing, a running
// one only its granted tools.
func skillGate(env *cliEnv, st *state.State, hand, name, verb string) string {
	return gate(skillsFor(env, st, hand), name, verb)
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

// skillTools narrows a tools/list to what the skill may call.
func skillTools(env *cliEnv, st *state.State, hand, name string, tools []map[string]any) []map[string]any {
	sks := skillsFor(env, st, hand)
	var out []map[string]any
	for _, t := range tools {
		if gate(sks, name, strings.TrimPrefix(t["name"].(string), "laib_")) == "" {
			out = append(out, t)
		}
	}
	return out
}

// cmdSkill: `laib skill list | enable <name> | pause <name>`. Listing is
// for everyone (the Operator reads it over MCP); the switch is a human's
// and a commit to policy.yml.
func cmdSkill(ctx context.Context, env *cliEnv, args []string) int {
	st, err := loadState(env)
	if err != nil {
		return env.fail(err)
	}
	sks := skillsFor(env, st, "ops")
	if len(args) == 0 || args[0] == "list" {
		if env.json {
			return emit(env, sks)
		}
		word := map[string]string{"active": "aktiv", "paused": "ruht", "refused": "verweigert"}
		for _, s := range sks {
			fmt.Fprintf(env.w(), "%-10s %-14s %s\n", word[s.Status], s.Name, s.Description)
			if s.Refused != "" {
				fmt.Fprintf(env.w(), "           %s\n", s.Refused)
			}
			for tool, why := range s.Denied {
				fmt.Fprintf(env.w(), "           ohne %s: %s\n", tool, why)
			}
		}
		return exitOK
	}
	if len(args) != 2 || (args[0] != "enable" && args[0] != "pause") {
		return env.fail(errors.New("usage: laib skill list | enable <name> | pause <name>"))
	}
	if proposal.Hand(env.actor) != "human" {
		return exitRefusedMsg(env, "only a human enables or pauses a skill")
	}
	if skill.Find(sks, args[1]) == nil {
		return env.fail(fmt.Errorf("no skill %q (laib skill list)", args[1]))
	}
	value := map[string]string{"enable": "active", "pause": "paused"}[args[0]]
	if err := setBox(filepath.Join(env.paths.State, "policy.yml"), "skills", args[1], value); err != nil {
		return env.fail(err)
	}
	if err := proposal.CommitDirty(ctx, env.paths.State, env.actor); err != nil {
		return env.fail(err)
	}
	journal.Append(env.paths.Journal, journal.Entry{Actor: env.actor, Verb: "skill", Result: "info", Detail: args[0] + " " + args[1]})
	fmt.Fprintf(env.w(), "%s: %s\n", args[1], map[string]string{"active": "aktiv", "paused": "ruht"}[value])
	return exitOK
}
