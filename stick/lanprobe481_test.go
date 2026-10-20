package stick

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #481: the v0.10.0 rollout re-measured the Kraftwerk's baseline and the LAN
// probe recorded `0 26`, while from the real LAN only two ports were closed,
// both on purpose: LLDAP (lan: false) and a port a foreign nftables table
// (ServiceBay's servicebay_lanblock) drops. Two defects:
//   - a port closed to the LAN on purpose counted as blocked, so a record-less
//     first run could never be green and looped its whole window;
//   - the probe path (namespace, veth, routes) was built once and trusted for
//     15 minutes; once it stopped carrying packets, every listening port read
//     as blocked, and that last round became the baseline.
// The record now names the blocked ports; a new deployment fails only on a
// port blocked now that was not blocked at the proven boot. laib's own
// lan: false ports are no LAN ports and are not probed.

func laibNft(ports ...int) string {
	ps := make([]string, len(ports))
	for i, p := range ports {
		ps[i] = fmt.Sprint(p)
	}
	return "# Rendered by laib\ntable inet laib\ndelete table inet laib\ntable inet laib {\n\tchain input {\n\t\ttype filter hook input priority -10; policy accept;\n" +
		"\t\tiifname { \"en*\", \"eth*\", \"wl*\" } tcp dport { " + strings.Join(ps, ", ") + " } drop\n\t}\n}\n"
}

func provenRecord(t *testing.T, b *bootBox) string {
	t.Helper()
	p, _ := os.ReadFile(filepath.Join(b.vardir, "boot-proven"))
	return string(p)
}

func TestALanFalsePortIsNoBlockedPortAndTheFirstRunRecordsAtOnce(t *testing.T) {
	b := newBootBox(t)
	open, lanFalse, foreign := listening(t), blockedPort(t), blockedPort(t)
	b.nft = laibNft(5900, lanFalse)
	b.listen = []string{ssLine("0.0.0.0", open), ssLine("[::]", lanFalse), ssLine("0.0.0.0", foreign)}
	b.timeout = "60"
	start := time.Now()
	log, out := b.run(t, fresh, "", "", "0")
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("the first run took %s: it must record the baseline at its first green round, not loop its window\n%s", took, out)
	}
	if rolledBack(log) {
		t.Fatalf("a baseline never rolls back:\n%s\n%s", log, out)
	}
	if got, want := provenRecord(t, b), fmt.Sprintf("%s.0 0 1 %d\n", fresh, foreign); got != want {
		t.Fatalf("boot-proven = %q, want %q: the lan: false port %d is closed on purpose, the foreign-dropped %d is recorded by name\n%s", got, want, lanFalse, foreign, out)
	}
	if strings.Contains(log, fmt.Sprintf("/dev/tcp/127.0.0.2/%d", lanFalse)) {
		t.Errorf("the lan: false port %d was probed: it is no LAN port\n%s", lanFalse, log)
	}
}

func TestAPortBlockedAtTheProvenBootIsNoReasonButANewOneIs(t *testing.T) {
	foreign := blockedPort(t)
	b := newBootBox(t)
	b.prove(t, proven, fmt.Sprintf("0 1 %d", foreign))
	b.listen = []string{ssLine("0.0.0.0", listening(t)), ssLine("0.0.0.0", foreign)}
	if log, out := b.run(t, fresh, "", "", "0"); rolledBack(log) {
		t.Fatalf("port %d was blocked at the proven boot too (a foreign table drops it): no rollback\n%s\n%s", foreign, log, out)
	}
	if got, want := provenRecord(t, b), fmt.Sprintf("%s.0 0 1 %d\n", fresh, foreign); got != want {
		t.Fatalf("boot-proven = %q, want %q", got, want)
	}

	// the same count, another port: blocked now, open at the proven boot
	other := blockedPort(t)
	b = newBootBox(t)
	b.prove(t, proven, fmt.Sprintf("0 1 %d", foreign))
	b.listen = []string{ssLine("0.0.0.0", listening(t)), ssLine("0.0.0.0", other)}
	log, out := b.run(t, fresh, "", "", "0")
	if !strings.Contains(log, "rpm-ostree rollback") {
		t.Fatalf("port %d is newly blocked (the proven boot had %d), and it was not rolled back:\n%s\n%s", other, foreign, log, out)
	}
	if !strings.Contains(out, fmt.Sprint(other)) {
		t.Errorf("the reason must name the newly blocked port %d:\n%s", other, out)
	}
}

