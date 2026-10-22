package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/proposal"
)

// stoppedThenReset: the unit was stopped, and reset-failed came after the
// stop, so a unit that had failed is not listed as failed without its file
func stoppedThenReset(t *testing.T, calls []string, unit string) {
	t.Helper()
	stop := slices.Index(calls, "systemctl --user stop "+unit)
	reset := slices.Index(calls, "systemctl --user reset-failed "+unit)
	if stop < 0 || reset < 0 || reset < stop {
		t.Fatalf("%s: want stop, then reset-failed (stop at %d, reset-failed at %d):\n%s", unit, stop, reset, strings.Join(calls, "\n"))
	}
}

// #551 (8): a removed service's failed units stayed listed as failed, with
// no unit file left (whoami, devweb-8790 on the Kraftwerk). laib remove
// resets them after it stopped them.
func TestRemoveResetsTheFailedStateOfItsUnits(t *testing.T) {
	c := newCbox(t)
	c.calls = nil
	if rc := cmdRemove(context.Background(), c.env, []string{"whoami"}); rc != cli.ExitOK {
		t.Fatalf("remove rc %d", rc)
	}
	stoppedThenReset(t, c.calls, "whoami.service")
}

// #551 (8): the other way a unit file goes, an apply that deletes the files
// of a service the state no longer has (here a rollback of its add).
func TestApplyResetsTheFailedStateOfAUnitWhoseFileGoes(t *testing.T) {
	c := newCbox(t)
	writeRegistryTemplate(t, c, "echo2")
	if rc := inbox.Add(context.Background(), c.env, []string{"echo2"}); rc != cli.ExitOK {
		t.Fatalf("add rc %d", rc)
	}
	ps, _ := proposal.List(cli.InboxDir(c.env), false)
	c.calls = nil
	if rc := inbox.Rollback(context.Background(), c.env, []string{"#" + itoa(ps[0].ID)}); rc != cli.ExitOK {
		t.Fatalf("rollback rc %d", rc)
	}
	stoppedThenReset(t, c.calls, "echo2.service")
}
