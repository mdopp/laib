package render

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// boteBox renders templates/bote with values.example.yml, the given values
// replaced, and the given people (#184).
func boteBox(t *testing.T, people string, values map[string]string) (Output, error) {
	t.Helper()
	repo := t.TempDir()
	dst := filepath.Join(repo, "services", "bote")
	os.MkdirAll(dst, 0o755)
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: x.home\ndataDir: /d\nroles: {}\nhostNetwork: []\nlaib: {listen: 0.0.0.0}\n"+people), 0o644)
	os.WriteFile(filepath.Join(repo, "policy.yml"), []byte("tiers: [read, propose, apply, destroy]\nhands: {human: {ceiling: destroy}}\n"), 0o644)
	files, _ := filepath.Glob(filepath.Join("..", "..", "templates", "bote", "*"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue // tests/
		}
		name := filepath.Base(f)
		if name == "values.example.yml" {
			name = "values.yml"
			for k, v := range values {
				line := regexp.MustCompile(`(?m)^` + k + `:.*$`)
				if !line.Match(b) {
					t.Fatalf("values.example.yml has no %s", k)
				}
				b = line.ReplaceAll(b, []byte(k+": \""+v+"\""))
			}
		}
		os.WriteFile(filepath.Join(dst, name), b, 0o644)
	}
	st, err := state.Load(repo)
	if err != nil {
		return nil, err
	}
	return Render(st, map[string]string{"BOTE_APPROVER_TOKEN": "approver-tok", "BOTE_MATRIX_TOKEN": "matrix-tok", "BOTE_HA_TOKEN": "ha-tok"})
}

var (
	noSignal   = map[string]string{"SIGNAL_ACCOUNT": "", "SIGNAL_ALLOWED_USERS": "", "ADMIN_NUMBER": ""}
	matrixOn   = map[string]string{"MATRIX_HOMESERVER": "https://hs.example.org", "MATRIX_ALLOWED_USERS": "@admin:example.org", "ADMIN_MXID": "@admin:example.org", "ADMIN_ROOM": "!raum:example.org"}
	telegramOn = map[string]string{"TELEGRAM_ALLOWED_USERS": "123456"}
)

func merge(ms ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// #184: the Bote's channels are a choice. Signal, Matrix and Telegram are
// each optional; an unset one leaves no container. The relay runs while an
// approval identity (Signal number, Matrix ID) is set; Telegram never approves.
func TestBoteChannelsAreAChoice(t *testing.T) {
	signal := "quadlet/bote/bote-signal.container"
	relay := "quadlet/bote/bote-relay.container"

	out, err := boteBox(t, "", nil) // values.example.yml: Signal only
	if err != nil {
		t.Fatal(err)
	}
	if out[signal] == "" || out[relay] == "" {
		t.Fatalf("Signal only: signal-cli and the relay run: %v", keys(out))
	}
	// the relay holds the approver token (#48): a value that names a secret renders it
	if !strings.Contains(out[relay], "LAIB_TOKEN=approver-tok\n") {
		t.Fatalf("the relay's approver token is not rendered:\n%s", out[relay])
	}

	out, err = boteBox(t, "", merge(noSignal, matrixOn))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[signal]; ok {
		t.Fatal("Matrix only: no bote-signal unit")
	}
	for _, want := range []string{"ADMIN_MXID=@admin:example.org\n", "ADMIN_ROOM=!raum:example.org\n", "MATRIX_HOMESERVER=https://hs.example.org\n", "MATRIX_ACCESS_TOKEN=matrix-tok\n"} {
		if !strings.Contains(out[relay], want) {
			t.Errorf("Matrix only: the relay lacks %s:\n%s", want, out[relay])
		}
	}

	out, err = boteBox(t, "", merge(noSignal, telegramOn))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[signal]; ok {
		t.Fatal("Telegram only: no bote-signal unit")
	}
	if _, ok := out[relay]; ok {
		t.Fatal("Telegram only: no relay, Telegram never approves")
	}

	out, err = boteBox(t, "", merge(matrixOn, telegramOn))
	if err != nil {
		t.Fatal(err)
	}
	if out[signal] == "" || out[relay] == "" || !strings.Contains(out[relay], "ADMIN_NUMBER=+") || !strings.Contains(out[relay], "ADMIN_MXID=@admin") {
		t.Fatalf("all three: signal-cli and one relay for both identities: %v", keys(out))
	}

	if _, err := boteBox(t, "", merge(noSignal, map[string]string{"MATRIX_HOMESERVER": "", "TELEGRAM_ALLOWED_USERS": ""})); err == nil || !strings.Contains(err.Error(), "no channel") {
		t.Fatalf("a Bote without any channel must fail plan: %v", err)
	}
}

// #184: a person's target on a channel the Bote does not have fails plan;
// on a box with that channel it is fine.
func TestBotePeopleMatchItsChannels(t *testing.T) {
	people := "people: {lena: [\"matrix:!lena:example.org\"]}\n"
	if _, err := boteBox(t, people, nil); err == nil || !strings.Contains(err.Error(), "MATRIX_HOMESERVER") {
		t.Fatalf("a matrix target on a Signal-only Bote must fail plan: %v", err)
	}
	if _, err := boteBox(t, people, matrixOn); err != nil {
		t.Fatalf("a matrix target on a Bote with Matrix: %v", err)
	}
}
