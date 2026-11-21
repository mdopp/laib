package inbox

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
)

// a roll-forward (laib rollback) that ends red says the new version is on again and still not right,
// any other repair that fails says it failed
func TestARollForwardThatIsStillRedSaysSoAndAnotherFailureDoesNot(t *testing.T) {
	env, st, _, _ := wahlBox(t)
	human := as(env, "human:test")
	file := func(line string) int {
		p, err := fileRepair(context.Background(), env, st, "Reparatur", "Reparatur", "nichts", nil, []string{line})
		if err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	for _, c := range []struct {
		line string
		code int
		want string
	}{
		{"laib rollback 4f9a2c1e", cli.ExitRed, "Die neue Version ist wieder aufgespielt, läuft aber noch nicht richtig."},
		{"laib rollback 4f9a2c1f", cli.ExitRefused, "fehlgeschlagen: "},
		{"laib restart whoami", cli.ExitRed, "fehlgeschlagen: "},
	} {
		RunVerb = func(context.Context, *cli.Env, []string) int { return c.code }
		id := file(c.line)
		Decide(context.Background(), human, []string{strconv.Itoa(id)}, true)
		if p := get(t, env, id); !strings.HasPrefix(p.Because, c.want) {
			t.Errorf("%s ending %d: %q, want %q", c.line, c.code, p.Because, c.want)
		}
	}
}
