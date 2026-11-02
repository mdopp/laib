package state

import (
	"strings"
	"testing"
)

// ADR 0030: one name for the assistant, „Homes“ when box.yml names none; a
// name must be one word, so a unit and a sentence carry it as it is.
func TestTheAssistantIsHomesUnlessBoxYmlNamesAnother(t *testing.T) {
	for box, want := range map[string]string{"": "Homes", "assistant: Jarvis\n": "Jarvis", "assistant: Jürgen\n": "Jürgen"} {
		st, err := Load(box46(t, box))
		if err != nil || st.Box.Assistant != want {
			t.Errorf("box.yml %q: %q, %v; want %q", box, st.Box.Assistant, err, want)
		}
	}
	for _, bad := range []string{"assistant: Hey Homes\n", "assistant: \"H0mes\"\n", "assistant: \"${DOMAIN}\"\n"} {
		if _, err := Load(box46(t, bad)); err == nil || !strings.Contains(err.Error(), "box.yml assistant") {
			t.Errorf("box.yml %q loaded: %v", bad, err)
		}
	}
}
