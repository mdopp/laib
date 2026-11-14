package state

import "testing"

// 2026-11-14: a helper is a timer or a oneshot other than the check the
// status names; the check is the service's health (#417), its main units
// are what serves. Only a helper that fails leaves the service running.
// Ablauf L-52
func TestAHelperIsATimerOrAOneshotOtherThanTheCheck(t *testing.T) {
	svc := &Service{Name: "home-assistant", Contract: Contract{Status: &StatusFile{Check: "home-assistant-check.service"}}, Units: map[string]string{
		"home-assistant.container":       "[Container]\nImage=x\n\n[Install]\nWantedBy=default.target\n",
		"home-assistant-drucker.service": "[Service]\nType=oneshot\n",
		"home-assistant-drucker.timer":   "[Timer]\nOnCalendar=*:0/15\n\n[Install]\nWantedBy=timers.target\n",
		"home-assistant-check.service":   "[Service]\nType=oneshot\n",
		"home-assistant-lease.service":   "[Service]\nExecStart=/bin/x\n\n[Install]\nWantedBy=default.target\n",
		"home-assistant-zwave.container": "[Container]\nImage=z\n",
	}}
	for u, want := range map[string]bool{
		"home-assistant.service": false, "home-assistant-zwave.service": false, "home-assistant-lease.service": false,
		"home-assistant-check.service": false, "home-assistant-drucker.service": true, "home-assistant-drucker.timer": true,
	} {
		if got := svc.Helper(u); got != want {
			t.Errorf("Helper(%s) = %v, want %v", u, got, want)
		}
	}
}
