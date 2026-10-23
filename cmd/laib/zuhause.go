package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

// cmdZuhause: `laib zuhause show | set [--street S] [--postcode P] [--city C]
// [--country DE] [--latitude N --longitude E] [--elevation M]` (#561). Set
// proposes the whole block as the value at box.yml:zuhause (#598), which a human approves:
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
	var v yaml.Node
	if err := v.Encode(z); err != nil {
		return env.Fail(err)
	}
	id, err := inbox.ProposeAt(ctx, env, st, "zuhause", "Die Adresse des Haushalts ändern (box.yml zuhause)", map[string]*yaml.Node{"box.yml:zuhause": &v})
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
