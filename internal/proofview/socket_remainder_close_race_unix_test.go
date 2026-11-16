//go:build linux || darwin || windows

package proofview

import (
	"errors"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

// Scope: these tests probe the HELPER's own Closed state with deliberately
// malicious/non-evicting fakes. They are not evidence about fd memory safety or
// real FD.Close behaviour. Reuses urfWritev/urfObs/urf* helpers and udrRaw from
// the sibling test files.

// crRaw is a RawConn whose Write announces entry and then waits for a test gate
// BEFORE invoking the callback, modelling a worker that is about to run cb#1
// when the helper is closed underneath it.
type crRaw struct {
	entered chan struct{}
	gate    chan struct{}
	once    sync.Once
	cbCalls atomic.Int32
}

func newCRRaw(t *testing.T) *crRaw {
	r := &crRaw{entered: make(chan struct{}, 1), gate: make(chan struct{})}
	t.Cleanup(r.open)
	return r
}

func (r *crRaw) open() { r.once.Do(func() { close(r.gate) }) }

func (r *crRaw) Control(func(uintptr)) error   { return errors.New("unused") }
func (r *crRaw) Read(func(uintptr) bool) error { return errors.New("unused") }

func (r *crRaw) Write(f func(uintptr) bool) error {
	r.entered <- struct{}{}
	<-r.gate
	r.cbCalls.Add(1)
	if !f(9) {
		return errors.New("callback unexpectedly asked to wait")
	}
	return nil
}

var _ syscall.RawConn = (*crRaw)(nil)

// Mutant caught: cb#1 that writes without re-checking the helper's Closed
// state under mu (stale pre-Close "Attempting" decision), so a Close that raced
// ahead of the callback still lets writev run; or a Submit result that reports
// the write outcome / nil instead of exactly errUnixRemainderClosed; or a Closed
// path that skips worker_exit. deps.Close returns nil and does NOT evict, on
// purpose, so only the helper's own guard can stop the write.
func TestUnixRemainderCloseBeforeCallbackBlocksWritev(t *testing.T) {
	r, o, w := newCRRaw(t), newURFObs(t), &urfWritev{script: []urfStep{{n: 8}}}
	c := newUnixRemainderWithDeps(unixRemainderDeps{
		Raw: r, Writev: w.fn, Close: func() error { return nil }, Observe: o.observe,
	})

	sub := urfAsync(func() (unixRemainderResult, error) { return c.Submit(urfParts()) })
	urfRecv(t, r.entered, "RawWrite entered, gated before callback")
	if err := urfClose(t, c); err != nil {
		t.Fatalf("Close = %v, want nil from the non-evicting fake", err)
	}
	r.open() // now the worker runs cb#1 against an already-Closed helper

	got := urfWait(t, sub, "Submit after gated callback released")
	urfRecv(t, o.workerExit, "worker_exit on the Closed path")
	if n := w.calls.Load(); n != 0 {
		t.Fatalf("writev ran %d time(s) after the helper was Closed", n)
	}
	if r.cbCalls.Load() != 1 {
		t.Fatalf("callback calls=%d, want 1 (fixture sanity)", r.cbCalls.Load())
	}
	// Pinned: closed is reported via result.Err with a nil API error.
	if got.err != nil || got.res.Err != errUnixRemainderClosed || got.res.N != 0 || got.res.Done || got.res.Block {
		t.Fatalf("Submit result %+v err=%v, want result.Err == errUnixRemainderClosed, N=0", got.res, got.err)
	}
}

// Mutant caught: a constructor/Submit that accepts nil deps.Close and starts a
// RawWrite that nothing can ever evict (forever park on Close). Precondition
// approved by the coordinator: non-nil Close is required for a usable helper.
// The rejection may surface either as the API error or as result.Err, but must
// be exactly errUnixRemainderClosed and start zero RawWrite calls. udrRaw in
// idle mode counts any Write and returns immediately, so a wrongly started
// worker fails by count instead of hanging.
func TestUnixRemainderNilCloseRejectedWithoutRawWrite(t *testing.T) {
	r, o, w := newUDRRaw(t, udrIdle), newURFObs(t), &urfWritev{script: []urfStep{{n: 8}}}
	c := newUnixRemainderWithDeps(unixRemainderDeps{Raw: r, Writev: w.fn, Close: nil, Observe: o.observe})
	if c == nil {
		t.Fatal("constructor returned nil; want a helper that rejects Submit")
	}

	s := urfSubmit(t, c, urfParts())
	if s.err != errUnixRemainderClosed && s.res.Err != errUnixRemainderClosed {
		t.Fatalf("Submit err=%v result=%+v, want exactly errUnixRemainderClosed", s.err, s.res)
	}
	if s.res.Done || s.res.N != 0 {
		t.Fatalf("rejected Submit reported progress: %+v", s.res)
	}
	if n := r.writes.Load(); n != 0 {
		t.Fatalf("RawWrite started %d time(s) with nil Close", n)
	}
	if n := w.calls.Load(); n != 0 {
		t.Fatalf("writev ran %d time(s) with nil Close", n)
	}
}
