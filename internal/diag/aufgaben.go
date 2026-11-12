package diag

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

// cover marks the findings an open Box-Aufgabe covers: info, „zurückgestellt
// → Box-Aufgabe #N“, until its erinnern day, then gelb with „erinnert“. A
// red stays red: a deferral silences a nudge, never damage. An unmeasured
// finding is never covered. A person's task says only that it exists; a status finding asks
// for one (#1168) until one of its fertig_wenn is open or a person ticked it off.
func cover(fs []Finding, tasks []aufgaben.Aufgabe, today string) []Finding {
	for i, f := range fs {
		if f.NeueAufgabe != nil && (f.Unmeasured || slices.ContainsFunc(tasks, func(a aufgaben.Aufgabe) bool {
			return a.FertigWenn == "status "+f.Topic && (a.Open() || a.Erledigt.Von != "laib")
		})) {
			fs[i].NeueAufgabe = nil
		}
		j := slices.IndexFunc(tasks, func(a aufgaben.Aufgabe) bool {
			return a.Open() && !f.Unmeasured && aufgaben.Match(a.FertigWenn, f.Probe, f.Service, f.Topic)
		})
		if j < 0 {
			continue
		}
		a := tasks[j]
		fs[i].Aufgabe = a.ID
		switch {
		case strings.HasPrefix(a.Fuer, "person:"):
			fs[i].Detail += fmt.Sprintf(" (→ Aufgabe #%d für die Person; Laib hakt sie ab, wenn es gemessen ist)", a.ID)
		case a.Due(today):
			fs[i].Detail += fmt.Sprintf(" (erinnert: Box-Aufgabe #%d „%s“, zurückgestellt bis %s)", a.ID, a.Titel, a.Erinnern)
			if f.Severity == "info" {
				fs[i].Severity = "gelb"
			}
		default:
			fs[i].Detail += fmt.Sprintf(" (zurückgestellt → Box-Aufgabe #%d „%s“)", a.ID, a.Titel)
			if f.Severity == "gelb" {
				fs[i].Severity = "info"
			}
		}
	}
	return fs
}

// gone: the open tasks whose finding a full diag measured away.
func gone(fs []Finding, tasks []aufgaben.Aufgabe) []int {
	var out []int
	for _, a := range tasks {
		probe, _, _ := strings.Cut(a.FertigWenn, " ")
		if !a.Open() || !aufgaben.ClosesItself(a.FertigWenn) {
			continue
		}
		if !slices.ContainsFunc(fs, func(f Finding) bool {
			return f.Probe == probe && f.Unmeasured || aufgaben.Match(a.FertigWenn, f.Probe, f.Service, f.Topic)
		}) {
			out = append(out, a.ID)
		}
	}
	return out
}

// unmeasuredStatus: fs and, for each service whose status file is not
// there, that it measured nothing (#759): a template's status closes a task
// only by what it wrote; one that says it could not look marks itself.
func unmeasuredStatus(st *state.State, fs []Finding) []Finding {
	for _, n := range st.ServiceNames() {
		if svc := st.Services[n]; state.StatusPath(st, svc) != "" && !ReadStatus(st, svc).Found {
			fs = append(fs, Finding{Probe: "status", Service: n, Unmeasured: true})
		}
	}
	return fs
}

// unsetKeys: fs and, for each task waiting on a key (homes#362), a finding
// while that key is not set; all of them unmeasured when secrets.age cannot be read.
func unsetKeys(fs []Finding, tasks []aufgaben.Aufgabe, sec map[string]string, err error) []Finding {
	for _, a := range tasks {
		if p, name, _ := strings.Cut(a.FertigWenn, " "); p == "schluessel" && (err != nil || sec[name] == "") {
			fs = append(fs, Finding{Probe: p, Topic: name, Unmeasured: err != nil})
		}
	}
	return fs
}

// closeGone checks off what gone finds, as laib, one journal line each.
func closeGone(env *cli.Env, st *state.State, fs []Finding) {
	tasks, err := aufgaben.Load(st.Dir)
	sec, serr := secrets.Load(env.Paths.Secrets, env.Paths.Key)
	ids := gone(unsetKeys(unmeasuredStatus(st, slices.Clip(fs)), tasks, sec, serr), tasks)
	if err != nil || len(ids) == 0 {
		return
	}
	const how = "Befund weg"
	err = aufgaben.Update(context.Background(), st.Dir, "laib", func(list []aufgaben.Aufgabe) ([]aufgaben.Aufgabe, string, error) {
		for _, id := range ids {
			aufgaben.Done(list, id, "laib", how, time.Now())
		}
		return list, fmt.Sprintf("aufgabe erledigt %v: Befund weg", ids), nil
	})
	result, detail := "info", how
	if err != nil {
		result, detail = "red", err.Error()
	}
	for _, id := range ids {
		cli.Record(env, st, journal.Entry{Actor: "laib", Verb: "aufgabe", Result: result, Detail: fmt.Sprintf("erledigt #%d: %s", id, detail)})
	}
}
