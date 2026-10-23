package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

// cmdZuhause: `laib zuhause show | set [--street S] [--postcode P] [--city C]
// [--country DE] [--latitude N --longitude E] [--elevation M]` (#561). Set
// replaces the whole block and becomes a box.yml proposal a human approves:
// the household's address is the owner's call, the review marks it so.
func cmdZuhause(ctx context.Context, env *cliEnv, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	var z state.Zuhause
	if len(args) == 0 || args[0] == "show" {
		if env.JSON {
			return cli.Emit(env, st.Box.Zuhause)
		}
		b, _ := yaml.Marshal(map[string]state.Zuhause{"zuhause": st.Box.Zuhause})
		fmt.Fprint(env.W(), string(b))
		return cli.ExitOK
	}
	fs := flag.NewFlagSet("zuhause set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&z.Street, "street", "", "")
	fs.StringVar(&z.Postcode, "postcode", "", "")
	fs.StringVar(&z.City, "city", "", "")
	fs.StringVar(&z.Country, "country", "", "")
	for name, p := range map[string]**float64{"latitude": &z.Latitude, "longitude": &z.Longitude} {
		fs.Func(name, "", func(v string) error {
			f, err := strconv.ParseFloat(v, 64)
			*p = &f
			return err
		})
	}
	fs.Func("elevation", "", func(v string) error {
		m, err := strconv.Atoi(v)
		z.Elevation = &m
		return err
	})
	usage := "usage: laib zuhause show | set [--street S] [--postcode P] [--city C] [--country DE] [--latitude N --longitude E] [--elevation M]"
	if err := fs.Parse(args[1:]); err != nil {
		return env.Fail(fmt.Errorf("%s: %w", usage, err))
	}
	if args[0] != "set" || fs.NArg() > 0 || fs.NFlag() == 0 {
		return env.Fail(errors.New(usage))
	}
	id, err := proposeBox(ctx, env, st, "zuhause", "Die Adresse des Haushalts ändern (box.yml zuhause)", func(box []byte) ([]byte, error) { return state.WithBlock(box, "zuhause", z) })
	if err != nil {
		return env.Fail(err)
	}
	msg := fmt.Sprintf("Vorschlag #%d: die neue Adresse gilt nach der Freigabe durch einen Menschen.", id)
	if env.JSON {
		return cli.Emit(env, map[string]any{"proposal": id, "message": msg})
	}
	fmt.Fprintln(env.W(), msg)
	return cli.ExitOK
}

// proposeBox files box.yml as edit makes it as a proposal (laib zuhause, laib
// person signal); the review marks a box.yml change a human's to approve. The
// reason reaches the inbox, the journal and the owner's chat: no address, no number.
func proposeBox(ctx context.Context, env *cliEnv, st *state.State, verb, reason string, edit func([]byte) ([]byte, error)) (int, error) {
	box, err := os.ReadFile(filepath.Join(env.Paths.State, "box.yml"))
	if err == nil {
		box, err = edit(box)
	}
	if err != nil {
		return 0, err
	}
	p, err := proposal.CreateFromEdit(ctx, env.Paths.State, cli.InboxDir(env), env.Actor, reason, proposal.Edit{Write: map[string][]byte{"box.yml": box}}, inbox.JudgeFor(st, env.Actor))
	if err != nil {
		return 0, err
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: verb, Result: "info", Reason: reason, Detail: fmt.Sprintf("#%d wartet auf Freigabe", p.ID)})
	return p.ID, nil
}
