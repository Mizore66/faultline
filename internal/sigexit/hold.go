package sigexit

import (
	"runtime"
	"sync/atomic"
	"time"
)

// dying is set once Die has started.
var dying atomic.Bool

// childKilled is set when a child fl started died of a signal fl did not
// send it.
var childKilled atomic.Bool

// ChildKilled records that a child died of a signal fl did not send. That
// signal most likely came with fl's process group (Ctrl-C, a terminal
// hang-up, killpg): fl got it at the same instant, but learns of it only
// after the runtime hands it to os/signal and the boot goroutine, while the
// child's death lets fl finish verifying within a few milliseconds.
func ChildKilled() { childKilled.Store(true) }

// holdWait bounds the wait for that signal. It only delays fl's output,
// which TS would print unchanged if no signal comes.
const holdWait = 500 * time.Millisecond

// Hold is called before fl writes its report or exits. Node dies of a
// signal the moment it arrives; fl learns of it on a goroutine, so output
// and the exit status could still get out first. Hold lets a signal the
// runtime has already queued reach its goroutine, waits up to holdWait for
// one after a child was killed by a signal, and blocks for good once Die
// has started.
func Hold() {
	runtime.Gosched()
	if childKilled.Load() {
		for deadline := time.Now().Add(holdWait); !dying.Load() && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
	}
	if dying.Load() {
		select {}
	}
}
