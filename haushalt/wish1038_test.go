package haushalt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// box answers laib-haushalt's calls back as laib would (#1038): the
// Box-Aufgaben and the Inbox as JSON, and records the tasks it adds.
type box struct {
	aufgaben, inbox string
	added           [][]string
	reads           []string
	ceilings        []string
}

func (b *box) Propose(context.Context, string, ...string) (int, string, error) {
	return 0, "", errors.New("unexpected laib propose")
}

func (b *box) Aufgabe(_ context.Context, ceiling string, args ...string) (int, error) {
	b.added, b.ceilings = append(b.added, args), append(b.ceilings, ceiling)
	return 12, nil
}

func (b *box) Read(_ context.Context, ceiling, tool string, out any, args ...string) error {
	b.reads, b.ceilings = append(b.reads, tool+" "+strings.Join(args, " ")), append(b.ceilings, ceiling)
	text := map[string]string{"laib_aufgabe": `{"offen": 1, "faellig": 0, "aufgaben": [` + b.aufgaben + `]}`, "laib_inbox": "[" + b.inbox + "]"}[tool]
	return json.Unmarshal([]byte(text), out)
}

func run1038(t *testing.T, actor string, laib Laib, args ...string) (string, int, string, string) {
	t.Helper()
	var out, errb, jr bytes.Buffer
	env := &Env{Call: Call{JSON: true, Actor: actor, Ceiling: "propose", Intake: &Intake{Repo: "mdopp/x"}}, Laib: laib, Out: &out, Err: &errb, Journal: &jr}
	code := Run(context.Background(), env, args)
	return out.String(), code, errb.String(), jr.String()
}

