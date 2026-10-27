package review

import (
	"slices"
	"testing"
)

// #782, the shape of box proposal #201: a one-line policy.yml patch on a
// state whose services declare external secrets with an owner's words and
// a login. The card's reason is the policy, none of the untouched secrets.
func TestReviewJudgesOnlyWhatAPolicyPatchChanges(t *testing.T) {
	base := copyTree(t, golden)
	edit(t, base, "services/caddy/laib.yml", "  - name: DNS_API_TOKEN\n    external: true\n",
		"  - name: DNS_API_TOKEN\n    external: true\n    optional: true\n    owner: {label: \"Zertifikate\", warum: \"Damit\", hilfe: \"deSEC\", link: \"https://desec.io/tokens\"}\n"+
			"  - name: CADDY_ADMIN_PASSWORD\n    external: true\n    login: {user: admin, host: caddy, note: \"Caddy\"}\n")
	changed := copyTree(t, base)
	edit(t, changed, "policy.yml", "  apply: {by: human}\n", "")
	if r := Dirs(base, changed); !slices.Equal(r.Human, []string{"ändert die Freigaberegeln (policy.yml)"}) {
		t.Errorf("a policy patch: %q", r.Human)
	}
}
