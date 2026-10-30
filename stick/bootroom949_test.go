package stick

import (
	"strings"
	"testing"
)

// #949: one kernel set is ~150 MiB on the GPU box, and /boot had 59 MiB
// free: the next OS update with a new kernel would not fit. Once the booted
// deployment is proven, the boot check removes the rollback deployment
// (rpm-ostree cleanup -r, never the booted one) when /boot is short, and
// tells the owner what it found. The rollback is the safety net of a
// deployment that is not proven, so then it stays; and nothing is touched
// while a deployment is staged or pending.

// cleanupFrees is the rpm-ostree stand-in whose cleanup -r leaves /boot
// with 260 MiB free.
const cleanupFrees = `[ "$1 $2" != "cleanup -r" ] || echo $((260 * 1024)) >"$BOOT_DF"; exit 0`

func cleanedUp(log string) bool { return strings.Contains(log, "rpm-ostree cleanup -r") }

func TestAProvenBootWithAFullBootRemovesTheRollback(t *testing.T) {
	for name, prove := range map[string]string{"already the proven one": fresh + ".0", "proven this boot": proven} {
		t.Run(name, func(t *testing.T) {
			b := newBootBox(t)
			b.bootFree = 59
			b.prove(t, prove, "0")
			log, out := b.runWith(t, fresh, "", "", "0", cleanupFrees)
			if rolledBack(log) || !cleanedUp(log) {
				t.Fatalf("a proven boot with 59 MiB free on /boot must remove the rollback, nothing else:\n%s\n%s", log, out)
			}
			if !strings.Contains(log, "note --kind watch /boot war voll (59 MiB frei): der alte Rückfall-Stand wurde entfernt, jetzt 260 MiB frei") {
				t.Fatalf("the owner is not told, in German, what was freed:\n%s\n%s", log, out)
			}
		})
	}
}

func TestAProvenBootWithRoomLeavesTheRollback(t *testing.T) {
	b := newBootBox(t)
	b.bootFree = 200
	b.prove(t, fresh+".0", "0")
	if log, out := b.runWith(t, fresh, "", "", "0", cleanupFrees); cleanedUp(log) || strings.Contains(log, "note --kind watch /boot") {
		t.Fatalf("200 MiB free fits a kernel set: nothing to remove, nothing to say:\n%s\n%s", log, out)
	}
}

// Not proven: the rollback is the way back, whether the check rolls back to
// it now or refuses to (rolled back from this deployment once already).
func TestANotProvenBootKeepsTheRollback(t *testing.T) {
	b := newBootBox(t)
	b.bootFree = 59
	b.prove(t, proven, "0")
	log, out := b.runWith(t, fresh, "", "", "", cleanupFrees) // laib down: rolls back
	if !rolledBack(log) || cleanedUp(log) {
		t.Fatalf("a deployment that is not proven rolls back and keeps its rollback:\n%s\n%s", log, out)
	}
	log, out = b.runWith(t, fresh, "", "", "", cleanupFrees) // the same deployment again: no second rollback
	if rolledBack(log) || cleanedUp(log) {
		t.Fatalf("not proven, no rollback either: the rollback deployment must stay:\n%s\n%s", log, out)
	}
}

func TestAStagedOrPendingDeploymentBlocksTheCleanup(t *testing.T) {
	for _, staged := range []string{"staged", "pending"} {
		t.Run(staged, func(t *testing.T) {
			b := newBootBox(t)
			b.bootFree = 59
			b.staged = staged
			b.prove(t, fresh+".0", "0")
			if log, out := b.runWith(t, fresh, "", "", "0", cleanupFrees); cleanedUp(log) || rolledBack(log) {
				t.Fatalf("a %s deployment: the cleanup must wait for the next boot:\n%s\n%s", staged, log, out)
			}
		})
	}
}

func TestAFailedCleanupIsToldAndTheCheckEndsAsBefore(t *testing.T) {
	b := newBootBox(t)
	b.bootFree = 59
	b.prove(t, fresh+".0", "0")
	log, out := b.runWith(t, fresh, "", "", "0", `[ "$1 $2" != "cleanup -r" ] || { echo "error: Transaction in progress" >&2; exit 1; }; exit 0`)
	if !cleanedUp(log) || rolledBack(log) {
		t.Fatalf("the cleanup was not tried, or something else happened:\n%s\n%s", log, out)
	}
	if !strings.Contains(log, "note --kind incident /boot ist voll (59 MiB frei, ein Kernel-Satz braucht ~150 MiB), aber der alte Rückfall-Stand ließ sich nicht entfernen (rpm-ostree cleanup -r: error: Transaction in progress)") {
		t.Fatalf("the owner is not told what failed:\n%s\n%s", log, out)
	}
	if !strings.Contains(out, "is the proven one") {
		t.Fatalf("the check must end as before:\n%s", out)
	}
}
