package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// #759: the trained „Hey Joshua“ never reached the Voice PE: the README's
// step in ESPHome was by hand, and the owner does not work on a console.
// Stimme's status names it with a repair, `laib flash esphome`; it writes
// firmware onto a device, so it is a destroy only a human approves in the
// Inbox, and the card says what happens. Approved, laib starts esphome's
// flash unit and answers at once; the unit copies the model, edits the
// device's YAML and uploads over the air.
const deviceFinding = `{"at":"%s","detail":"Wake-Word „Hey Joshua“ trainiert, noch nicht auf dem Gerät","phrase":"Hey Joshua","geraet":false,
"findings":[{"severity":"gelb","detail":"Wake-Word „Hey Joshua“ trainiert, noch nicht auf dem Gerät „Voice 0907c9“","fix":"laib repair status stimme",
"repair":["flash","esphome"],"topic":"wakeword","does":"„Hey Joshua“ wird auf den Lautsprecher gespielt","effect":"Der Lautsprecher startet neu und ist etwa 2 Minuten still, danach hört er auf „Hey Joshua“.",
"undo":"Ein anderes Wake-Word kommt nur mit einem neuen Aufspielen zurück."}]}`

func flashBox(t *testing.T) (*cliEnv, func() []string) {
	t.Helper()
	env, data := testBox(t)
	for svc, files := range map[string]map[string]string{
		"esphome": {"laib.yml": "name: esphome\n", "esphome.container": "[Container]\nImage=img\n\n[Install]\nWantedBy=default.target\n",
			"esphome-flash.service": "[Service]\nType=oneshot\nExecStart=/bin/true\n"},
		"stimme": {"laib.yml": "name: stimme\nstatus:\n  file: ${DATA_DIR}/stimme/micro/laib-status.json\n",
			"stimme-whisper.container": "[Container]\nImage=img\n\n[Install]\nWantedBy=default.target\n"},
	} {
		dir := filepath.Join(env.Paths.State, "services", svc)
		os.MkdirAll(dir, 0o755)
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(data, "stimme", "micro"), 0o755)
	status := strings.Replace(deviceFinding, "%s", time.Now().UTC().Format(time.RFC3339), 1)
	if err := os.WriteFile(filepath.Join(data, "stimme", "micro", "laib-status.json"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []string
	prev := sysd.Run
	t.Cleanup(func() { sysd.Run = prev })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" {
			mu.Lock()
			calls = append(calls, strings.Join(args, " "))
			mu.Unlock()
		}
		return prev(ctx, name, args...)
	}
	return env, func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(calls) }
}

func flashed(calls []string) bool {
	return slices.ContainsFunc(calls, func(c string) bool { return strings.Contains(c, "start --no-block esphome-flash.service") })
}

func TestTheWakeWordReachesTheDeviceOnlyAfterTheOwnerApproves(t *testing.T) {
	ctx := context.Background()
	env, calls := flashBox(t)
	st, _ := cli.LoadState(env)
	f := slices.IndexFunc(diag.Probes(ctx, env, st, "stimme"), func(f diag.Finding) bool { return f.Probe == "status" && f.Topic == "wakeword" })
	if f < 0 || !inbox.Repairable(st, []string{"flash", "esphome"}) || inbox.Repairable(st, []string{"flash", "whoami"}) {
		t.Fatal("the status file's finding carries its topic and repair; only a service with a flash unit flashes")
	}
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_flash", map[string]any{"args": "esphome"}); code == cli.ExitOK || flashed(calls()) {
		t.Fatalf("an agent flashed a device itself: exit %d: %s", code, out)
	}
	if code := envelope.Flash(ctx, &cliEnv{Paths: env.Paths, Actor: "claude"}, []string{"esphome"}); code == cli.ExitOK || flashed(calls()) {
		t.Fatal("a hand, not a human, ran laib flash")
	}
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_repair", map[string]any{"probe": "status", "service": "stimme"}); code != cli.ExitOK {
		t.Fatalf("the Operator's repair proposal: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || open[0].Tier != "destroy" || open[0].Class != "human" || flashed(calls()) {
		t.Fatalf("want one open destroy for a human, nothing flashed yet: %+v", open)
	}
	card := proposal.Card(open[0], "box.test")
	if card.Summary != "„Hey Joshua“ wird auf den Lautsprecher gespielt" || !strings.Contains(card.Why, "noch nicht auf dem Gerät") || !slices.ContainsFunc(card.Impact, func(s string) bool { return strings.Contains(s, "etwa 2 Minuten still") }) ||
		!strings.HasPrefix(card.Reversible, "Nicht mit einem Tipp rückgängig") || !strings.Contains(card.Reversible, "neuen Aufspielen") {
		t.Fatalf("the card must say what happens to the device and that a tap does not undo it: %+v", card)
	}
	id := strconv.Itoa(open[0].ID)
	if _, code := mcp.Call(ctx, env, st, "ops", "laib_approve", map[string]any{"args": id}); code != cli.ExitRefused || flashed(calls()) {
		t.Fatalf("an agent approved a flash: exit %d", code)
	}
	if code := inbox.Decide(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test"}, []string{id}, true); code != cli.ExitOK || !flashed(calls()) {
		t.Fatalf("approved by a human: exit %d, calls %v", code, calls())
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if !slices.ContainsFunc(es, func(e journal.Entry) bool {
		return e.Verb == "flash" && e.Actor == "human:test" && e.Service == "esphome"
	}) {
		t.Fatalf("the flash runs as the human who approved, journaled: %+v", es)
	}
}

// #759: Box-Aufgabe #4 („Voice PE flashen“) was added without a befund, so
// no measurement could close it; its befund is set afterwards.
func TestATasksBefundIsSetAfterwards(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	claude := &cliEnv{Paths: env.Paths, Actor: "claude"}
	if code := cmdAufgabe(ctx, claude, []string{"add", "Voice PE flashen"}); code != cli.ExitOK {
		t.Fatalf("add: %d", code)
	}
	if code := cmdAufgabe(ctx, claude, []string{"befund", "1", "Status!"}); code == cli.ExitOK {
		t.Fatal("a befund that is none was set")
	}
	if code := cmdAufgabe(ctx, &cliEnv{Paths: env.Paths, Actor: "bote"}, []string{"befund", "1", "status wakeword"}); code == cli.ExitOK {
		t.Fatal("the Bote set a befund")
	}
	if code := cmdAufgabe(ctx, claude, []string{"befund", "#1", "status wakeword"}); code != cli.ExitOK {
		t.Fatalf("befund: %d", code)
	}
	b, _ := os.ReadFile(filepath.Join(env.Paths.State, "aufgaben.yml"))
	if !strings.Contains(string(b), "fertig_wenn: status wakeword") {
		t.Fatalf("aufgaben.yml: %s", b)
	}
}
