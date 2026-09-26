//go:build unix

package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"
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
// resets every signal in its children. For HUP, INT and TERM, Go's own
// default is to die by the signal, so the handler restores it and
// re-raises. For the others (and when the signal was ignored when fl
// started, which restoring brings back) fl exits with 128+signal, the
// status a shell reports for a signal death.
func installSignals() {
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE, syscall.SIGXFSZ)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, nodeDefaultSignals...)
	go func() {
		sig := (<-ch).(syscall.Signal)
		switch sig {
		case syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM:
			signal.Reset(sig)
			syscall.Kill(os.Getpid(), sig)
			time.Sleep(100 * time.Millisecond)
		}
		os.Exit(128 + int(sig))
	}()
}
