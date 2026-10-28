package haushalt

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Zuhause is box.yml zuhause (#561), as the door hands it over: the
// household's address, all optional. The kernel's loader validates it.
type Zuhause struct {
	Street    string   `yaml:"street,omitempty" json:"street,omitempty"`
	Postcode  string   `yaml:"postcode,omitempty" json:"postcode,omitempty"`
	City      string   `yaml:"city,omitempty" json:"city,omitempty"`
	Country   string   `yaml:"country,omitempty" json:"country,omitempty"`
	Latitude  *float64 `yaml:"latitude,omitempty" json:"latitude,omitempty"`
	Longitude *float64 `yaml:"longitude,omitempty" json:"longitude,omitempty"`
	Elevation *int     `yaml:"elevation,omitempty" json:"elevation,omitempty"`
}

// cmdZuhause: `laib zuhause show | set [--street S] [--postcode P] [--city C]
// [--country DE] [--latitude N --longitude E] [--elevation M]` (#561, #677).
// Set proposes the whole block as the value at box.yml:zuhause (#598), which
// a human approves: the household's address is the owner's call.
func cmdZuhause(ctx context.Context, env *Env, args []string) int {
	z := Zuhause{}
	if len(args) == 0 || args[0] == "show" {
		if env.Zuhause != nil {
			z = *env.Zuhause
		}
		if env.JSON {
			return env.emit(z)
		}
		b, _ := yaml.Marshal(map[string]Zuhause{"zuhause": z})
		fmt.Fprint(env.Out, string(b))
		return exitOK
	}
	fs := flag.NewFlagSet("zuhause set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	keys := []string{"street", "postcode", "city", "country", "latitude", "longitude", "elevation"}
	set := map[string]string{} // the block's lines, the strings single-quoted
	for _, name := range keys[:4] {
		fs.Func(name, "", func(v string) error {
			if strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || r == '"' }) {
				return fmt.Errorf("--%s: eine Zeile, ohne Anführungszeichen", name)
			}
			set[name] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
			return nil
		})
	}
	for _, name := range keys[4:] {
		fs.Func(name, "", func(v string) error {
			_, err := strconv.ParseFloat(v, 64)
			if name == "elevation" {
				_, err = strconv.Atoi(v)
			}
			set[name] = v
			return err
		})
	}
	usage := "usage: laib zuhause show | set [--street S] [--postcode P] [--city C] [--country DE] [--latitude N --longitude E] [--elevation M]"
	if err := fs.Parse(args[1:]); err != nil {
		return env.fail(fmt.Errorf("%s: %w", usage, err))
	}
	if args[0] != "set" || fs.NArg() > 0 || fs.NFlag() == 0 {
		return env.fail(errors.New(usage))
	}
	var lines []string
	for _, k := range keys {
		if v, ok := set[k]; ok {
			lines = append(lines, k+": "+v)
		}
	}
	return env.propose(ctx, "", "Die Adresse des Haushalts ändern (box.yml zuhause)",
		"Vorschlag #%d: die neue Adresse gilt nach der Freigabe durch einen Menschen.", "--path", "box.yml:zuhause="+strings.Join(lines, "\n"))
}

// cmdPersonSignal: `laib person signal <user> <+49…>` (#596, #677), a
// person's Signal number in box.yml people as a proposal a human approves;
// the kernel's loader checks the number.
func cmdPersonSignal(ctx context.Context, env *Env, args []string) int {
	if len(args) != 2 {
		return env.fail(errors.New("usage: laib person signal <user> <+49…>"))
	}
	user, number := args[0], args[1]
	if !scanPerson.MatchString(user) {
		return env.fail(fmt.Errorf("%q: ein Name aus box.yml people, klein", user))
	}
	if strings.ContainsAny(number, "\" \n") {
		return env.fail(fmt.Errorf("%q ist keine Signal-Nummer (+49…)", number))
	}
	return env.propose(ctx, "", "Die Signal-Nummer von "+user+" eintragen (box.yml people)",
		"Vorschlag #%d: nach der Freigabe durch einen Menschen darf "+user+" dem Boten auf Signal schreiben.", "--path", "box.yml:people."+user+".signal="+number)
}

// propose files `laib propose <edit…> "<reason>"` as the haushalt hand
// under the verb's ceiling and answers {proposal, class, message}. The
// reason reaches the inbox, the journal and the owner's chat: no address,
// no number.
func (e *Env) propose(ctx context.Context, service, reason, msg string, edit ...string) int {
	if e.Laib == nil {
		return e.fail(errors.New("laib-haushalt has no way back to laib (LAIB_MCP_URL, LAIB_TOKEN)"))
	}
	id, class, err := e.Laib.Propose(ctx, e.Ceiling, append(edit, reason)...)
	if err != nil {
		return e.fail(err)
	}
	e.journal(service, reason, fmt.Sprintf("#%d (%s) wartet auf Freigabe", id, class))
	msg = fmt.Sprintf(msg, id)
	if e.JSON {
		return e.emit(map[string]any{"proposal": id, "class": class, "message": msg})
	}
	fmt.Fprintln(e.Out, msg)
	return exitOK
}
