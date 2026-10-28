// Package aufgaben is the box's to-do list (#667): the owner's actions that
// are box topics, not code — a second disk, a NAS share, a device to
// flash — which no pull request ever closes. It lives in the state repo as
// aufgaben.yml, so it is backed up and versioned with the box. A change is
// bookkeeping, not a change of the box: it is committed on main by itself,
// never a proposal, and renders nothing.
package aufgaben

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/proposal"
)

// File is the list's path in the state repo.
const File = "aufgaben.yml"

// Aufgabe is one task. Dates are the box's local days, YYYY-MM-DD.
type Aufgabe struct {
	ID       int    `yaml:"id" json:"id"`
	Titel    string `yaml:"titel" json:"titel"`
	Warum    string `yaml:"warum,omitempty" json:"warum,omitempty"`
	Seit     string `yaml:"seit" json:"seit"`
	Von      string `yaml:"von" json:"von"`                               // who added it
	Fuer     string `yaml:"fuer" json:"fuer"`                             // who it is for (#878): admins, alle or person:<uid>
	Weg      *Weg   `yaml:"weg,omitempty" json:"weg,omitempty"`           // the guided way (#878)
	Erinnern string `yaml:"erinnern,omitempty" json:"erinnern,omitempty"` // from this day on diag reminds again
	// FertigWenn is the diag finding it covers and whose absence closes it
	// (#667, #878): "<probe> [<service|topic>]"; Befund is its old key.
	FertigWenn string    `yaml:"fertig_wenn,omitempty" json:"fertig_wenn,omitempty"`
	Befund     string    `yaml:"befund,omitempty" json:"-"`
	Erledigt   *Erledigt `yaml:"erledigt,omitempty" json:"erledigt,omitempty"`
}

// Weg is how a task is done (#878): a Haus page that leads through it, or
// a conversation with the Bote; one of the two.
type Weg struct {
	Seite     string `yaml:"seite,omitempty" json:"seite,omitempty"`
	Gespraech bool   `yaml:"gespraech,omitempty" json:"gespraech,omitempty"`
}

// Who a task is for (#878); a person's is "person:<uid>".
const (
	Admins = "admins"
	Alle   = "alle"
)

var (
	fuerRe  = regexp.MustCompile(`^person:[a-z0-9][a-z0-9._-]{0,63}$`)
	seiteRe = regexp.MustCompile(`^/[a-z0-9][a-z0-9/_-]{0,63}$`)
)

// Fuer checks who a task is for.
func Fuer(s string) error {
	if s != Admins && s != Alle && !fuerRe.MatchString(s) {
		return fmt.Errorf("%q: für wen die Aufgabe ist, ist admins, alle oder person:<login>, z. B. person:anna", s)
	}
	return nil
}

// ParseWeg reads a weg: a Haus page's path, or gespraech.
func ParseWeg(s string) (*Weg, error) {
	switch {
	case s == "gespraech":
		return &Weg{Gespraech: true}, nil
	case seiteRe.MatchString(s):
		return &Weg{Seite: s}, nil
	}
	return nil, fmt.Errorf("%q ist kein Weg: eine Seite von Haus (/stimme) oder gespraech (gemeinsam mit dem Boten)", s)
}

// Sees: may a caller see a task? whole: its hand sees the whole list;
// otherwise it acts for person (Haus, the Bote; "" for nobody) and sees
// the admins' and everyone's, and of the personal ones only that person's.
func Sees(a Aufgabe, whole bool, person string) bool {
	uid, own := strings.CutPrefix(cmp.Or(a.Fuer, Admins), "person:")
	return whole || !own || person != "" && uid == person
}

// Closable are the probes whose silence is a measurement (#667): each runs
// on every full diag and says so when it cannot look (raid: laib-disk's
// silence is an unmeasured raid finding). Any other probe's absence may
// only mean it did not run — a service not installed, /boot not readable,
// a DNS check not configured — so its task is checked off by a person.
// status (#759): the template's own status file, read on every full diag;
// a finding it marks unmeasured, or no file, closes nothing.
var Closable = []string{"watchdog", "backup", "recovery", "restore-test", "raid", "status"}

// ClosesItself: does diag check a task with this fertig_wenn off?
func ClosesItself(fertigWenn string) bool {
	probe, _, _ := strings.Cut(fertigWenn, " ")
	return slices.Contains(Closable, probe)
}

// Erledigt: when and by whom; Wie says how, when it was not a person's tick.
type Erledigt struct {
	Am  string `yaml:"am" json:"am"`
	Von string `yaml:"von" json:"von"`
	Wie string `yaml:"wie,omitempty" json:"wie,omitempty"`
}

// Open: not done yet.
func (a Aufgabe) Open() bool { return a.Erledigt == nil }

// Due: open, and its reminder day has come.
func (a Aufgabe) Due(today string) bool { return a.Open() && a.Erinnern != "" && a.Erinnern <= today }

var befundRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}( [a-z0-9][a-z0-9._-]{0,63})?$`)

// Day checks a YYYY-MM-DD date.
func Day(s string) error {
	if _, err := time.Parse(time.DateOnly, s); err != nil {
		return fmt.Errorf("%q ist kein Datum der Form JJJJ-MM-TT", s)
	}
	return nil
}

// Befund checks a befund: a diag probe, optionally a service or topic.
func Befund(s string) error {
	if !befundRe.MatchString(s) {
		return fmt.Errorf("%q ist kein Befund: die Probe aus laib diag, dahinter wahlweise Dienst oder Thema, z. B. raid, watchdog, backup offsite, unit immich", s)
	}
	return nil
}

// Match: does a befund cover a finding? Its probe, and its service or
// topic; a befund without one covers only a finding without one, so
// "backup" never hides a failed nightly behind the deferred off-box copy.
func Match(befund, probe, service, topic string) bool {
	p, q, _ := strings.Cut(befund, " ")
	if p == "" || p != probe {
		return false
	}
	if q == "" {
		return service == "" && topic == ""
	}
	return q == service || q == topic
}

type file struct {
	Aufgaben []Aufgabe `yaml:"aufgaben"`
}

// Load reads the list; a box without aufgaben.yml has an empty one.
func Load(stateDir string) ([]Aufgabe, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, File))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %v", File, err)
	}
	for i := range f.Aufgaben { // #878: a list from before
		a := &f.Aufgaben[i]
		a.Fuer, a.FertigWenn, a.Befund = cmp.Or(a.Fuer, Admins), cmp.Or(a.FertigWenn, a.Befund), ""
	}
	return f.Aufgaben, nil
}

// Update loads, changes and saves the list under its own lock, then
// commits aufgaben.yml alone on main as actor; fn returns the commit's
// subject. An unchanged list commits nothing.
func Update(ctx context.Context, stateDir, actor string, fn func([]Aufgabe) ([]Aufgabe, string, error)) error {
	if lock, err := os.OpenFile(stateDir+".aufgaben.lock", os.O_CREATE|os.O_RDWR, 0o644); err == nil {
		syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
		defer lock.Close()
	}
	list, err := Load(stateDir)
	if err != nil {
		return err
	}
	list, why, err := fn(list)
	if err != nil {
		return err
	}
	b, err := yaml.Marshal(file{list})
	if err != nil {
		return err
	}
	b = append([]byte("# Box-Aufgaben (#667): kept by laib aufgabe, each change one commit; erledigt ones stay.\n"), b...)
	if err := boxfs.WriteAtomic(stateDir+".aufgaben.tmp", filepath.Join(stateDir, File), b, 0o644); err != nil {
		return err
	}
	return proposal.CommitPath(ctx, stateDir, File, why, actor)
}

// Add appends a task with the next id; ids are never reused.
func Add(list []Aufgabe, a Aufgabe, by string, now time.Time) ([]Aufgabe, Aufgabe) {
	a.ID = 1
	for _, x := range list {
		a.ID = max(a.ID, x.ID+1)
	}
	a.Seit, a.Von, a.Fuer, a.Erledigt = now.Format(time.DateOnly), by, cmp.Or(a.Fuer, Admins), nil
	return append(list, a), a
}

func find(list []Aufgabe, id int) (*Aufgabe, error) {
	i := slices.IndexFunc(list, func(a Aufgabe) bool { return a.ID == id })
	if i < 0 {
		return nil, fmt.Errorf("Box-Aufgabe #%d gibt es nicht", id)
	}
	if !list[i].Open() {
		return nil, fmt.Errorf("Box-Aufgabe #%d ist schon erledigt (%s, %s)", id, list[i].Erledigt.Am, list[i].Erledigt.Von)
	}
	return &list[i], nil
}

// Done checks an open task off.
func Done(list []Aufgabe, id int, by, how string, now time.Time) error {
	a, err := find(list, id)
	if err != nil {
		return err
	}
	a.Erledigt = &Erledigt{Am: now.Format(time.DateOnly), Von: by, Wie: how}
	return nil
}

// Defer moves an open task's reminder to day.
func Defer(list []Aufgabe, id int, day string) error {
	if err := Day(day); err != nil {
		return err
	}
	a, err := find(list, id)
	if err != nil {
		return err
	}
	a.Erinnern = day
	return nil
}

// Link sets an open task's fertig_wenn (#759).
func Link(list []Aufgabe, id int, befund string) error {
	a, err := find(list, id)
	if err == nil {
		a.FertigWenn = befund
	}
	return err
}

// Counts: the open tasks, and of them the due ones.
func Counts(list []Aufgabe, today string) (open, due int) {
	for _, a := range list {
		if a.Open() {
			open++
		}
		if a.Due(today) {
			due++
		}
	}
	return open, due
}
