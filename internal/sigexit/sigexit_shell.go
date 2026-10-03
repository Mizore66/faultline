//go:build unix && !linux

// Package sigexit ends the process by a signal's default action, which the
// Go runtime cannot do for signals it handles itself (SIGTRAP, SIGQUIT,
// SIGABRT, ...): restoring "default" there means Go's handler, which dumps
// goroutines and exits 2.
package sigexit

import (
	"os"
	"os/signal"
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
	// Node dies of the first signal. Ignoring the others here keeps them
	// ignored across exec, so one that arrives while the shell starts
	// cannot take its place; sig itself is caught and so reset to SIG_DFL.
	var others []os.Signal
	for _, s := range catchable {
		if s != sig {
			others = append(others, s)
		}
	}
	signal.Ignore(others...)
	n := strconv.Itoa(int(sig))
	syscall.Exec("/bin/sh", []string{"sh", "-c", "kill -" + n + " $$; exit " + strconv.Itoa(128+int(sig))}, nil)
	os.Exit(128 + int(sig))
}

// catchable are the signals os/signal can set to SIG_IGN: every signal whose
// default action ends or stops the process, except the synchronous ones the
// runtime keeps for faults in Go code.
var catchable = []syscall.Signal{
	syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTRAP, syscall.SIGABRT, syscall.SIGEMT,
	syscall.SIGSYS, syscall.SIGPIPE, syscall.SIGALRM, syscall.SIGTERM, syscall.SIGTSTP, syscall.SIGTTIN,
	syscall.SIGTTOU, syscall.SIGXCPU, syscall.SIGXFSZ, syscall.SIGVTALRM, syscall.SIGUSR1, syscall.SIGUSR2,
}
