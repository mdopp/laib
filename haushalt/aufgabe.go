package haushalt

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Aufgabe is one Box-Aufgabe as the kernel answers it (#667, #878).
type Aufgabe struct {
	ID         int       `json:"id"`
	Titel      string    `json:"titel"`
	Warum      string    `json:"warum,omitempty"`
	Seit       string    `json:"seit"`
	Von        string    `json:"von"`
	Fuer       string    `json:"fuer"`
	Weg        *Weg      `json:"weg,omitempty"`
	Erinnern   string    `json:"erinnern,omitempty"`
	FertigWenn string    `json:"fertig_wenn,omitempty"`
	Erledigt   *Erledigt `json:"erledigt,omitempty"`
}

// Weg is how a task is done (#878): a Homes page, the Bote, an owner's key
// on Laib's form (homes#362, the kernel links it), or Laib's page (#1236).
type Weg struct {
	Seite      string `json:"seite,omitempty"`
	Gespraech  bool   `json:"gespraech,omitempty"`
	Laib       bool   `json:"laib,omitempty"`
	Schluessel string `json:"schluessel,omitempty"`
	Link       string `json:"link,omitempty"`
}

// Erledigt: when and by whom; Wie says how, when it was not a person's tick.
type Erledigt struct {
	Am  string `json:"am"`
	Von string `json:"von"`
	Wie string `json:"wie,omitempty"`
}

func (a Aufgabe) open() bool            { return a.Erledigt == nil }
func (a Aufgabe) due(today string) bool { return a.open() && a.Erinnern != "" && a.Erinnern <= today }

var (
	fuerRe   = regexp.MustCompile(`^person:[a-z0-9][a-z0-9._-]{0,63}$`)
	seiteRe  = regexp.MustCompile(`^/[a-z0-9][a-z0-9/_-]{0,63}$`)
	keyRe    = regexp.MustCompile(`^schluessel:([A-Z][A-Z0-9_]{0,63})$`)
	befundRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}( [A-Za-z0-9][A-Za-z0-9._-]{0,63})?$`) // a key's topic is its NAME
)

// parseWeg reads a weg: a Homes page's path, gespraech, laib or schluessel:<NAME>.
func parseWeg(s string) (*Weg, error) {
	switch m := keyRe.FindStringSubmatch(s); {
	case s == "gespraech" || s == "laib":
		return &Weg{Gespraech: s == "gespraech", Laib: s == "laib"}, nil
	case seiteRe.MatchString(s):
		return &Weg{Seite: s}, nil
	case m != nil:
		return &Weg{Schluessel: m[1]}, nil
	}
	return nil, fmt.Errorf("%q ist kein Weg: eine Seite von Homes (/stimme), laib (auf Laibs Status-Seite unter Aufgaben), gespraech (gemeinsam mit dem Boten) oder schluessel:<NAME> (ein Schlüssel auf der Box-Seite)", s)
}

func checkFuer(s string) error {
	if s != "admins" && s != "alle" && !fuerRe.MatchString(s) {
		return fmt.Errorf("%q: für wen die Aufgabe ist, ist admins, alle oder person:<login>, z. B. person:anna", s)
	}
	return nil
}

func checkDay(s string) error {
	if _, err := time.Parse(time.DateOnly, s); err != nil {
		return fmt.Errorf("%q ist kein Datum der Form JJJJ-MM-TT", s)
	}
	return nil
}

func checkBefund(s string) error {
	if !befundRe.MatchString(s) {
		return fmt.Errorf("%q ist kein Befund: die Probe aus laib diag, dahinter wahlweise Dienst oder Thema, z. B. raid, watchdog, backup offsite, unit immich", s)
	}
	return nil
}

const aufgabeUsage = `usage: laib aufgabe list [--alle] [--faellig] | zeigen <id> | add "<titel>" [--warum W] [--erinnern JJJJ-MM-TT] [--fertig-wenn "<probe> [<dienst|thema>]"] [--fuer admins|alle|person:<login>] [--weg /seite|laib|gespraech] | erledigt <id> | zurueckstellen <id> <JJJJ-MM-TT> | befund <id> "<probe> [<dienst|thema>]"`

// cmdAufgabe: `laib aufgabe list [--alle] [--faellig] | zeigen <id> | add "<titel>"
// [--warum W] [--erinnern JJJJ-MM-TT] [--fertig-wenn "<probe> [<dienst|thema>]"]
// [--fuer admins|alle|person:<login>] [--weg /seite|laib|gespraech] | erledigt <id>
// | zurueckstellen <id> <JJJJ-MM-TT> | befund <id> "<probe> [<dienst|thema>]"`
// (#667, #759, #878): the box's to-do list. Here the input is checked and the
// answer worded (#1234); the list is aufgaben.yml in the state repo, and who
// sees and changes which task the kernel's part decides (aufgabe_kern), as
// the caller.
func cmdAufgabe(ctx context.Context, env *Env, args []string) int {
	val, pos := map[string]string{}, []string{}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--alle" || a == "--faellig":
			val[a] = "1"
		case strings.HasPrefix(a, "--") && i+1 < len(args):
			val[a], i = args[i+1], i+1
		default:
			pos = append(pos, a)
		}
	}
	usage := errors.New(aufgabeUsage)
	if len(pos) == 0 {
		return env.fail(usage)
	}
	var von []string
	if v := val["--von"]; v != "" {
		von = []string{"--von", v}
	}
	act, today := pos[0], time.Now().Format(time.DateOnly)
	if act == "list" || act == "zeigen" {
		return aufgabeList(ctx, env, act, pos, val, von, today, usage)
	}
	kargs, stdin, err := aufgabeKernArgs(act, pos, val)
	if err != nil {
		return env.fail(err)
	}
	var reply struct {
		Aufgabe Aufgabe `json:"aufgabe"`
		Selbst  bool    `json:"selbst"`
		Link    string  `json:"link"`
	}
	if err := env.Laib.Kern(ctx, env.Auftrag, "laib_aufgabe_kern", stdin, &reply, append(kargs, von...)...); err != nil {
		return env.kernFailed(err)
	}
	a := reply.Aufgabe
	msg := map[string]string{
		"add":            fmt.Sprintf("Box-Aufgabe #%d angelegt: %s", a.ID, a.Titel),
		"erledigt":       fmt.Sprintf("Box-Aufgabe #%d erledigt.", a.ID),
		"befund":         fmt.Sprintf("Box-Aufgabe #%d schließt Laib, wenn der Befund %s weg ist.", a.ID, a.FertigWenn),
		"zurueckstellen": fmt.Sprintf("Box-Aufgabe #%d zurückgestellt bis %s.", a.ID, a.Erinnern),
	}[act]
	switch {
	case act != "add":
	case reply.Link != "":
		msg += ". Laib hakt sie selbst ab, sobald der Schlüssel eingetragen ist: " + reply.Link
	case reply.Selbst:
		msg += fmt.Sprintf(". Laib hakt sie selbst ab, wenn der Befund %s weg ist.", a.FertigWenn)
	case a.FertigWenn != "":
		msg += fmt.Sprintf(". Laib hakt sie nicht selbst ab: %s misst nicht bei jedem diag, abhaken tut ein Mensch.", a.FertigWenn)
	}
	if env.JSON {
		return env.emit(map[string]any{"message": msg, "aufgabe": a})
	}
	fmt.Fprintln(env.Out, msg)
	return exitOK
}