// wishTask is Homes' wish for standort as aufgabe list shows it, filed on day.
func wishTask(id int, day, erledigtVon string) string {
	done := ""
	if erledigtVon != "" {
		done = `, "erledigt": {"am": "` + day + `", "von": "` + erledigtVon + `"}`
	}
	return `{"id": ` + itoa(id) + `, "titel": "Anfrage aus Homes: Standortverlauf", "seit": "` + day + `", "von": "homes:lena", "fuer": "admins", "weg": {"seite": "/module/standort"}` + done + `}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// proposalJSON is a proposal as inbox --all shows it, created at noon of day.
func proposalJSON(id int, day, status, outcome, rest string) string {
	return `{"id": ` + itoa(id) + `, "status": "` + status + `", "outcome": "` + outcome + `", "created": "` + day + `T12:00:00` + localOffset(day) + `"` + rest + `}`
}

// localOffset is the zone offset of noon on day here, so the proposal's day is the same in the test's zone.
func localOffset(day string) string {
	d, _ := time.ParseInLocation(time.DateOnly, day, time.Local)
	return d.Add(12 * time.Hour).Format("-07:00")
}

const (
	addsReitti = `, "adds": ["reitti"]`
	moduleOn   = `, "changes": [{"op": "~", "service": "homes", "key": "MODULE_AUS", "old": "podcasts,standort", "new": "podcasts"}]`
)

// #1038 (homes ADR 0001 §10): each state of a module's latest wish, from
// the Box-Aufgaben and the Inbox laib-haushalt reads back.
func TestWishStandAnswersEachState(t *testing.T) {
	const day, before = "2026-11-04", "2026-11-03"
	for _, c := range []struct {
		name, task, inbox string
		stand             string
		vorschlag         int
		grund             string
	}{
		{"open, nothing proposed", wishTask(3, day, ""), "", "angefordert", 0, ""},
		{"a proposal adding the backend open", wishTask(3, day, ""), proposalJSON(40, day, "open", "", addsReitti), "wartet", 40, ""},
		{"a proposal taking it out of MODULE_AUS open", wishTask(3, day, ""), proposalJSON(41, day, "open", "", moduleOn), "wartet", 41, ""},
		{"rejected, with the owner's reason", wishTask(3, day, ""), proposalJSON(40, day, "rejected", "rejected", addsReitti+`, "rejectReason": "kein Platz auf der Platte"`), "abgelehnt", 40, "kein Platz auf der Platte"},
		{"„später“ is no answer", wishTask(3, day, ""), proposalJSON(40, day, "rejected", "later", addsReitti+`, "rejectReason": "später"`), "angefordert", 0, ""},
		{"approved", wishTask(3, day, ""), proposalJSON(40, day, "approved", "effective", addsReitti), "eingerichtet", 40, ""},
		{"approved, the run rolled it back", wishTask(3, day, ""), proposalJSON(40, day, "approved", "rolled-back", addsReitti), "angefordert", 0, ""},
		{"filed before 2026-11-05 as „Wunsch aus Homes“", strings.Replace(wishTask(3, day, ""), "Anfrage aus Homes", "Wunsch aus Homes", 1), "", "angefordert", 0, ""},
		{"ticked off by Homes itself", wishTask(3, day, "homes"), "", "umgesetzt", 0, ""},
		{"ticked off by a person", wishTask(3, day, "human:michael"), proposalJSON(40, day, "approved", "effective", addsReitti), "abgelehnt", 0, ""},
		{"ticked off by a person in Homes", wishTask(3, day, "homes:michael"), "", "abgelehnt", 0, ""},
		{"a proposal from before the wish", wishTask(3, day, ""), proposalJSON(40, before, "rejected", "rejected", addsReitti), "angefordert", 0, ""},
		{"another backend, another module's switch, a stale one", wishTask(3, day, ""),
			proposalJSON(40, day, "open", "", `, "adds": ["dawarich"]`) + "," + proposalJSON(41, day, "approved", "effective", `, "changes": [{"op": "~", "service": "homes", "key": "MODULE_AUS", "old": "podcasts,standort", "new": "standort"}]`) + "," + proposalJSON(42, day, "stale", "", addsReitti), "angefordert", 0, ""},
		{"an open one beats a decided one", wishTask(3, day, ""), proposalJSON(44, day, "rejected", "rejected", moduleOn) + "," + proposalJSON(43, day, "open", "", addsReitti), "wartet", 43, ""},
		{"the latest decided one counts", wishTask(3, day, ""), proposalJSON(44, day, "approved", "waiting", moduleOn) + "," + proposalJSON(43, day, "rejected", "rejected", addsReitti), "eingerichtet", 44, ""},
		{"MODULE_AUS removed", wishTask(3, day, ""), proposalJSON(45, day, "approved", "applied", `, "changes": [{"op": "-", "service": "homes", "key": "MODULE_AUS", "old": "standort"}]`), "eingerichtet", 45, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := &box{aufgaben: c.task, inbox: c.inbox}
			out, code, errs, _ := run1038(t, "homes", b, "wish", "stand", "standort=reitti", "podcasts=audiobookshelf")
			var got []Stand
			if code != exitOK || json.Unmarshal([]byte(out), &got) != nil || len(got) != 1 {
				t.Fatalf("exit %d %s %s", code, out, errs)
			}
			want := Stand{Modul: "standort", Backend: "reitti", Stand: c.stand, Aufgabe: 3, Seit: day, Von: "homes:lena", Vorschlag: c.vorschlag, Grund: c.grund}
			if got[0] != want {
				t.Fatalf("got %+v, want %+v", got[0], want)
			}
			for _, cl := range b.ceilings {
				if cl != "propose" {
					t.Fatalf("a read back above the call's ceiling: %v", b.ceilings)
				}
			}
		})
	}
}

// #1038: a module without a wish is left out ([]); the latest of several
// wishes counts; a wish ticked off needs no Inbox; a malformed pair is refused.
func TestWishStandLeavesOutModulesWithoutAWish(t *testing.T) {
	b := &box{}
	if out, code, errs, _ := run1038(t, "claude", b, "wish", "stand", "standort=reitti"); code != exitOK || strings.TrimSpace(out) != "[]" {
		t.Fatalf("no wish: exit %d %q %s", code, out, errs)
	}
	if len(b.reads) != 1 {
		t.Fatalf("without a wish the Inbox is not read: %v", b.reads)
	}
	b = &box{aufgaben: wishTask(3, "2026-11-01", "human:michael") + "," + wishTask(9, "2026-11-04", "homes") + `, {"id": 10, "titel": "Zertifikat erneuern", "seit": "2026-11-04", "von": "ops", "fuer": "admins", "weg": {"seite": "/module/standort"}}`}
	out, code, _, _ := run1038(t, "homes", b, "wish", "stand", "standort=reitti")
	var got []Stand
	if json.Unmarshal([]byte(out), &got); code != exitOK || len(got) != 1 || got[0].Aufgabe != 9 || got[0].Stand != "umgesetzt" || len(b.reads) != 1 {
		t.Fatalf("the latest wish, no Inbox read: exit %d %s %v", code, out, b.reads)
	}
	for _, bad := range [][]string{{}, {"Standort=reitti"}, {"standort=Reitti!"}, {"standort=reitti; rm"}} {
		if _, code, _, _ := run1038(t, "homes", &box{}, append([]string{"wish", "stand"}, bad...)...); code == exitOK {
			t.Errorf("%q was taken", bad)
		}
	}
}

// #1038: Homes' wish for a module is a Box-Aufgabe for the admins, with who
// asked, the module's Homes page as its weg and a warum that says what
// ticking it off means; never a GitHub issue.
func TestHomesWishForAModuleIsABoxAufgabe(t *testing.T) {
	b := &box{}
	out, code, errs, journal := run1038(t, "homes", b, "wish", "--modul", "standort", "--von", "lena", "Standortverlauf")
	if code != exitOK || len(b.added) != 1 {
		t.Fatalf("exit %d %s %s %v", code, out, errs, b.added)
	}
	a := b.added[0]
	if a[0] != "add" || a[1] != "Anfrage aus Homes: Standortverlauf" || argAfter(a, "--fuer") != "admins" || argAfter(a, "--weg") != "/module/standort" || argAfter(a, "--von") != "homes:lena" {
		t.Fatalf("the task: %q", a)
	}
	if w := argAfter(a, "--warum"); !strings.Contains(w, "lena") || !strings.Contains(w, "wird nicht eingerichtet") || !strings.Contains(w, "hakt Homes die Aufgabe selbst ab") || strings.ContainsAny(w, "\n\r") {
		t.Fatalf("warum %q", w)
	}
	var got map[string]any
	if json.Unmarshal([]byte(out), &got); got["aufgabe"] != float64(12) || got["neu"] != true || got["modul"] != "standort" || got["von"] != "homes:lena" {
		t.Fatalf("answer %v", got)
	}
	if !strings.Contains(journal, "Box-Aufgabe #12") {
		t.Fatalf("journal %q", journal)
	}
	// without von: from Homes itself
	b = &box{}
	if _, code, _, _ := run1038(t, "homes", b, "wish", "--modul", "podcasts", "Podcasts"); code != exitOK || argAfter(b.added[0], "--von") != "homes" {
		t.Fatalf("no von: exit %d %v", code, b.added)
	}
}

// #1038: while a wish for the module is open, a second files nothing and
// answers the open one; once that one is ticked off, a new one is filed.
func TestASecondWishWhileOneIsOpenFilesNothing(t *testing.T) {
	b := &box{aufgaben: wishTask(3, "2026-11-04", "")}
	out, code, _, _ := run1038(t, "homes", b, "wish", "--modul", "standort", "--von", "max", "Standortverlauf")
	var got map[string]any
	if json.Unmarshal([]byte(out), &got); code != exitOK || len(b.added) != 0 || got["aufgabe"] != float64(3) || got["neu"] != false || got["von"] != "homes:lena" {
		t.Fatalf("exit %d %s %v", code, out, b.added)
	}
	b = &box{aufgaben: wishTask(3, "2026-11-04", "human:michael")}
	if _, code, _, _ := run1038(t, "homes", b, "wish", "--modul", "standort", "Standortverlauf"); code != exitOK || len(b.added) != 1 {
		t.Fatalf("after a no, a new wish: exit %d %v", code, b.added)
	}
}

// #1038: --modul is Homes' alone; an unknown or malformed module and a
// malformed login are refused; nothing is filed.
func TestAModuleWishIsHomesAloneAndChecked(t *testing.T) {
	for _, actor := range []string{"bote", "claude", "ops", "human:core"} {
		b := &box{}
		if _, code, errs, _ := run1038(t, actor, b, "wish", "--modul", "standort", "Standortverlauf"); code != exitRefused || len(b.added)+len(b.reads) != 0 || !strings.Contains(errs, "Homes") {
			t.Errorf("%s: exit %d %s %v", actor, code, errs, b.added)
		}
	}
	for _, args := range [][]string{
		{"--modul", "wetter", "Wetter"}, {"--modul", "../box", "x"}, {"--modul", "", "x"},
		{"--modul", "standort"}, {"--modul", "standort", "--von", "Lena Maier", "x"},
	} {
		b := &box{}
		if _, code, _, _ := run1038(t, "homes", b, append([]string{"wish"}, args...)...); code == exitOK || len(b.added) != 0 {
			t.Errorf("%q was taken: %v", args, b.added)
		}
	}
}

// #1036: laib module aus is one proposal for Homes' MODULE_AUS, under the
// call's ceiling, the list as a quoted YAML string; empty switches every
// module on; a malformed name is refused before anything is filed.
func TestModuleAusProposesHomesSwitch(t *testing.T) {
	for _, c := range []struct {
		args       []string
		value, why string
	}{
		{[]string{"module", "aus", "podcasts,energie"}, "'podcasts,energie'", "In Homes ausschalten: podcasts, energie"},
		{[]string{"module", "aus", "podcasts,", "energie", "podcasts"}, "'podcasts,energie'", "In Homes ausschalten: podcasts, energie"},
		{[]string{"module", "aus"}, "''", "Alle Bereiche von Homes einschalten"},
		{[]string{"module", "aus", ""}, "''", "Alle Bereiche von Homes einschalten"},
		{[]string{"module", "aus", "podcasts", "--grund", "Podcasts verschwinden aus Homes\nund der App."}, "'podcasts'", "Podcasts verschwinden aus Homes und der App."},
		{[]string{"module", "aus", "--grund", "Alles kommt zurück.", ""}, "''", "Alles kommt zurück."},
	} {
		p := &proposals{}
		out, code, errs, journal := run1038(t, "homes", p, c.args...)
		if code != exitOK || len(p.calls) != 1 || p.ceiling != "propose" {
			t.Fatalf("%q: exit %d %s %s %v", c.args, code, out, errs, p.calls)
		}
		if a := p.calls[0]; a[0] != "--path" || a[1] != "services/homes/values.yml:MODULE_AUS="+c.value || !strings.HasPrefix(a[2], c.why) {
			t.Fatalf("%q: %q", c.args, a)
		}
		if !strings.Contains(out, `"proposal": 1`) || !strings.Contains(journal, "wartet auf Freigabe") {
			t.Fatalf("%q: %s %s", c.args, out, journal)
		}
	}
	for _, bad := range [][]string{{"module"}, {"module", "an", "podcasts"}, {"module", "aus", "Podcasts"}, {"module", "aus", "podcasts'; x"}, {"module", "aus", "a b"}, {"module", "aus", "podcasts", "--grund", strings.Repeat("x", 201)}} {
		p := &proposals{}
		if _, code, _, _ := run1038(t, "homes", p, bad...); code == exitOK || len(p.calls) != 0 {
			t.Errorf("%q was taken: %v", bad, p.calls)
		}
	}
}
