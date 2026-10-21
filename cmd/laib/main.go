// Command laib is the one binary: CLI and daemon. It fills the verb table
// (mcp.Verbs: the single source for `laib help`, the MCP tool list and the
// tier check) and holds the small verbs that only parse and delegate: plan
// and apply, journal, note and context, logs, the state repo's init, adopt
// and ignore, remove, pause, secret, token, operator and self (#309).
// Everything else lives in internal/.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/registry"
)

// Version is set by the release build.
var Version = "dev"

// cliEnv is what every verb gets (internal/cli).
type cliEnv = cli.Env

func init() {
	cli.Version = Version
	mcp.Verbs = []cli.Verb{
		{Name: "status", Tier: "read", Help: "one line per service; first line is the readiness state", Run: pages.Status},
		{Name: "diag", Tier: "read", Help: "findings with severity and a fix verb each: units, health, drift, disk", Run: diag.Cmd},
		{Name: "plan", Tier: "read", Help: "render the state and diff it against the box", Run: cmdPlan},
		{Name: "apply", Tier: "apply", Help: "snapshot, write, reload, restart, verify", Run: cmdApply},
		{Name: "inspect", Tier: "read", Help: "<service>: units, containers, data files, health, last logs — the first minute of any diagnosis", Run: envelope.Inspect},
		{Name: "read", Tier: "read", Help: "<service> <path>: a file in the service's data paths, secrets redacted", Run: envelope.Read},
		{Name: "write", Tier: "apply", Help: "<service> <path> (stdin): a file in the service's data paths, inside the envelope", Run: envelope.Write},
		{Name: "look", Tier: "read", Help: "<service> [--container c] -- <cat|ls|grep|ps|…>: read-only, in the service's own container, unbudgeted", Run: envelope.Look},
		{Name: "exec", Tier: "apply", Help: "<service> [--container c] -- <cmd>: run inside the service's own container, budgeted, snapshot first, output journaled", Run: envelope.Exec},
		{Name: "restart", Tier: "apply", Help: "<service>: restart its units and verify, inside the envelope", Run: envelope.Restart},
		{Name: "start", Tier: "apply", Help: "<service>: start its units (lifting a stop's pause) and verify, inside the envelope", Run: envelope.Start},
		{Name: "stop", Tier: "apply", Help: "<service> [--until 2h] [\"<grund>\"]: stop its units and pause it (at most a day), inside the envelope", Run: envelope.Stop},
		{Name: "backups", Tier: "read", Help: "the backups from the journal: last nightly, last green, off-box copy, restore test, with diag's fixes", Run: diag.Backups},
		{Name: "escalate", Tier: "propose", Help: "<service> \"<was ich weiß>\": context bundle + red journal line for the next hand", Run: envelope.Escalate},
		{Name: "note", Tier: "propose", Help: "[--kind report|incident|watch] \"<text>\": what a hand concluded; report is the morning report", Run: cmdNote},
		{Name: "repair", Tier: "propose", Help: "<probe> [service]: a diag finding's repair as a proposal; it runs once a human approves (#441)", Run: diag.Repair},
		{Name: "wish", Tier: "propose", Help: "\"<was das Haus braucht>\": the intake — a GitHub issue with the wunsch label (box.yml intake), kept on the box without one", Run: inbox.Wish},
		{Name: "net", Tier: "read", Help: "[--mermaid]: every name with its reach, Authelia and target, the direct ports, the host network, the pods' links — from the rendered config (#439)", Run: cmdNet},
		{Name: "updates", Tier: "read", Help: "[--run]: what is behind — a tag's or pin's newer digest, patch and minor releases (#287), a template's extensions and firmware; --run is laib-images.timer's nightly update of the images without data (#528)", Run: registry.Updates},
		{Name: "skill", Tier: "read", Help: "list | enable <name> | pause <name> (humans only): the Operator's skills, active or paused (ADR 0023)", Run: mcp.Skill},
		{Name: "operator", Tier: "apply", Help: "enable [--model alias] [--report-at 07:00] | disable | status (humans only)", Run: cmdOperator},
		{Name: "logs", Tier: "read", Help: "journald for every unit of a service, secrets redacted (--since 1h)", Run: cmdLogs},
		{Name: "rollback", Tier: "apply", Help: "revert the last (or a given) state commit and apply", Run: inbox.Rollback},
		{Name: "backup", Tier: "propose", Help: "restic snapshot of state, journal and declared data paths", Run: backup.Cmd},
		{Name: "restore", Tier: "apply", Help: "<snapshot|latest> [service] restore declared paths, then apply", Run: backup.Restore},
		{Name: "converge", Tier: "apply", Help: "bring the box back to the state: re-apply drift, restart inactive units, within a budget", Run: apply.Converge},
		{Name: "journal", Tier: "read", Help: "show journal entries (--since 24h)", Run: cmdJournal},
		{Name: "context", Tier: "read", Help: "starter bundle for an agent session", Run: cmdContext},
		{Name: "template", Tier: "read", Help: "lint <dir> | list | sync | update <service>", Run: registry.TemplateCmd},
		{Name: "add", Tier: "apply", Help: "<template> [--name n] [KEY=VALUE…]: install a template — propose, approve and apply as the human", Run: inbox.Add},
		{Name: "remove", Tier: "apply", Help: "<service>: stop its units, take it out of the state, apply; data stays (humans only)", Run: cmdRemove},
		{Name: "inbox", Tier: "read", Help: "open proposals (--all for decided ones too)", Run: inbox.List},
		{Name: "propose", Tier: "propose", Help: "commit the working tree to a proposal branch and an inbox entry", Run: inbox.Propose},
		{Name: "approve", Tier: "apply", Help: "merge a proposal and apply it (never your own)", Run: inbox.Approve},
		{Name: "reject", Tier: "apply", Help: "drop a proposal", Run: inbox.Reject},
		{Name: "adopt", Tier: "propose", Help: "adopt an unmanaged Quadlet folder into services/", Run: cmdAdopt},
		{Name: "ignore", Tier: "propose", Help: "record an unmanaged unit as deliberately unmanaged", Run: cmdIgnore},
		{Name: "session", Tier: "read", Help: "[<name>]: a token of the hand policy.yml names in your session:, with its ttl, for one session (#380)", Run: cmdSession},
		{Name: "token", Tier: "apply", Help: "mint <hand> [--ttl 8h] | list | revoke <id> (humans only)", Run: cmdToken},
		{Name: "secret", Tier: "apply", Help: "set <NAME> (value on stdin) | list | rotate <NAME>", Run: cmdSecret},
		{Name: "disk", Tier: "apply", Help: "status | add <device>: the data RAID; add joins the mirror (humans only)", Run: cmdDisk},
		{Name: "init", Tier: "apply", Help: "first setup: key, state repo skeleton", Run: cmdInit},
		{Name: "serve", Tier: "read", Help: "daemon: /ready, the three pages", Run: pages.Serve},
		{Name: "version", Tier: "read", Help: "print the version", Run: cmdVersion},
		{Name: "self", Tier: "apply", Help: "rollback: the previous binaries back and the units restarted (humans only)", Run: cmdSelf},
		{Name: "family-card", Tier: "read", Help: "print the one-page card for the family: what the box does, what to do when it is down, whom to call", Run: pages.FamilyCard},
		{Name: "recovery-sheet", Tier: "destroy", Help: "print the one page that restores the box (age key, restic repo and password, four steps); --confirm once it is stored (humans at the console only)", Run: backup.RecoverySheet},
		{Name: "person_invite", Tier: "apply", Help: "<user> --first F --last L --email E [--group family|admins] [--resend]: a new resident in LLDAP without a password; Authelia mails the set-password link, without SMTP the link comes back (`laib person invite`)", Run: cmdPersonInvite},
		{Name: "person_pending", Tier: "read", Help: "the invited who have not set their password yet (`laib person pending`)", Run: cmdPersonPending},
		{Name: "pause", Tier: "apply", Help: "<service> [--until 2h] [\"<grund>\"]: converge leaves the service alone for a while", Run: cmdPause},
		{Name: "resume", Tier: "apply", Help: "<service>: lift a pause", Run: cmdResume},
	}
	inbox.RunVerb = func(ctx context.Context, env *cli.Env, argv []string) int {
		for _, v := range mcp.Verbs {
			if v.Name == argv[0] {
				return v.Run(ctx, env, argv[1:])
			}
		}
		return cli.ExitRefused
	}
}

func main() {
	os.Exit(runArgs(os.Args[1:]))
}

func runArgs(args []string) int {
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
	rest = personArgs(rest)
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help" {
		usage()
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP) // an SSH drop ends an apply cleanly, the flock goes with the process
	defer stop()
	for _, v := range mcp.Verbs {
		if v.Name == rest[0] {
			return v.Run(ctx, env, rest[1:])
		}
	}
	fmt.Fprintf(env.Errw(), "laib: unknown verb %q\n\n", rest[0])
	usage()
	return 1
}

func usage() {
	fmt.Fprintln(os.Stderr, "laib — the control plane for one home box\n\nVerbs (tier in brackets):")
	sorted := slices.Clone(mcp.Verbs)
	slices.SortFunc(sorted, func(a, b cli.Verb) int { return strings.Compare(a.Name, b.Name) })
	for _, v := range sorted {
		fmt.Fprintf(os.Stderr, "  %-10s [%s]  %s\n", v.Name, v.Tier, v.Help)
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