// A converge reloads laib's table while the probe runs, and the path into the
// namespace stops carrying packets in the middle of a round: that round
// measured nothing. It is no blocked port; the next round builds a new path.
func TestThePathBreakingDuringAProbeIsNoBlockedPort(t *testing.T) {
	for name, rec := range map[string]string{"baseline": "", "new deployment": "0"} {
		t.Run(name, func(t *testing.T) {
			b := newBootBox(t)
			if rec != "" {
				b.prove(t, proven, rec)
			}
			lanFalse := blockedPort(t)
			b.nft = laibNft(lanFalse)
			b.listen = []string{ssLine("0.0.0.0", listening(t)), ssLine("0.0.0.0", listening(t)), ssLine("0.0.0.0", listening(t)), ssLine("0.0.0.0", lanFalse)}
			b.breakAt = 2
			b.timeout = "20"
			log, out := b.run(t, fresh, "", "", "0")
			if rolledBack(log) {
				t.Fatalf("the probe path broke during a round, and it was rolled back:\n%s\n%s", log, out)
			}
			if got := provenRecord(t, b); got != fresh+".0 0 0\n" {
				t.Fatalf("boot-proven = %q, want %q: every LAN port answers once the path carries packets\n%s", got, fresh+".0 0 0\n", out)
			}
			if !strings.Contains(out, "not measured") {
				t.Errorf("a round that measured nothing must be said:\n%s", out)
			}
		})
	}
}

// The records from before #481 keep their meaning: "<id> <red>" had no LAN
// probe (nothing blocked), "<id> <red> <n>" counted n blocked ports without
// naming them, so a new deployment may have up to n.
func TestRecordsFromBeforeKeepTheirMeaning(t *testing.T) {
	cases := []struct {
		name, rec string
		blocked   int
		rollback  bool
	}{
		{"two fields, one blocked", "0", 1, true},
		{"count 1, one blocked", "0 1", 1, false},
		{"count 1, two blocked", "0 1", 2, true},
		{"count 0, none blocked", "0 0", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBootBox(t)
			b.prove(t, proven, c.rec)
			lanFalse := blockedPort(t)
			b.nft = laibNft(lanFalse)
			b.listen = []string{ssLine("0.0.0.0", listening(t)), ssLine("0.0.0.0", lanFalse)}
			for i := 0; i < c.blocked; i++ {
				b.listen = append(b.listen, ssLine("0.0.0.0", blockedPort(t)))
			}
			log, out := b.run(t, fresh, "", "", "0")
			if rolledBack(log) != c.rollback {
				t.Fatalf("record %q, %d blocked (and a lan: false port): rolled back = %v, want %v\n%s\n%s", c.rec, c.blocked, rolledBack(log), c.rollback, log, out)
			}
		})
	}
}

// The Kraftwerk's 26: every listening port failed from the service, while
// the same probe by hand from an SSH shell reached 24 of them. A connect that
// fails for any reason but "no answer" (timeout) or "refused" (the port, or a
// firewall's reject) says nothing about the port: the round measured nothing.
func TestAProbeThatFailsLocallyIsNoBlockedPort(t *testing.T) {
	b := newBootBox(t)
	b.listen = []string{ssLine("0.0.0.0", listening(t)), ssLine("0.0.0.0", listening(t))}
	b.denied = true
	b.timeout = "2"
	log, out := b.run(t, fresh, "", "", "0")
	if rolledBack(log) {
		t.Fatalf("a baseline never rolls back:\n%s\n%s", log, out)
	}
	if got := provenRecord(t, b); got != fresh+".0 0 0\n" {
		t.Fatalf("boot-proven = %q: a probe refused locally counted its ports as blocked\n%s", got, out)
	}
	if !strings.Contains(out, "Permission denied") || !strings.Contains(out, "not checked") {
		t.Errorf("the probe's own error must be said, and that the LAN was not checked:\n%s", out)
	}
}
