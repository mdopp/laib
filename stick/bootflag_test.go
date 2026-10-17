package stick

import (
	"strings"
	"testing"
)

// #81: CoreOS ends the first boot by deleting /boot/ignition.firstboot
// (coreos-ignition-firstboot-complete, ~13 s in) and nothing syncs it. The
// kernel has it in the journal; GRUB reads ext4 without replaying the journal
// and sees the flag until writeback puts the delete in place ~30 s later. A
// power cut in that gap boots a "first boot" again: Ignition re-runs, useradd
// core fails, emergency mode (vm-install runs 38336942856, 38350510758).
// Freezing /boot right after the delete writes the journal home; Laib's
// first boot waits for it.
func TestTheEndOfTheFirstBootReachesTheDiskBeforeLaibStarts(t *testing.T) {
	ign, err := DestIgnition(box())
	if err != nil {
		t.Fatal(err)
	}
	unit := unitFromIgnition(t, ign, "laib-boot-flag.service")
	for _, want := range []string{
		"After=coreos-ignition-firstboot-complete.service",
		"ConditionKernelCommandLine=ignition.firstboot",
		"MountFlags=slave",
		"mount -o remount,rw /boot",
		"fsfreeze --freeze /boot",
		"fsfreeze --unfreeze /boot",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("laib-boot-flag.service lacks %q:\n%s", want, unit)
		}
	}
	first := unitFromIgnition(t, ign, "laib-firstboot.service")
	if !strings.Contains(first, "laib-boot-flag.service") {
		t.Errorf("laib's first boot does not wait for the flag to reach the disk:\n%s", first)
	}
}
