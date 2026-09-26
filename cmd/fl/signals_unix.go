//go:build unix

package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/Mizore66/faultline/internal/sigexit"
)

// nodeDefaultSignals are the signals whose default action kills Node but
// that the Go runtime would otherwise ignore (ALRM, USR2, VTALRM, XCPU),
// turn into a goroutine dump and exit 2 (QUIT, ABRT), or leave ignored
// when inherited that way (HUP under nohup, INT and QUIT in background
// jobs). Node resets every disposition to SIG_DFL at startup. SIGPROF
// cannot be emulated: the runtime consumes it before os/signal sees it.
var nodeDefaultSignals = []os.Signal{
	syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGABRT, syscall.SIGALRM,
	syscall.SIGTERM, syscall.SIGUSR2, syscall.SIGVTALRM, syscall.SIGXCPU,
}

// installSignals emulates Node's dispositions. SIGPIPE and SIGXFSZ are
// caught and dropped (Node ignores them), so writes fail with EPIPE/EFBIG;
// unlike SIG_IGN, a caught signal is reset to the default in git, as libuv
// resets every signal in its children. The signals above end fl by their
// default action (sigexit.Die), also when fl inherited them as ignored:
// Notify replaces the inherited SIG_IGN, as Node's reset does.
func installSignals() {
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE, syscall.SIGXFSZ)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, nodeDefaultSignals...)
	go func() {
		sigexit.Die((<-ch).(syscall.Signal))
	}()
}
