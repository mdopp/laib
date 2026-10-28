package render

import (
	"regexp"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// The Bote's Signal number reaches Haus as HAUS_BOTE_SIGNAL (haus#175), so
// Willkommen can say „Joshua auf Signal“: the BOTE_SIGNAL builtin is the
// value a service names under channels.signal, E.164 or nothing.
func TestHausGetsTheBotesSignalNumber(t *testing.T) {
	for _, tc := range []struct{ account, want string }{
		{"+4915100000000", "+4915100000000"},
		{"", ""},             // no Signal on the Bote: Haus shows no number
		{"0151 0000000", ""}, // not E.164: never shown as if it were one
	} {
		st := shippedBox(t)
		st.Services["bote"].Values["SIGNAL_ACCOUNT"] = state.Value{Literal: tc.account}
		out, err := Render(st, map[string]string{})
		if err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(`(?m)^Environment=HAUS_BOTE_SIGNAL=(.*)$`).FindStringSubmatch(out["quadlet/haus/haus.container"])
		if m == nil {
			t.Fatalf("haus.container has no HAUS_BOTE_SIGNAL line")
		}
		if m[1] != tc.want {
			t.Errorf("SIGNAL_ACCOUNT %q: HAUS_BOTE_SIGNAL = %q, want %q", tc.account, m[1], tc.want)
		}
	}
}

func TestBoteSignalWithoutTheBote(t *testing.T) {
	st := shippedBox(t)
	delete(st.Services, "bote")
	if got := st.BoteSignal(); got != "" {
		t.Errorf("BoteSignal without a service on Signal = %q, want empty", got)
	}
}
