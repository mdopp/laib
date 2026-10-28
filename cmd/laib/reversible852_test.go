package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/pages"
)

// #852: „Zurücknehmen“ only on a finished card it undoes something for, the
// same answer in /ready's lastChange.reversible, and „Freigeben“ without the
// accent where „Später“ is the safe default.
func TestZuruecknehmenOnlyWhereItUndoesSomething(t *testing.T) {
	env, _ := testBox(t)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	inbox := cli.InboxDir(env)
	os.MkdirAll(inbox, 0o755)
	now := time.Now().UTC()
	write := func(id int, status string, decided time.Time, rest string) {
		t.Helper()
		body := fmt.Sprintf(`{"id":%d,"actor":"ops","branch":"proposal/%d","tier":"apply","status":%q,"created":%q,"decided":%q,"decidedBy":"human:core",%s}`,
			id, id, status, now.Add(-time.Hour).Format(time.RFC3339), decided.Format(time.RFC3339), rest)
		if err := os.WriteFile(fmt.Sprintf("%s/%d.json", inbox, id), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(1, "approved", now.Add(-time.Minute), `"reason":"Reparieren: whoami läuft nicht — laib restart whoami","files":["repairs/2026-10-28T10-00-00.000000.md"],"class":"tree","kind":"tree","finding":"unit whoami","undo":"nichts zurückzunehmen: laib führt nur laib restart whoami aus","does":"Whoami wird neu gestartet"`)
	write(2, "approved", now.Add(-2*time.Minute), `"reason":"neues Modell","files":["services/bote/values.yml"],"class":"values","kind":"values","changes":[{"op":"~","service":"bote","key":"MODEL","old":"a","new":"b"}],"outcome":"effective"`)
	write(3, "open", time.Time{}, `"reason":"von unterwegs","files":["services/media/laib.yml"],"class":"human","kind":"tree","reasons":["macht media aus dem Internet erreichbar (media)"]`)
	write(4, "open", time.Time{}, `"reason":"neuer Wert","files":["services/bote/values.yml"],"class":"values","kind":"values","changes":[{"op":"~","service":"bote","key":"MODEL","old":"b","new":"c"}]`)

	body := inboxMux(env)("GET", "/inbox", nil)
	if strings.Contains(body, "/inbox/1/rollback") {
		t.Errorf("a restart repair offers „Zurücknehmen“, which would undo nothing:\n%s", body)
	}
	if !strings.Contains(body, "/inbox/2/rollback") {
		t.Errorf("a finished value change lacks „Zurücknehmen“:\n%s", body)
	}
	if !strings.Contains(body, `action=/inbox/4/approve`) || !strings.Contains(formOf(body, "/inbox/4/approve"), `class="go"`) {
		t.Errorf("a safe card's „Freigeben“ lost its accent:\n%s", formOf(body, "/inbox/4/approve"))
	}
	if f := formOf(body, "/inbox/3/approve"); f == "" || strings.Contains(f, `class="go"`) {
		t.Errorf("a risky card's „Freigeben“ still reads as the default: %q", f)
	}

	r, _ := pages.CollectStatus(context.Background(), env, st)
	if r.LastChange.Proposal != 1 || r.LastChange.Reversible {
		t.Fatalf("after a repair lastChange is not reversible: %+v", r.LastChange)
	}
	if b, _ := json.Marshal(r.LastChange); !strings.Contains(string(b), `"reversible":false`) {
		t.Fatalf("/ready: %s", b)
	}
}

// formOf is the form posting to action, up to its end.
func formOf(body, action string) string {
	i := strings.Index(body, "action="+action)
	if i < 0 {
		return ""
	}
	j := strings.Index(body[i:], "</form>")
	return body[i : i+j]
}
