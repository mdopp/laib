package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	var b bytes.Buffer
	io.Copy(&b, r)
	return b.String()
}

// #57 (1): while readiness is not red, serve pings the outside watchdog;
// silence is the signal the outside turns into a push. Red boxes stay
// silent on purpose.
func TestServePingsTheWatchdogWhileNotRed(t *testing.T) {
	c := newCbox(t)
	var pings int32
	wd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&pings, 1) }))
	defer wd.Close()
	b, _ := os.ReadFile(filepath.Join(c.env.paths.State, "box.yml"))
	os.WriteFile(filepath.Join(c.env.paths.State, "box.yml"), append(b, []byte("watchdog:\n  url: "+wd.URL+"/ping/abc\n  contact: Martin, 0170 1234567\n")...), 0o644)
	st, err := loadState(c.env)
	if err != nil {
		t.Fatal(err)
	}
	if !watchdogPing(context.Background(), c.env, st) {
		t.Fatal("a green box must ping")
	}
	if atomic.LoadInt32(&pings) != 1 {
		t.Fatalf("pings: %d", pings)
	}
	r, _ := collectStatus(context.Background(), c.env, st)
	if r.Watchdog == "" || !strings.Contains(r.Watchdog, "zuletzt gemeldet") {
		t.Fatalf("readiness must show the last report: %+v", r)
	}
	c.down["whoami.service"] = true
	if watchdogPing(context.Background(), c.env, st) {
		t.Fatal("a red box must not ping: silence is the alarm")
	}
	if atomic.LoadInt32(&pings) != 1 {
		t.Fatalf("pinged while red: %d", pings)
	}
	if watchdogInterval < 4*time.Minute || watchdogInterval > 6*time.Minute {
		t.Fatalf("every five minutes: %v", watchdogInterval)
	}
}

// #57 (1): no watchdog configured is a yellow diag finding.
func TestDiagIsYellowWithoutAWatchdog(t *testing.T) {
	c := newCbox(t)
	st, _ := loadState(c.env)
	var found bool
	for _, f := range runProbes(context.Background(), c.env, st, "") {
		if f.Probe == "watchdog" && f.Severity == "gelb" {
			found = true
		}
	}
	if !found {
		t.Fatal("no watchdog.url must be yellow: when the box dies, nobody is told")
	}
}

// #57 (2): the paper card for the family — one page, German, with the
// unplug-and-wait instruction and whom to call.
func TestFamilyCardIsOnePageInGerman(t *testing.T) {
	c := newCbox(t)
	b, _ := os.ReadFile(filepath.Join(c.env.paths.State, "box.yml"))
	os.WriteFile(filepath.Join(c.env.paths.State, "box.yml"), append(b, []byte("watchdog:\n  contact: Martin, 0170 1234567\n")...), 0o644)
	out := captureStdout(t, func() {
		if rc := cmdFamilyCard(context.Background(), c.env, nil); rc != exitOK {
			t.Errorf("rc %d", rc)
		}
	})
	for _, want := range []string{"<html", "Wenn die Box nicht geht", "10 Sekunden", "5 Minuten", "Martin, 0170 1234567", "Stick", "Alles läuft"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the family card lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "laib ") || strings.Contains(out, "systemctl") {
		t.Fatal("the family card is not for admins: no commands")
	}
}
