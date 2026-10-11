// Package review reads a change to the state repo the way a careful admin
// would before saying yes (#34): what it touches, in plain German, and
// whether only a human may allow it. It compares two checkouts — main and
// the change — by their files, their contracts and what they render to, and
// it lets through only what it knows to be harmless.
package review

// Result is what a change does that the autonomy dial alone may not decide.
type Result struct {
	Human []string // why only a human may allow the change, in plain German; empty = the dial decides
	Mints []string // hands a newly declared secret mints a Laib token for
	Added []string // services the change adds
}

// Dirs compares a checkout of main (base) with a checkout of the change.
// Not written yet: the tests in review_test.go describe it (#34).
func Dirs(base, changed string) Result {
	return Result{}
}
