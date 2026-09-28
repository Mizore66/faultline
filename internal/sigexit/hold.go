package sigexit

import (
	"runtime"
	"sync/atomic"
)

// dying is set once Die has started.
var dying atomic.Bool

// Hold is called before fl writes its report or exits. Node dies of a
// signal the moment it arrives; fl learns of it on a goroutine, so output
// and the exit status could still get out first. Hold lets a signal the
// runtime has already queued reach its goroutine, and blocks for good once
// Die has started.
func Hold() {
	runtime.Gosched()
	if dying.Load() {
		select {}
	}
}
