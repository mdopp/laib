package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/proposal"
)

// #294: an add whose apply was cut short (the converge timer held the lock, a power
// cut) has already put the service into the state. Running the same add again
// finishes the job — it applies, makes no second proposal — instead of
// "services/x exists" with the service half there. Another template under the
// same name is still refused.
func TestAddAgainFinishesAnAddThatDidNotApply(t *testing.T) {
	c := newCbox(t)
	writeRegistryTemplate(t, c, "echo2")
	if rc := cmdAdd(context.Background(), c.env, []string{"echo2"}); rc != exitOK {
		t.Fatalf("add rc %d", rc)
	}
	unit := filepath.Join(c.env.paths.Quadlet, "echo2", "echo2.container")
	os.RemoveAll(filepath.Dir(unit)) // as if the apply never ran
	c.calls = nil
	if rc := cmdAdd(context.Background(), c.env, []string{"echo2"}); rc != exitOK {
		t.Fatalf("the same add again: rc %d, want ok", rc)
	}
	if _, err := os.Stat(unit); err != nil {
		t.Fatal("the second add did not apply the service")
	}
	if ps, _ := proposal.List(inboxDir(c.env), false); len(ps) != 1 {
		t.Fatalf("%d proposals, want the one from the first add", len(ps))
	}
	writeRegistryTemplate(t, c, "other")
	if rc := cmdAdd(context.Background(), c.env, []string{"other", "--name", "echo2"}); rc == exitOK {
		t.Fatal("another template under an existing service's name was accepted")
	}
}