// aufgabeKernArgs checks a change's input and turns it into the kernel part's argv and stdin.
func aufgabeKernArgs(act string, pos []string, val map[string]string) ([]string, string, error) {
	usage := errors.New(aufgabeUsage)
	switch act {
	case "add":
		if len(pos) != 2 || strings.TrimSpace(pos[1]) == "" || len(pos[1]) > 200 || strings.ContainsAny(pos[1]+val["--warum"], "\n\r") {
			return nil, "", usage
		}
		for _, c := range []struct {
			v     string
			check func(string) error
		}{{val["--erinnern"], checkDay}, {val["--fertig-wenn"], checkBefund}, {cmp.Or(val["--fuer"], "admins"), checkFuer}} {
			if c.v != "" && c.check(c.v) != nil {
				return nil, "", c.check(c.v)
			}
		}
		a := Aufgabe{Titel: strings.TrimSpace(pos[1]), Warum: val["--warum"], Erinnern: val["--erinnern"], FertigWenn: val["--fertig-wenn"], Fuer: cmp.Or(val["--fuer"], "admins")}
		var err error
		if w := val["--weg"]; w != "" {
			a.Weg, err = parseWeg(w)
		}
		if err != nil {
			return nil, "", err
		}
		b, _ := json.Marshal(a)
		return []string{"add"}, string(b), nil
	case "erledigt", "zurueckstellen", "befund":
		pos = append(pos, "", "")
		_, err := strconv.Atoi(strings.TrimPrefix(pos[1], "#"))
		day := cmp.Or(pos[2], val["--erinnern"])
		if err != nil || pos[3] != "" || act == "erledigt" && day != "" || act != "erledigt" && day == "" {
			return nil, "", usage
		}
		if act == "befund" && checkBefund(pos[2]) != nil { // #759: a task added without one, linked afterwards
			return nil, "", checkBefund(pos[2])
		}
		if act == "erledigt" {
			return []string{act, pos[1]}, "", nil
		}
		return []string{act, pos[1], day}, "", nil
	}
	return nil, "", usage
}

