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
	"github.com/mdopp/laib/internal/state"
)

// closable are the probes whose silence is a measurement (#667): each runs
// on every full diag and says so when it cannot look (raid: laib-disk's
// silence is an unmeasured raid finding). Any other probe's absence may
// only mean it did not run — a service not installed, /boot not readable,
// a DNS check not configured — so its task is checked off by a person.
// status (#759): the template's own status file, read on every full diag;
// a finding it marks unmeasured, or no file, closes nothing.
var closable = []string{"watchdog", "backup", "recovery", "restore-test", "raid", "status"}

// cover marks the findings an open Box-Aufgabe covers: info, „zurückgestellt
// → Box-Aufgabe #N“, until its erinnern day, then gelb with „erinnert“. A
// red stays red: a deferral silences a nudge, never damage. An unmeasured
// finding is never covered.
func cover(fs []Finding, tasks []aufgaben.Aufgabe, today string) []Finding {
	for i, f := range fs {
		j := slices.IndexFunc(tasks, func(a aufgaben.Aufgabe) bool {
			return a.Open() && !f.Unmeasured && aufgaben.Match(a.Befund, f.Probe, f.Service, f.Topic)
		})
		if j < 0 {
			continue
		}
		a := tasks[j]
		fs[i].Aufgabe = a.ID
		switch {
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
		probe, _, _ := strings.Cut(a.Befund, " ")
		if !a.Open() || !slices.Contains(closable, probe) {
			continue
		}
		if !slices.ContainsFunc(fs, func(f Finding) bool {
			return f.Probe == probe && f.Unmeasured || aufgaben.Match(a.Befund, f.Probe, f.Service, f.Topic)
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

// closeGone checks off what gone finds, as laib, one journal line each.
func closeGone(env *cli.Env, st *state.State, fs []Finding) {
	tasks, err := aufgaben.Load(st.Dir)
	ids := gone(unmeasuredStatus(st, slices.Clip(fs)), tasks)
	if err != nil || len(ids) == 0 {
		return
	}
	const how = "von Laib erledigt: Befund weg"
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
