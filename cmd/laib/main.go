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
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/registry"
)

// Version is set by the release build.
var Version = "dev"

// cliEnv is what every verb gets (internal/cli).
type cliEnv = cli.Env

func init() {
	cli.Version = Version
	mcp.Verbs = []cli.Verb{
		{Name: "status", Tier: "read", Help: "[service] [--fresh]: one line per service; first line is the readiness state", Run: pages.Status},
		{Name: "diag", Tier: "read", Help: "findings with severity and a fix verb each: units, health, drift, disk", Run: diag.Cmd},
		{Name: "plan", Tier: "read", Help: "render the state and diff it against the box", Run: cmdPlan},
		{Name: "apply", Tier: "apply", Help: "[<service>] [--reason \"<grund>\"] | --proposal <id> (ops): snapshot, write, reload, restart, verify", Flags: []string{"--reason", "--proposal"}, Run: cmdApply},
		{Name: "inspect", Tier: "read", Help: "<service>: units, containers, data files, health, last logs — the first minute of any diagnosis", Run: envelope.Inspect},
		{Name: "read", Tier: "read", Help: "<service> <path>: a file in the service's data paths, secrets redacted", Run: envelope.Read},
		{Name: "write", Tier: "apply", Help: "<service> <path> (stdin): a file in the service's data paths, inside the envelope", Flags: []string{}, Run: envelope.Write},
		{Name: "look", Tier: "read", Help: "<service> [--container c] -- <cat|ls|grep|ps|…>: read-only, in the service's own container, unbudgeted", Run: envelope.Look},
		{Name: "exec", Tier: "apply", Help: "<service> [--container c] -- <cmd>: run inside the service's own container, budgeted, snapshot first, output journaled", Flags: []string{"--container"}, Run: envelope.Exec},
		{Name: "restart", Tier: "apply", Help: "<service>: restart its units and verify, inside the envelope", Flags: []string{}, Run: envelope.Restart},
		{Name: "start", Tier: "apply", Help: "<service>: start its units (lifting a stop's pause) and verify, inside the envelope", Flags: []string{}, Run: envelope.Start},
		{Name: "stop", Tier: "apply", Help: "<service> [--until 2h] [\"<grund>\"]: stop its units and pause it (at most a day), inside the envelope", Flags: []string{"--until"}, Run: envelope.Stop},
		{Name: "backups", Tier: "read", Help: "the backups from the journal: last nightly, last green, off-box copy, restore test, with diag's fixes", Run: diag.Backups},
		{Name: "escalate", Tier: "propose", Help: "<service> \"<was ich weiß>\": context bundle + red journal line for the next hand", Run: envelope.Escalate},
		{Name: "note", Tier: "propose", Help: "[--kind report|incident|watch] \"<text>\": what a hand concluded; report is the morning report", Run: cmdNote},
		{Name: "repair", Tier: "propose", Help: "<probe> [service]: a diag finding's repair as a proposal; it runs once a human approves (#441)", Run: diag.Repair},
		{Name: "wish", Tier: "propose", Help: "\"<was das Haus braucht>\": the intake — a GitHub issue with the wunsch label (box.yml intake), kept on the box without one", Run: inbox.Wish},
		{Name: "net", Tier: "read", Help: "[--mermaid] | geraete [--wecken]: every name with its reach, Authelia and target, the direct ports, the host network, the pods' links — from the rendered config (#439); geraete: the LAN's devices from the host's neighbour table (#577)", Run: cmdNet},
		{Name: "updates", Tier: "read", Help: "[--run|--now]: what is behind — a tag's or pin's newer digest, patch and minor releases (#287), a template's extensions and firmware; --run is laib-images.timer's nightly update of the images without data (#528); --now (tier apply) starts that night's run now, Laib's own update first, without waiting (#578)", Run: cmdUpdates},
		{Name: "skill", Tier: "read", Help: "list | enable <name> | pause <name> (humans only): the Operator's skills, active or paused (ADR 0023)", Run: mcp.Skill},
		{Name: "operator", Tier: "apply", Help: "enable [--model alias] [--report-at 07:00] | disable | status (humans only)", Run: cmdOperator},
		{Name: "logs", Tier: "read", Help: "journald for every unit of a service, secrets redacted (--since 1h)", Run: cmdLogs},
		{Name: "rollback", Tier: "apply", Help: "[<commit>]: revert the last (or a given) state commit and apply", Flags: []string{}, Run: inbox.Rollback},
		{Name: "backup", Tier: "propose", Help: "[check]: restic snapshot of state, journal and declared data paths; check tests a restore", Flags: []string{}, Run: backup.Cmd},
		{Name: "restore", Tier: "apply", Help: "<snapshot|latest> <service> | --box <snapshot|latest>: restore declared paths, then apply", Flags: []string{"--box"}, Run: backup.Restore},
		{Name: "converge", Tier: "apply", Help: "[--force]: bring the box back to the state: re-apply drift, restart inactive units, within a budget", Flags: []string{"--force"}, Run: apply.Converge},
		{Name: "state", Tier: "read", Help: "[<path>]: the state repo's files on main, or one as committed — box.yml, policy.yml, services/<s>/values.yml …, to write a --patch against; secrets.age never, secrets redacted (#576)", Run: cmdState},
		{Name: "journal", Tier: "read", Help: "show journal entries (--since 24h) [--limit n] [--verb v] [--kind report|incident|watch]: newest n, filtered", Run: cmdJournal},
		{Name: "context", Tier: "read", Help: "[--since 24h] [--full]: starter bundle for an agent session — the first look (readiness, what is not green, open proposals, the journal short); --full the whole bundle (#579)", Run: cmdContext},
		{Name: "template", Tier: "read", Help: "lint <dir> | list | sync | update <service>", Run: registry.TemplateCmd},
		{Name: "add", Tier: "apply", Help: "<template> [--name n] [KEY=VALUE…]: install a template — propose, approve and apply as the human", Flags: []string{"--name"}, Run: inbox.Add},
		{Name: "remove", Tier: "apply", Help: "<service>: stop its units, take it out of the state, apply; data stays (humans only)", Flags: []string{}, Run: cmdRemove},
		{Name: "inbox", Tier: "read", Help: "[<id>] [--all [--limit 20]]: open proposals in full; --all adds the decided ones newest first without their cards (--limit 0: all); <id> one in full (#579)", Run: inbox.List},
		{Name: "propose", Tier: "propose", Help: "\"<grund>\" [--set path:KEY=VALUE | --add <template> [--name n] [KEY=VALUE…] | --grant <hand> <verb> | --patch] [--finding f --evidence e --undo u] [--source s]: commit the edit to a proposal branch and an inbox entry", Flags: []string{"--patch", "--set", "--path", "--grant", "--add", "--name", "--finding", "--evidence", "--undo", "--source"}, Run: inbox.Propose},
		{Name: "approve", Tier: "apply", Help: "<id>: merge a proposal and apply it (never your own)", Flags: []string{}, Run: inbox.Approve},
		{Name: "reject", Tier: "apply", Help: "<id> [\"<grund>\"]: drop a proposal", Flags: []string{}, Run: inbox.Reject},
		{Name: "adopt", Tier: "propose", Help: "adopt an unmanaged Quadlet folder into services/", Run: cmdAdopt},
		{Name: "ignore", Tier: "propose", Help: "record an unmanaged unit as deliberately unmanaged", Run: cmdIgnore},
		{Name: "session", Tier: "read", Help: "[<name>]: a token of the hand policy.yml names in your session:, with its ttl, for one session (#380)", Run: cmdSession},
		{Name: "token", Tier: "apply", Help: "mint <hand> [--ttl 8h] [--note n] | list | revoke <id> (humans only)", Flags: []string{"--ttl", "--note"}, Run: cmdToken},
		{Name: "secret", Tier: "apply", Help: "set <NAME> (value on stdin) | list | rotate <NAME>", Flags: []string{}, Run: cmdSecret},
		{Name: "disk", Tier: "apply", Help: "status | add <device>: the data RAID; add joins the mirror (humans only)", Run: cmdDisk},
		{Name: "init", Tier: "apply", Help: "first setup: key, state repo skeleton", Run: cmdInit},
		{Name: "serve", Tier: "read", Help: "daemon: /ready, the three pages", Run: pages.Serve},
		{Name: "version", Tier: "read", Help: "print the version", Run: cmdVersion},
		{Name: "self", Tier: "apply", Help: "rollback: the previous binaries back and the units restarted (humans only)", Run: cmdSelf},
		{Name: "family-card", Tier: "read", Help: "print the one-page card for the family: what the box does, what to do when it is down, whom to call", Run: pages.FamilyCard},
		{Name: "recovery-sheet", Tier: "destroy", Help: "print the one page that restores the box (age key, restic repo and password, four steps); --confirm once it is stored (humans at the console only)", Run: backup.RecoverySheet},
		{Name: "person_invite", Tier: "apply", Help: "<user> --first F --last L --email E [--group family|admins] [--resend] [--signal +49…]: a new resident in LLDAP without a password; Authelia mails the set-password link, without SMTP the link comes back; --signal proposes the number for box.yml people (`laib person invite`)", Run: cmdPersonInvite},
		{Name: "person_signal", Tier: "propose", Help: "<user> <+49…>: set or change a person's Signal number in box.yml people, as a proposal a human approves (`laib person signal`, #596)", Run: cmdPersonSignal},
		{Name: "person_pending", Tier: "read", Help: "the invited who have not set their password yet (`laib person pending`)", Run: cmdPersonPending},
		{Name: "calendar", Tier: "apply", Help: "subscribe <person> <name> google|abo [<title>] (the ICS address on stdin, kept in secrets.age, never answered) | unsubscribe <person> <name> | list: the calendars the kalender template mirrors into Radicale (#495)", Run: cmdCalendar},
		{Name: "zuhause", Tier: "propose", Help: "show | set [--street S] [--postcode P] [--city C] [--country DE] [--latitude N --longitude E] [--elevation M]: the household's address in box.yml; set replaces it as a proposal a human approves (#561)", Run: cmdZuhause},
		{Name: "scan", Tier: "propose", Help: "[status] | start | weiter | abschliessen | verwerfen | redo --stapel <zeit> [--person p]: the scan service's state, or an order for it — a stack from the scanner, after a jam go on, finish or drop, a stack again from its archive (#568)", Run: served("haushalt", "scan", "propose"), Door: true},
		{Name: "pause", Tier: "apply", Help: "<service> [--until 2h] [--stop <unit>]… [--restart <unit>]… [\"<grund>\"]: converge leaves the service alone for a while; --stop/--restart act on its own units now, resume brings them back (the GPU lease, ADR 0027)", Flags: []string{"--until", "--stop", "--restart"}, Run: cmdPause},
		{Name: "resume", Tier: "apply", Help: "<service>: lift a pause, start and restart what it stopped and restarted", Flags: []string{}, Run: cmdResume},
	}
	for i := range mcp.Verbs { // --help and unknown flags, for the CLI, MCP and an approved repair alike (#590)
		mcp.Verbs[i] = cli.Guard(mcp.Verbs[i])
	}
	inbox.Present = diag.Present
	proposal.VerbTier = map[string]string{} // the card says whether a granted verb changes or proposes (#616)
	for _, v := range mcp.Verbs {
		proposal.VerbTier[v.Name] = v.Tier
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
	env := &cliEnv{Paths: boxfs.Default(), Actor: os.Getenv("LAIB_ACTOR"), Ceiling: os.Getenv("LAIB_CEILING")}
	if strings.HasPrefix(env.Actor, "human:") {
		env.Wait = 15 * time.Minute // a human at the shell means approve to be applied when it returns (#609)
	}
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
		if v.Name == rest[0] && env.Ceiling != "" && policy.Index(v.Tier) > policy.Index(env.Ceiling) { // a program behind the door (ADR 0026)
			return cli.Refused(env, fmt.Sprintf("%s ist Stufe %s; ein Programm hinter der Tür darf höchstens %s", v.Name, v.Tier, env.Ceiling))
		}
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
