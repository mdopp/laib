// Command laib is the one binary: CLI and daemon. The verb table below is the
// single source for `laib help`, the MCP tool list and the tier check.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/disk"
)

// Version is set by the release build.
var Version = "dev"

// verb is one CLI command. tier is the policy tier it needs (docs/STATE.md).
type verb struct {
	name string
	tier string
	help string
	run  func(ctx context.Context, env *cliEnv, args []string) int
}

// cliEnv is what every verb gets (internal/cli).
type cliEnv = cli.Env

// verbs is filled in init so the MCP handler (reached through cmdServe) may
// read it without an initialization cycle.
var verbs []verb

func init() {
	verbs = []verb{
		{"status", "read", "one line per service; first line is the readiness state", cmdStatus},
		{"diag", "read", "findings with severity and a fix verb each: units, health, drift, disk", diag.Cmd},
		{"plan", "read", "render the state and diff it against the box", cmdPlan},
		{"apply", "apply", "snapshot, write, reload, restart, verify", cmdApply},
		{"inspect", "read", "<service>: units, containers, data files, health, last logs — the first minute of any diagnosis", cmdInspect},
		{"read", "read", "<service> <path>: a file in the service's data paths, secrets redacted", cmdRead},
		{"write", "apply", "<service> <path> (stdin): a file in the service's data paths, inside the envelope", cmdWrite},
		{"look", "read", "<service> [--container c] -- <cat|ls|grep|ps|…>: read-only, in the service's own container, unbudgeted", cmdLook},
		{"exec", "apply", "<service> [--container c] -- <cmd>: run inside the service's own container, budgeted, snapshot first, output journaled", cmdExec},
		{"restart", "apply", "<service>: restart its units and verify, inside the envelope", cmdRestart},
		{"escalate", "propose", "<service> \"<was ich weiß>\": context bundle + red journal line for the next hand", cmdEscalate},
		{"note", "propose", "[--kind report|incident|watch] \"<text>\": what a hand concluded; report is the morning report", cmdNote},
		{"wish", "propose", "\"<was das Haus braucht>\": the intake — a GitHub issue with the wunsch label (box.yml intake), kept on the box without one", cmdWish},
		{"updates", "read", "newer patch and minor releases of every pinned image, from the registry (#287)", cmdUpdates},
		{"skill", "read", "list | enable <name> | pause <name> (humans only): the Operator's skills, active or paused (ADR 0023)", cmdSkill},
		{"operator", "apply", "enable [--model alias] [--report-at 07:00] | disable | status (humans only)", cmdOperator},
		{"logs", "read", "journald for every unit of a service, secrets redacted (--since 1h)", cmdLogs},
		{"rollback", "apply", "revert the last (or a given) state commit and apply", cmdRollback},
		{"backup", "propose", "restic snapshot of state, journal and declared data paths", cmdBackup},
		{"restore", "apply", "<snapshot|latest> [service] restore declared paths, then apply", cmdRestore},
		{"converge", "apply", "bring the box back to the state: re-apply drift, restart inactive units, within a budget", cmdConverge},
		{"journal", "read", "show journal entries (--since 24h)", cmdJournal},
		{"context", "read", "starter bundle for an agent session", cmdContext},
		{"template", "read", "lint <dir> | list | sync | update <service>", cmdTemplate},
		{"add", "apply", "<template> [--name n] [KEY=VALUE…]: install a template — propose, approve and apply as the human", cmdAdd},
		{"remove", "apply", "<service>: stop its units, take it out of the state, apply; data stays (humans only)", cmdRemove},
		{"inbox", "read", "open proposals (--all for decided ones too)", cmdInbox},
		{"propose", "propose", "commit the working tree to a proposal branch and an inbox entry", cmdPropose},
		{"approve", "apply", "merge a proposal and apply it (never your own)", cmdApprove},
		{"reject", "apply", "drop a proposal", cmdReject},
		{"adopt", "propose", "adopt an unmanaged Quadlet folder into services/", cmdAdopt},
		{"ignore", "propose", "record an unmanaged unit as deliberately unmanaged", cmdIgnore},
		{"token", "apply", "mint <hand> [--ttl 8h] | list | revoke <id> (humans only)", cmdToken},
		{"secret", "apply", "set <NAME> (value on stdin) | list | rotate <NAME>", cmdSecret},
		{"disk", "apply", "status | add <device>: the data RAID; add joins the mirror (humans only)", disk.Cmd},
		{"init", "apply", "first setup: key, state repo skeleton", cmdInit},
		{"serve", "read", "daemon: /ready, the three pages", cmdServe},
		{"version", "read", "print the version", cmdVersion},
		{"self", "apply", "rollback: the previous binaries back and the units restarted (humans only)", cmdSelf},
		{"family-card", "read", "print the one-page card for the family: what the box does, what to do when it is down, whom to call", cmdFamilyCard},
		{"recovery-sheet", "destroy", "print the one page that restores the box (age key, restic repo and password, four steps); --confirm once it is stored (humans at the console only)", cmdRecoverySheet},
		{"pause", "apply", "<service> [--until 2h] [\"<grund>\"]: converge leaves the service alone for a while", cmdPause},
		{"resume", "apply", "<service>: lift a pause", cmdResume},
	}
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	env := &cliEnv{Paths: boxfs.Default(), Actor: os.Getenv("LAIB_ACTOR")}
	if env.Actor == "" {
		env.Actor = "human:" + os.Getenv("USER")
	}
	var rest []string
	for _, a := range args {
		if a == "--json" {
			env.JSON = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help" {
		usage()
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP) // an SSH drop ends an apply cleanly, the flock goes with the process
	defer stop()
	for _, v := range verbs {
		if v.name == rest[0] {
			return v.run(ctx, env, rest[1:])
		}
	}
	fmt.Fprintf(env.Errw(), "laib: unknown verb %q\n\n", rest[0])
	usage()
	return 1
}

func usage() {
	fmt.Fprintln(os.Stderr, "laib — the control plane for one home box\n\nVerbs (tier in brackets):")
	sorted := slices.Clone(verbs)
	slices.SortFunc(sorted, func(a, b verb) int { return strings.Compare(a.name, b.name) })
	for _, v := range sorted {
		fmt.Fprintf(os.Stderr, "  %-10s [%s]  %s\n", v.name, v.tier, v.help)
	}
	fmt.Fprintln(os.Stderr, "\nFlags: --json on every verb. Env: LAIB_HOME, LAIB_STATE, LAIB_QUADLET, LAIB_ACTOR.")
}

func cmdVersion(_ context.Context, env *cliEnv, _ []string) int {
	if env.JSON {
		return cli.Emit(env, map[string]string{"version": Version})
	}
	fmt.Fprintln(env.W(), "laib", Version)
	return 0
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "laib:", strings.TrimSpace(err.Error()))
	return 1
}
