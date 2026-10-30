package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

// served is a verb that a program beside laib serves (ADR 0026, #598). By
// the time it runs, the kernel has checked hand, tier and verb (mcp.Call)
// and cli.Guard has taken --help. The program gets "<verb> <args>", the
// actor, the verb's ceiling and what it may know of the box; its output
// and exit code are the verb's.
func served(prog, verb, tier string) func(context.Context, *cliEnv, []string) int {
	return func(ctx context.Context, env *cliEnv, args []string) int {
		return door(ctx, env, prog, tier, append([]string{verb}, args...))
	}
}

// doorCall is what the door hands laib-haushalt (haushalt.Call; the two
// meet over HTTP, never by import): the program reads no state of laib's.
type doorCall struct {
	Argv    []string    `json:"argv"`
	JSON    bool        `json:"json"`
	Actor   string      `json:"actor"`
	Ceiling string      `json:"ceiling"`
	LanIP   string      `json:"lanIp"`
	Scan    *doorScan   `json:"scan,omitempty"`
	Zuhause any         `json:"zuhause,omitempty"` // box.yml zuhause, for laib zuhause only (#677)
	Intake  *doorIntake `json:"intake,omitempty"`  // box.yml intake, for laib wish only (#797)
}

// doorIntake is box.yml intake for laib wish (haushalt.Intake). Title, Body
// and Label set are the kernel's own ticket (fileIssue), never the caller's.
type doorIntake struct {
	Repo   string `json:"repo"`
	Label  string `json:"label"`
	Domain string `json:"domain"`
	Title  string `json:"title,omitempty"`
	Body   string `json:"body,omitempty"`
}

// doorScan is the service that provides scan: its status file as it wrote
// it (nil while there is none) and its orders folder, which the program's
// container mounts at this path.
type doorScan struct {
	Name   string  `json:"name"`
	Status *string `json:"status"`
	Orders string  `json:"orders"`
}

// door hands the call to the program's container (ADR 0027) on 127.0.0.1,
// with the door's key from secrets.age. It holds no token of laib's: the
// program calls laib back with its own (the haushalt hand: propose, and
// the Bote's wish as a Box-Aufgabe, #883), under the ceiling sent here —
// propose, or the verb's own lower tier — and the dial applies nothing it
// proposes. What it would journal comes back
// with the answer; the kernel writes it as the caller, under the verb.
func door(ctx context.Context, env *cliEnv, prog, tier string, argv []string) int {
	ceiling := "propose"
	if policy.Index(tier) < policy.Index(ceiling) {
		ceiling = tier
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	call := doorCall{Argv: argv, JSON: env.JSON, Actor: env.Actor, Ceiling: ceiling, LanIP: st.Box.LanIP}
	switch argv[0] {
	case "zuhause":
		call.Zuhause = st.Box.Zuhause
	case "wish":
		call.Intake = &doorIntake{Repo: st.Box.Intake.Repo, Label: inbox.WishLabel(st), Domain: st.Box.Domain}
	}
	if name, status, orders := render.ScanFolder(st); name != "" {
		call.Scan = &doorScan{Name: name, Orders: orders}
		if b, err := os.ReadFile(status); status != "" && err == nil {
			s := string(b)
			call.Scan.Status = &s
		}
	}
	reply, err := doorPost(ctx, env, call)
	if err != nil {
		fmt.Fprintf(env.Errw(), "laib: laib-%s antwortet nicht: %v — systemctl --user status laib-%s.service zeigt warum\n", prog, err, prog)
		return cli.ExitRed
	}
	fmt.Fprint(env.W(), reply.Out)
	fmt.Fprint(env.Errw(), reply.Err)
	for _, e := range reply.Journal {
		e.Actor, e.Verb = env.Actor, argv[0]
		cli.Record(env, st, e)
	}
	return reply.Code
}

type doorReply struct {
	Out, Err string
	Code     int
	Journal  []journal.Entry
}

// doorPost posts one call to laib-haushalt with the door's key.
func doorPost(ctx context.Context, env *cliEnv, call doorCall) (doorReply, error) {
	var reply doorReply
	sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	url := cmp.Or(os.Getenv("LAIB_HAUSHALT_URL"), "http://"+render.HaushaltListen)
	body, _ := json.Marshal(call)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url+"/call", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+sec[render.HaushaltKey])
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		} else {
			err = json.NewDecoder(resp.Body).Decode(&reply)
		}
	}
	if err != nil {
		return reply, fmt.Errorf("%s: %w", url, err)
	}
	return reply, nil
}

// fileIssue is inbox.Issue (#797): the kernel's own ticket — an escalation,
// a wish the admin approved — filed by laib-haushalt with its token, as the
// caller. The caller journals it.
func fileIssue(ctx context.Context, env *cliEnv, st *state.State, title, body, label string) (string, bool, error) {
	if st.Box.Intake.Repo == "" {
		return "", false, nil
	}
	in := &doorIntake{Repo: st.Box.Intake.Repo, Label: label, Domain: st.Box.Domain, Title: title, Body: body}
	reply, err := doorPost(ctx, env, doorCall{Argv: []string{"wish"}, JSON: true, Actor: env.Actor, Ceiling: "propose", Intake: in})
	var out struct{ Issue string }
	if err == nil && (reply.Code != cli.ExitOK || json.Unmarshal([]byte(reply.Out), &out) != nil) {
		err = fmt.Errorf("laib-haushalt: %s", strings.TrimSpace(strings.TrimPrefix(reply.Err, "laib:")))
	}
	return out.Issue, true, err
}

// cmdWish is laib wish (#56): laib-haushalt files it behind the door
// (#797). The Operator's wish waits for the admin as a proposal (ADR 0023
// §6), whose approval files it through the same door (fileIssue).
func cmdWish(ctx context.Context, env *cliEnv, args []string) int {
	words, von := args, ""
	if len(words) > 1 && words[0] == "--von" { // the asker, never the title's first words (#896)
		von, words = words[1], words[2:]
	}
	text := strings.TrimSpace(strings.Join(words, " "))
	if proposal.Hand(env.Actor) != "ops" || text == "" {
		return door(ctx, env, "haushalt", "propose", append([]string{"wish"}, args...))
	}
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	by := "vom Operator (" + env.Actor + ")"
	if von != "" {
		by += ", gewünscht von " + von + ","
	}
	return inbox.WishProposal(ctx, env, st, "Wunsch: "+sentence(text), text+"\n\n— "+by+" auf "+st.Box.Domain+", "+time.Now().Format("02.01.2006 15:04")+".")
}

// sentence: a wish's first sentence for its title (#896) — up to „. “ or
// the line's end, never a dot inside a number or an address; 70 runes at most.
func sentence(text string) string {
	first, _, _ := strings.Cut(text, "\n")
	if i := strings.Index(first, ". "); i > 0 {
		first = first[:i+1]
	}
	if r := []rune(first); len(r) > 70 {
		first = string(r[:70]) + "…"
	}
	return first
}
