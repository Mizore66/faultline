package sigexit

import (
	"syscall"
	"testing"
	"time"
)

// After a child is killed, Hold waits until holdWait past that death, once:
// a report written in many pieces is not delayed by holdWait per piece
// (round 6: 8 s for one INVALID report).
func TestHoldWaitsOncePerChildDeath(t *testing.T) {
	defer holdUntil.Store(0)
	start := time.Now()
	ChildKilled(syscall.SIGINT)
	for range 5 {
		Hold()
	}
	if d := time.Since(start); d < holdWait || d > holdWait+time.Second {
		t.Fatalf("five Holds after one child death took %v, want about %v", d, holdWait)
	}
}
