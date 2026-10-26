package diag

import (
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
)

// #665: the Inbox rotated MQTT_PASSWORD, which the door lock holds. A
// secret the template marks `devices: true` is never offered as a rotate
// repair; once rotated, diag offers putting the old value back.
func TestADeviceHeldSecretIsNeverARotateRepairAndOffersZurueck(t *testing.T) {
	st := &state.State{Services: map[string]*state.Service{
		"mosquitto": {Name: "mosquitto", Contract: state.Contract{Secrets: []state.Secret{{Name: "MQTT_PASSWORD", Devices: true}}}},
		"ha":        {Name: "ha", Contract: state.Contract{Secrets: []state.Secret{{Name: "MQTT_PASSWORD", External: true}}}},
	}}
	env := &cli.Env{Paths: boxfs.Paths{Journal: t.TempDir()}}
	if _, fix, repair := carriedFix(st, "MQTT_PASSWORD", nil); repair != nil || !strings.Contains(fix, "Geräte") {
		t.Fatalf("a device-held secret offered as a rotate repair: %q %v", fix, repair)
	}
	if inbox.Repairable(st, []string{"secret", "rotate", "MQTT_PASSWORD"}) {
		t.Fatal("a device-held secret is repairable by rotation")
	}
	if fs := deviceFindings(env, st); len(fs) != 0 {
		t.Fatalf("not rotated, no finding: %+v", fs)
	}
	journal.Append(env.Paths.Journal, journal.Entry{Actor: "human:owner", Verb: "secret", Result: "info", Detail: "secret rotate MQTT_PASSWORD"})
	fs := deviceFindings(env, st)
	want := []string{"secret", "zurueck", "MQTT_PASSWORD"}
	if len(fs) != 1 || fs[0].Service != "mosquitto" || fs[0].Severity != "gelb" || !slices.Equal(fs[0].Repair, want) ||
		!strings.Contains(fs[0].Detail, "MQTT_PASSWORD wurde erneuert, aber Geräte kennen das alte") {
		t.Fatalf("a rotated device-held secret offers zurück: %+v", fs)
	}
	if !inbox.Repairable(st, want) || inbox.Repairable(st, []string{"secret", "zurueck", "NOPE"}) {
		t.Fatal("zurück is repairable for a declared secret only")
	}
	journal.Append(env.Paths.Journal, journal.Entry{Actor: "human:owner", Verb: "secret", Result: "info", Detail: "secret zurück MQTT_PASSWORD"})
	if fs := deviceFindings(env, st); len(fs) != 0 {
		t.Fatalf("put back, no finding: %+v", fs)
	}
}

// HA: the hook could not log in with the value Laib holds, so the app did
// not take the rotation. Offering the same repair again fails the same way:
// the owner gets the how-to instead.
func TestARotationTheAppRefusedIsTheOwnersHowTo(t *testing.T) {
	st := &state.State{Services: map[string]*state.Service{
		"home-assistant": {Name: "home-assistant", Contract: state.Contract{Secrets: []state.Secret{{Name: "HA_ADMIN_PASSWORD", DataBound: true, Rotate: true}}}},
	}}
	env := &cli.Env{Paths: boxfs.Paths{Journal: t.TempDir()}}
	if _, _, repair := carriedFix(st, "HA_ADMIN_PASSWORD", lastSecret(env)); repair == nil {
		t.Fatal("before any try, the rotation is a repair")
	}
	journal.Append(env.Paths.Journal, journal.Entry{Actor: "human:owner", Verb: "secret", Result: "red", Detail: "secret rotate HA_ADMIN_PASSWORD: von der App nicht übernommen"})
	_, fix, repair := carriedFix(st, "HA_ADMIN_PASSWORD", lastSecret(env))
	if repair != nil || !strings.Contains(fix, "von Hand") || !strings.Contains(fix, "laib secret set HA_ADMIN_PASSWORD") {
		t.Fatalf("a refused rotation offered again: %q %v", fix, repair)
	}
}
