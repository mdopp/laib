package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/secrets"
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
	Argv    []string  `json:"argv"`
	JSON    bool      `json:"json"`
	Actor   string    `json:"actor"`
	Ceiling string    `json:"ceiling"`
	LanIP   string    `json:"lanIp"`
	Scan    *doorScan `json:"scan,omitempty"`
	Zuhause any       `json:"zuhause,omitempty"` // box.yml zuhause, for laib zuhause only (#677)
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
// program calls laib back with its own (the haushalt hand, propose only),
// under the ceiling sent here — propose, or the verb's own lower tier — and
// the dial applies nothing it proposes. What it would journal comes back
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
	if argv[0] == "zuhause" {
		call.Zuhause = st.Box.Zuhause
	}
	if name, status, orders := render.ScanFolder(st); name != "" {
		call.Scan = &doorScan{Name: name, Orders: orders}
		if b, err := os.ReadFile(status); status != "" && err == nil {
			s := string(b)
			call.Scan.Status = &s
		}
	}
	sec, _ := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	url := cmp.Or(os.Getenv("LAIB_HAUSHALT_URL"), "http://"+render.HaushaltListen)
	body, _ := json.Marshal(call)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url+"/call", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+sec[render.HaushaltKey])
	var reply struct {
		Out, Err string
		Code     int
		Journal  []journal.Entry
	}
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
		fmt.Fprintf(env.Errw(), "laib: laib-%s antwortet nicht (%s): %v — systemctl --user status laib-%s.service zeigt warum\n", prog, url, err, prog)
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
