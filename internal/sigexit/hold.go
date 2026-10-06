package sigexit

import (
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
)

// dying is set once Die has started.
var dying atomic.Bool

// holdUntil is, in Unix nanoseconds, holdWait after the last child fl
// started died of a signal fl did not send it; 0 if none did.
var holdUntil atomic.Int64

// ChildKilled records that a child died of a signal fl did not send. That
// signal most likely came with fl's process group (Ctrl-C, a terminal
// hang-up, killpg): fl got it at the same instant, but learns of it only
// after the runtime hands it to os/signal and the boot goroutine, while the
// child's death lets fl finish verifying within a few milliseconds.
//
// A child that died of a fault (SIGSEGV, SIGBUS, SIGILL, SIGFPE, SIGTRAP,
// SIGABRT) crashed by itself; no wait.
func ChildKilled(sig syscall.Signal) {
	switch sig {
	case syscall.SIGSEGV, syscall.SIGBUS, syscall.SIGILL, syscall.SIGFPE, syscall.SIGTRAP, syscall.SIGABRT:
		return
	}
	holdUntil.Store(time.Now().Add(holdWait).UnixNano())
}

// holdWait bounds the wait for that signal, generously for a loaded
// machine. It only delays fl's output, which TS would print unchanged if no
// signal comes.
const holdWait = 2 * time.Second

// Hold is called before fl writes its report or exits. Node dies of a
// signal the moment it arrives; fl learns of it on a goroutine, so output
// and the exit status could still get out first. Hold lets a signal the
// runtime has already queued reach its goroutine, waits for one until
// holdWait after a child was killed by a signal (once in all: later calls
// return at once past that point), and blocks for good once Die has
// started.
func Hold() {
	runtime.Gosched()
	if until := holdUntil.Load(); until != 0 {
		for !dying.Load() && time.Now().UnixNano() < until {
			time.Sleep(time.Millisecond)
		}
	}
	if dying.Load() {
		select {}
	}
}
