package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
)

// placesOf are the Inbox's cards and its status block, in the order the
// page or fragment holds them (#941): the card ids, then "laeuft".
var placeRe = regexp.MustCompile(`<(?:section|div)[^>]* id="?([0-9]+|laeuft)"?[ >]`)

func placesOf(body string) []string {
	var ids []string
	for _, m := range placeRe.FindAllStringSubmatch(body, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

// cardOf is the Inbox card with id, up to its end.
func cardOf(body string, id int) string {
	start := regexp.MustCompile(fmt.Sprintf(`<section[^>]* id=%d[ >]`, id)).FindStringIndex(body)
	if start == nil {
		return ""
	}
	end := strings.Index(body[start[0]:], "</section>")
	return body[start[0] : start[0]+end]
}

// swapLive does what the live script does with a fragment: each of its
// top-level elements with an id (the cards in their places, then the
// status block, which is last) replaces the page's element with that id.
func swapLive(t *testing.T, page, frag string) string {
	t.Helper()
	for _, m := range regexp.MustCompile(`<section[^>]* id=([0-9]+)[ >]`).FindAllStringSubmatch(frag, -1) {
		n := 0
		fmt.Sscan(m[1], &n)
		old := cardOf(page, n)
		if old == "" {
			t.Fatalf("the fragment has card %d the page does not:\n%s", n, page)
		}
		page = strings.Replace(page, old, cardOf(frag, n), 1)
	}
	if i := strings.Index(frag, "<div id=laeuft"); i >= 0 {
		j, k := strings.Index(page, "<div id=laeuft"), strings.Index(page, "</body>")
		if n := strings.Index(page, "<noscript>"); n > 0 {
			k = n
		}
		if j < 0 {
			t.Fatalf("the page has no status block to swap:\n%s", page)
		}
		page = page[:j] + frag[i:] + page[k:]
	}
	return page
}

// writeDecided puts a proposal decided long ago into the inbox: what
// „Zuletzt entschieden“ shows.
func writeDecided(t *testing.T, env *cliEnv, id int, ago time.Duration) {
	t.Helper()
	at := time.Now().UTC().Add(-ago).Format(time.RFC3339)
	body := fmt.Sprintf(`{"id":%d,"actor":"ops","branch":"proposal/%d","tier":"apply","status":"approved","created":%q,"decided":%q,"decidedBy":"human:core","reason":"neuer Wert","files":["services/bote/values.yml"],"class":"values","kind":"values","changes":[{"op":"~","service":"bote","key":"MODEL","old":"a","new":"b"}],"outcome":"applied"}`, id, id, at, at)
	if err := os.WriteFile(fmt.Sprintf("%s/%d.json", cli.InboxDir(env), id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// #941 (1): three open cards, the middle one approved. Open cards stand
// oldest first; the approved one keeps its place and says there what the
// box does, in the page and in the live fragment; „Zuletzt entschieden“
// stands at the bottom, busy or idle.
func TestADecidedCardKeepsItsPlace(t *testing.T) {
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	ps := []*proposal.Proposal{proposeService(t, env, "w1"), proposeService(t, env, "w2"), proposeService(t, env, "w3")}
	writeDecided(t, env, 9, 2*time.Hour)
	do := inboxMux(env)
	one, two, three := fmt.Sprint(ps[0].ID), fmt.Sprint(ps[1].ID), fmt.Sprint(ps[2].ID)
	want := []string{one, two, three, "laeuft", "9"}

	before := do("GET", "/inbox", nil)
	if got := placesOf(before); !slices.Equal(got, want) {
		t.Fatalf("before: places %v, want %v (open oldest first, the status block last)", got, want)
	}

	unlock, _ := apply.TakeLock(env, st, "apply") // the work before it: the approved card stays busy
	do("POST", fmt.Sprintf("/inbox/%s/approve", two), nil)
	var busy string
	until(t, "waiting shown", func() bool { busy = do("GET", "/inbox", nil); return strings.Contains(busy, "wartet, bis") })
	if got := placesOf(busy); !slices.Equal(got, want) {
		t.Errorf("busy: places %v, want %v:\n%s", got, want, busy)
	}
	mid := cardOf(busy, ps[1].ID)
	if !strings.Contains(mid, "wartet, bis") || !strings.Contains(mid, " data-run>") || strings.Contains(mid, "<button") {
		t.Errorf("the approved card does not say in its place what the box does:\n%s", mid)
	}
	for _, p := range []*proposal.Proposal{ps[0], ps[2]} {
		if c := cardOf(busy, p.ID); !strings.Contains(c, fmt.Sprintf("/inbox/%d/approve", p.ID)) {
			t.Errorf("open card #%d lost its buttons:\n%s", p.ID, c)
		}
	}
	frag := do("GET", "/inbox/stand", nil)
	if got := placesOf(frag); !slices.Equal(got, []string{two, "laeuft", "9"}) {
		t.Errorf("the fragment holds %v, want the decided card and the status block:\n%s", got, frag)
	}
	if strings.Contains(frag, "<html") || strings.Contains(frag, "<script") {
		t.Errorf("the fragment is a page:\n%s", frag)
	}
	if got := placesOf(swapLive(t, busy, frag)); !slices.Equal(got, want) {
		t.Errorf("after the live swap: places %v, want %v", got, want)
	}

	unlock()
	until(t, "carried", settled(env, ps[1].ID))
	frag = do("GET", "/inbox/stand", nil)
	live := swapLive(t, busy, frag)
	idle := do("GET", "/inbox", nil)
	for name, body := range map[string]string{"the page": idle, "the live page": live} {
		if got := placesOf(body); !slices.Equal(got, want) {
			t.Errorf("idle, %s: places %v, want %v:\n%s", name, got, want, body)
		}
		if c := cardOf(body, ps[1].ID); !strings.Contains(c, ">fertig<") || strings.Contains(c, " data-run>") {
			t.Errorf("idle, %s: the approved card does not end fertig in its place:\n%s", name, c)
		}
	}
	if !strings.Contains(idle, "Zuletzt entschieden") || strings.Contains(idle, "Was die Box gerade macht") {
		t.Errorf("the status block is not „Zuletzt entschieden“:\n%s", idle)
	}
}

var buttonRe = regexp.MustCompile(`<button[^>]*>([^<]*)</button>`)

// #941 (2, 3, 4): every open card has one row, Freigeben then Ablehnen,
// risky or not; a risky one says its risk above the row and its Freigeben
// has no accent; no „Später“; the reason and „Ablehnen bestätigen“ come
// only after the first tap on Ablehnen.
func TestEveryCardHasTheSameButtonRow(t *testing.T) {
	env, _ := testBox(t)
	inbox := cli.InboxDir(env)
	os.MkdirAll(inbox, 0o755)
	at := time.Now().UTC().Format(time.RFC3339)
	risky := &proposal.Proposal{ID: 7} // the review flagged it: the owner decides (class human)
	os.WriteFile(fmt.Sprintf("%s/7.json", inbox), []byte(fmt.Sprintf(`{"id":7,"actor":"ops","branch":"proposal/7","tier":"apply","status":"open","created":%q,"reason":"von unterwegs","files":["services/media/laib.yml"],"class":"human","kind":"tree","reasons":["macht media aus dem Internet erreichbar (media)"]}`, at)), 0o644)
	os.WriteFile(fmt.Sprintf("%s/%d.json", inbox, 8), []byte(fmt.Sprintf(`{"id":8,"actor":"ops","branch":"proposal/8","tier":"apply","status":"open","created":%q,"reason":"neuer Wert","files":["services/bote/values.yml"],"class":"values","kind":"values","changes":[{"op":"~","service":"bote","key":"MODEL","old":"b","new":"c"}]}`, at)), 0o644)
	body := inboxMux(env)("GET", "/inbox", nil)
	if strings.Contains(body, "Später") {
		t.Errorf("„Später“ is still on the page:\n%s", body)
	}
	for _, id := range []int{risky.ID, 8} {
		c := cardOf(body, id)
		hidden := regexp.MustCompile(`(?s)<form[^>]* hidden[ >].*?</form>`)
		first := hidden.FindString(c)
		row := strings.Replace(c, first, "", 1)
		var labels []string
		for _, m := range buttonRe.FindAllStringSubmatch(row, -1) {
			labels = append(labels, m[1])
		}
		if !slices.Equal(labels, []string{"Freigeben", "Ablehnen"}) {
			t.Errorf("#%d: buttons %q, want Freigeben, Ablehnen:\n%s", id, labels, c)
		}
		if strings.Contains(row, "name=grund") {
			t.Errorf("#%d: the reason shows before the first tap:\n%s", id, row)
		}
		if !strings.Contains(first, fmt.Sprintf("action=/inbox/%d/reject", id)) || !strings.Contains(first, "name=grund") || !strings.Contains(first, "Warum nicht? (optional)") ||
			!strings.Contains(first, ">Ablehnen bestätigen</button>") || !strings.Contains(first, ">Abbrechen</button>") {
			t.Errorf("#%d: the second step lacks the reason, „Ablehnen bestätigen“ or „Abbrechen“: %q", id, first)
		}
		approve := formOf(c, fmt.Sprintf("/inbox/%d/approve", id))
		line := strings.Index(c, "class=risiko")
		if id == risky.ID && (strings.Contains(approve, `class="go"`) || line < 0 || line > strings.Index(c, fmt.Sprintf("/inbox/%d/approve", id))) {
			t.Errorf("risky #%d: Freigeben has the accent or no risk line above the row:\n%s", id, c)
		}
		if id == 8 && (!strings.Contains(approve, `class="go"`) || line >= 0) {
			t.Errorf("safe #%d: Freigeben without its accent or with a risk line:\n%s", id, c)
		}
	}
	// an empty reason is fine: Ablehnen bestätigen alone ends abgelehnt
	do := inboxMux(env)
	do("POST", "/inbox/8/reject", nil)
	until(t, "rejected", func() bool {
		p, _ := proposal.Get(inbox, 8)
		return p.Status == "rejected" && strings.Contains(cardOf(do("GET", "/inbox", nil), 8), "abgelehnt")
	})
}
