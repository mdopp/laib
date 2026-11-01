// A wish's status (#978): an approved wish stays in the state repo as
// wishes/<time>.md; laib adds its status there — offen until a hand closes
// it erledigt or verworfen, with the reason, when and who. The daily log
// review re-checks the Operator's own and closes them; the Inbox shows it.
// Bookkeeping like a Box-Aufgabe: one commit on main per change, one
// journal line (verb wish), never a proposal and never a ticket.

package inbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
)

// Wish is an approved wish as wishes/<id>.md has it.
type Wish struct {
	ID       string    `json:"id"` // the file's name without .md, the time it was filed: 2026-10-28T05-30-43.423582
	Title    string    `json:"title"`
	Text     string    `json:"text"`
	Since    time.Time `json:"since"`
	Operator bool      `json:"operator"`        // the Operator filed it
	Skill    string    `json:"skill,omitempty"` // which of its skills, when the wish names it (#978)
	Services []string  `json:"services"`        // the box's services its text names
	Status   string    `json:"status"`          // offen | erledigt | verworfen
	Grund    string    `json:"grund,omitempty"`
	Am       time.Time `json:"am,omitzero"`
	Von      string    `json:"von,omitempty"`
}

// WishStatuses are the states a wish can be in.
var WishStatuses = []string{"offen", "erledigt", "verworfen"}

const statusMark = "\n\n---\nstatus: " // the file's trailer, laib's alone

var (
	wishID    = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d-\d\d-\d\d\.\d{6}$`)
	wishSkill = regexp.MustCompile(`vom Operator \([^,)]*, Fähigkeit ([a-z0-9-]+)\)`)
)

// WishFile is a wish's path in the state repo.
func WishFile(id string) string { return "wishes/" + id + ".md" }

// ReadWish reads one wish from the state repo; names are the services it may name.
func ReadWish(stateDir, id string, names []string) (Wish, error) {
	if !wishID.MatchString(id) {
		return Wish{}, fmt.Errorf("%q ist kein Wunsch (wishes/<Zeit>.md, laib wish list)", id)
	}
	b, err := os.ReadFile(filepath.Join(stateDir, WishFile(id)))
	if err != nil {
		return Wish{}, err
	}
	body, trailer, _ := strings.Cut(string(b), statusMark)
	title, text, _ := strings.Cut(body, "\n")
	w := Wish{ID: id, Title: strings.TrimPrefix(title, "# "), Text: strings.TrimSpace(text), Status: "offen", Services: []string{}}
	w.Since, _ = time.Parse("2006-01-02T15-04-05.000000", id)
	w.Operator = strings.Contains(body, "vom Operator (") || strings.Contains(body, " von ops auf ")
	if m := wishSkill.FindStringSubmatch(body); m != nil {
		w.Skill = m[1]
	}
	low := strings.ToLower(body)
	for _, n := range names {
		if regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(n) + `($|[^a-z0-9])`).MatchString(low) {
			w.Services = append(w.Services, n)
		}
	}
	if trailer != "" {
		for _, l := range strings.Split("status: "+trailer, "\n") {
			k, v, _ := strings.Cut(l, ": ")
			switch k {
			case "status":
				w.Status = v
			case "grund":
				w.Grund = v
			case "am":
				w.Am, _ = time.Parse(time.RFC3339, v)
			case "von":
				w.Von = v
			}
		}
	}
	return w, nil
}

// Wishes are the state repo's approved wishes, oldest first.
func Wishes(st *state.State) []Wish {
	files, _ := filepath.Glob(filepath.Join(st.Dir, "wishes", "*.md"))
	var out []Wish
	for _, f := range files {
		if w, err := ReadWish(st.Dir, strings.TrimSuffix(filepath.Base(f), ".md"), st.ServiceNames()); err == nil {
			out = append(out, w)
		}
	}
	return out
}

// SetWish gives a wish its status and commits the file alone on main.
func SetWish(ctx context.Context, env *cli.Env, st *state.State, id, status, grund string) (Wish, error) {
	w, err := ReadWish(st.Dir, id, st.ServiceNames())
	switch {
	case err != nil:
		return w, err
	case !slices.Contains(WishStatuses, status):
		return w, fmt.Errorf("%q: ein Wunsch ist offen, erledigt oder verworfen", status)
	case status != "offen" && strings.TrimSpace(grund) == "":
		return w, errors.New(status + " braucht einen Grund")
	}
	b, _ := os.ReadFile(filepath.Join(st.Dir, WishFile(id)))
	body, _, _ := strings.Cut(string(b), statusMark)
	w.Status, w.Grund, w.Am, w.Von = status, strings.Join(strings.Fields(grund), " "), time.Now().UTC().Truncate(time.Second), env.Actor
	if status != "offen" {
		body = strings.TrimRight(body, "\n") + fmt.Sprintf("%s%s\ngrund: %s\nam: %s\nvon: %s\n", statusMark, w.Status, w.Grund, w.Am.Format(time.RFC3339), w.Von)
	}
	if err := os.WriteFile(filepath.Join(st.Dir, WishFile(id)), []byte(body), 0o644); err != nil {
		return w, err
	}
	if err := proposal.CommitPath(ctx, st.Dir, WishFile(id), "wish "+status+" "+id+": "+w.Grund, env.Actor); err != nil {
		return w, err
	}
	cli.Record(env, st, journal.Entry{Actor: env.Actor, Verb: "wish", Result: "info", Reason: w.Title, Detail: strings.TrimSuffix(status+": "+w.Grund, ": ")})
	return w, nil
}

// WishCmd is `laib wish list [--alle]` and `laib wish status [<id>
// <offen|erledigt|verworfen> "<grund>"]`, without an id one per line on
// stdin, so the log review closes what it re-checked in one call.
func WishCmd(ctx context.Context, env *cli.Env, args []string) int {
	st, err := cli.LoadState(env)
	if err != nil {
		return env.Fail(err)
	}
	if args[0] == "list" {
		var out []Wish
		for _, w := range Wishes(st) {
			if w.Status == "offen" || slices.Contains(args, "--alle") {
				out = append(out, w)
			}
		}
		if env.JSON {
			return cli.Emit(env, append([]Wish{}, out...))
		}
		for _, w := range out {
			fmt.Fprintf(env.W(), "%s  %-9s %s%s\n", w.ID, w.Status, w.Title, strings.TrimSuffix(" — "+w.Grund, " — "))
		}
		return cli.ExitOK
	}
	if h := proposal.Hand(env.Actor); h != "ops" && h != "claude" && h != "human" {
		return cli.Refused(env, env.Actor+" setzt keinen Wunsch auf erledigt oder verworfen")
	}
	lines := []string{strings.Join(args[1:], " ")}
	if len(args) == 1 {
		lines = nil
		for sc := bufio.NewScanner(env.Stdin()); sc.Scan(); {
			if l := strings.TrimSpace(sc.Text()); l != "" {
				lines = append(lines, l)
			}
		}
	}
	code := cli.ExitOK
	for _, l := range lines {
		f := strings.SplitN(l, " ", 3)
		if len(f) < 2 {
			code = env.Fail(fmt.Errorf("%q: <id> <offen|erledigt|verworfen> <grund>", l))
			continue
		}
		w, err := SetWish(ctx, env, st, f[0], f[1], strings.Trim(strings.Join(f[2:], " "), `"`))
		if err != nil {
			code = env.Fail(err)
			continue
		}
		fmt.Fprintf(env.W(), "%s: %s %s\n", w.ID, w.Status, w.Grund)
	}
	return code
}
