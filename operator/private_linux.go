package operator

import "syscall"

// Private makes the Operator's process undumpable (#839): a skill's
// script runs as the same user in the same container, and without this it
// could read the token from /proc/<pid>/environ or the process's memory.
// The container drops every capability, so nothing in it can override it.
func Private() error {
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_DUMPABLE, 0, 0); e != 0 {
		return e
	}
	return nil
}