// aufgabeList is list and zeigen: what the kernel lets the caller see, counted, filtered and shown.
func aufgabeList(ctx context.Context, env *Env, act string, pos []string, val map[string]string, von []string, today string, usage error) int {
	var reply struct {
		Aufgaben   []Aufgabe         `json:"aufgaben"`
		Schluessel []json.RawMessage `json:"schluessel"`
	}
	if err := env.Laib.Kern(ctx, env.Auftrag, "laib_aufgabe_kern", "", &reply, append([]string{"list"}, von...)...); err != nil {
		return env.kernFailed(err)
	}
	list := reply.Aufgaben
	if act == "zeigen" {
		id, err := strconv.Atoi(strings.TrimPrefix(append(pos, "")[1], "#"))
		i := slices.IndexFunc(list, func(a Aufgabe) bool { return a.ID == id })
		if err != nil || len(pos) != 2 {
			return env.fail(usage)
		}
		if i < 0 {
			return env.refused(fmt.Sprintf("Box-Aufgabe #%d gibt es nicht", id))
		}
		if env.JSON {
			return env.emit(map[string]any{"message": list[i].Titel, "aufgabe": list[i]})
		}
		fmt.Fprintf(env.Out, "#%d %s\n%s\n", id, list[i].Titel, list[i].Warum)
		return exitOK
	}
	open, due := 0, 0
	for _, a := range list {
		open += map[bool]int{true: 1}[a.open()]
		due += map[bool]int{true: 1}[a.due(today)]
	}
	list = slices.DeleteFunc(list, func(a Aufgabe) bool {
		return val["--faellig"] != "" && !a.due(today) || val["--alle"] == "" && !a.open()
	})
	if env.JSON {
		return env.emit(map[string]any{"offen": open, "faellig": due, "aufgaben": append([]Aufgabe{}, list...), "schluessel": reply.Schluessel})
	}
	fmt.Fprintf(env.Out, "Box-Aufgaben: %d offen, %d fällig\n", open, due)
	for _, a := range list {
		line := fmt.Sprintf("#%-3d %s", a.ID, a.Titel)
		if a.Erledigt != nil {
			line += fmt.Sprintf("  ✓ %s %s %s", a.Erledigt.Am, a.Erledigt.Von, a.Erledigt.Wie)
		} else if a.Erinnern != "" {
			line += "  erinnern " + a.Erinnern
		}
		if a.FertigWenn != "" {
			line += "  [" + a.FertigWenn + "]"
		}
		if a.Fuer != "admins" {
			line += "  für " + a.Fuer
		}
		fmt.Fprintln(env.Out, strings.TrimRight(line, " "))
	}
	return exitOK
}
