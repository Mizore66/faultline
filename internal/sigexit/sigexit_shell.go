//go:build unix && !linux

// Package sigexit ends the process by a signal's default action, which the
// Go runtime cannot do for signals it handles itself (SIGTRAP, SIGQUIT,
// SIGABRT, ...): restoring "default" there means Go's handler, which dumps
// goroutines and exits 2.
package sigexit

import (
	"os"
	"strconv"
	"syscall"
)

// Die replaces the process image (same pid) with a shell that sends itself
// sig. Exec resets caught signals to SIG_DFL, so the parent sees a death by
// sig, as when Node or V8 dies of it. If sig was ignored when fl started,
// exec keeps it ignored, and the shell exits 128+sig, the status a shell
// reports for a signal death. Without /bin/sh, fl exits 128+sig itself.
func Die(sig syscall.Signal) {
	dying.Store(true)
	n := strconv.Itoa(int(sig))
	syscall.Exec("/bin/sh", []string{"sh", "-c", "kill -" + n + " $$; exit " + strconv.Itoa(128+int(sig))}, nil)
	os.Exit(128 + int(sig))
}
