package proposal

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Intent is what a structured proposal asks for (#637), kept beside its
// diff: when the branch no longer merges, approve applies it to the current
// main again. Was holds each value as main had it when it was proposed, so
// a value someone else changed since is not written over.
type Intent struct {
	Set    map[string]string `json:"set,omitempty"`
	At     map[string]string `json:"at,omitempty"` // the value as YAML; "" removes it
	Was    map[string]string `json:"was,omitempty"`
	Grant  []Grant           `json:"grant,omitempty"`
	Update string            `json:"update,omitempty"` // the service a template update refreshes …
	From   string            `json:"from,omitempty"`   // … from this template,
	Sum    string            `json:"sum,omitempty"`    // whose files were these when the owner saw them
}

// Recompute is what approve needs to compute a proposal again (#637): the
// proposer's judge and the templates as they are now.
type Recompute struct {
	Judge    Judge
	Template func(name string) (fs.FS, error)
}

// intentConflict: the intent itself no longer fits the current main.
type intentConflict string

func (c intentConflict) Error() string { return string(c) }

// intentOf is the edit's intent; nil for a patch or a new service, whose
// conflict is the owner's to read.
func intentOf(repo string, e Edit) *Intent {
	if e.Patch != "" || e.Add != nil || e.Update != nil && e.Update.From == "" {
		return nil
	}
	in := &Intent{Set: e.Set, Grant: e.Grant, Was: map[string]string{}}
	for k := range e.Set {
		in.Was[k] = valueAt(repo, k)
	}
	for k, v := range e.At {
		if in.At == nil {
			in.At = map[string]string{}
		}
		in.At[k], in.Was[k] = yamlText(v), valueAt(repo, k)
	}
	if e.Update != nil {
		in.Update, in.From, in.Sum = e.Update.Name, e.Update.From, templateSum(e.Update.Template)
	}
	return in
}

// edit is the intent as an Edit again, with the template as it is now.
func (in *Intent) edit(re *Recompute) (Edit, error) {
	e := Edit{Set: in.Set, Grant: in.Grant}
	for k, v := range in.At {
		if e.At == nil {
			e.At = map[string]*yaml.Node{}
		}
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(v), &doc); err != nil {
			return e, err
		}
		e.At[k] = nil
		if len(doc.Content) > 0 {
			e.At[k] = doc.Content[0]
		}
	}
	if in.Update != "" {
		if re.Template == nil {
			return e, errors.New("keine Vorlage zum Neuberechnen")
		}
		tpl, err := re.Template(in.From)
		if err != nil {
			return e, err
		}
		e.Update = &UpdateService{Name: in.Update, From: in.From, Template: tpl}
	}
	return e, nil
}

// recompute applies p's intent to the current main checked out in wt and
// reviews it again (#637). When the result is what the owner approved —
// class, files, tokens, services, reasons, warnings and the card's changes
// — it is committed on p's branch and merged in wt, as an approve's merge
// is. Otherwise nothing is applied and the content the intent makes now
// comes back, to be filed fresh. An intentConflict says the intent itself
// no longer fits.
func recompute(ctx context.Context, repo, wt string, p *Proposal, by string, re *Recompute) (*Proposal, error) {
	for _, k := range slices.Sorted(maps.Keys(p.Intent.Was)) {
		if now, was := valueAt(wt, k), p.Intent.Was[k]; now != was {
			_, at, _ := strings.Cut(k, ":")
			return nil, intentConflict(fmt.Sprintf("%s wurde seit dem Vorschlag geändert: jetzt %s, vorher %s", at, shown(now), shown(was)))
		}
	}
	e, err := p.Intent.edit(re)
	if err != nil {
		return nil, err
	}
	np, err := prepare(ctx, repo, wt, e, re.Judge)
	if err != nil {
		return nil, err
	}
	np.Anchored(p.Anchor)
	if !sameDecision(p, np) || e.Update != nil && templateSum(e.Update.Template) != p.Intent.Sum {
		git(ctx, wt, "reset", "-q", "--hard")
		return np, nil
	}
	for _, args := range [][]string{
		{"checkout", "-q", "-B", p.Branch},
		{"commit", "-q", "-m", "proposal: " + p.Reason + " (neu berechnet auf dem aktuellen Stand)", "--author", p.Actor + " <" + p.Actor + "@laib>"},
		{"checkout", "-q", "--detach", "main"},
		{"merge", "-q", "--no-ff", "-m", fmt.Sprintf("approve #%d by %s: %s", p.ID, by, p.Reason), p.Branch},
	} {
		if _, err := git(ctx, wt, args...); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// sameDecision: b is what the owner decided on in a.
func sameDecision(a, b *Proposal) bool {
	shownChanges := func(cs []Change) []Change {
		return slices.DeleteFunc(slices.Clone(cs), func(c Change) bool { return c.Op == "=" })
	}
	return a.Class == b.Class && slices.Equal(slices.Sorted(slices.Values(a.Files)), slices.Sorted(slices.Values(b.Files))) &&
		slices.Equal(a.Mints, b.Mints) && slices.Equal(a.Adds, b.Adds) && slices.Equal(a.Reasons, b.Reasons) &&
		slices.Equal(a.Warnings, b.Warnings) && slices.Equal(a.Snapshot, b.Snapshot) && slices.Equal(shownChanges(a.Changes), shownChanges(b.Changes))
}

// valueAt is the value at "file:a.b.c" under dir as YAML without its
// comments, "" when it is not there; a list item "<segment>:<value>" as
// WithPath names it gives its value.
func valueAt(dir, key string) string {
	file, at, _ := strings.Cut(key, ":")
	b, _ := os.ReadFile(filepath.Join(dir, file))
	var doc yaml.Node
	if yaml.Unmarshal(b, &doc) != nil || len(doc.Content) == 0 {
		return ""
	}
	n := doc.Content[0]
	for _, s := range strings.Split(at, ".") {
		var next *yaml.Node
		for i, c := range n.Content {
			switch {
			case n.Kind == yaml.MappingNode && i%2 == 0 && c.Value == s && i+1 < len(n.Content):
				next = n.Content[i+1]
			case n.Kind == yaml.SequenceNode && strings.HasPrefix(c.Value, s+":"):
				return strings.TrimPrefix(c.Value, s+":") + "\n"
			}
		}
		if next == nil {
			return ""
		}
		n = next
	}
	c := *n
	c.HeadComment, c.LineComment, c.FootComment = "", "", ""
	return yamlText(&c)
}

func yamlText(v *yaml.Node) string {
	if v == nil {
		return ""
	}
	b, _ := yaml.Marshal(v)
	return string(b)
}

func shown(v string) string { return cmp.Or(strings.TrimSpace(v), "(leer)") }

// templateSum fingerprints a template's files.
func templateSum(tpl fs.FS) string {
	h := sha256.New()
	fs.WalkDir(tpl, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := fs.ReadFile(tpl, path)
			fmt.Fprintf(h, "%s\x00%d\x00", path, len(b))
			h.Write(b)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}
