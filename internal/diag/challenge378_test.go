package diag

import (
	"errors"
	"testing"
)

// #378: before the switch, and after it, _acme-challenge.<domain> must point
// at the alias; otherwise every renewal fails 60 days later, silently.
func TestChallengeFinding(t *testing.T) {
	const alias = "_acme-challenge.laib-acme.dedyn.io"
	if f := challengeFinding("dopp.cloud", alias, alias+".", nil); f != nil {
		t.Fatalf("a matching CNAME is no finding: %+v", f)
	}
	if f := challengeFinding("dopp.cloud", alias, "dopp.cloud.", nil); f == nil || f.Severity != "rot" {
		t.Fatalf("a wrong CNAME is red: %+v", f)
	}
	if f := challengeFinding("dopp.cloud", alias, "", errors.New("no such host")); f == nil || f.Severity != "gelb" {
		t.Fatalf("an unanswered lookup is yellow: %+v", f)
	}
}
