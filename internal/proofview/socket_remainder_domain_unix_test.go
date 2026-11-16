//go:build linux || darwin || windows

package proofview

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

// Domain/lifecycle tests for the remaining REVIEW D-5 items. They reuse the
// urfWritev, urfObs and urfAsync/urfWait/urfClose/urfRecv helpers from
// socket_remainder_unix_test.go and add a separate RawConn driver (udrRaw).

type udrMode int

const (
	udrIdle      udrMode = iota // any Write call is a violation (counted)
	udrBeforeCb                 // parks after lock/prepare, before the callback, until evicted
	udrWaitError                // cb#1 (expected false), then the wait fails with waitErr
)

type udrRaw struct {
	mode    udrMode
	waitErr error

	entered chan struct{} // one send when the driver reaches its park point
	gate    chan struct{} // closed by release: waitError mode proceeds to fail
	evict   chan struct{} // closed by doEvict: beforeCb mode fails with net.ErrClosed
	rawDone chan struct{} // closed on first Write return

	writes  atomic.Int32
	cbCalls atomic.Int32
	gOnce   sync.Once
	eOnce   sync.Once
	dOnce   sync.Once
}

func newUDRRaw(t *testing.T, mode udrMode) *udrRaw {
	r := &udrRaw{mode: mode, entered: make(chan struct{}, 4), gate: make(chan struct{}),
		evict: make(chan struct{}), rawDone: make(chan struct{})}
	t.Cleanup(func() { r.release(); r.doEvict() })
	return r
}

func (r *udrRaw) release() { r.gOnce.Do(func() { close(r.gate) }) }
func (r *udrRaw) doEvict() { r.eOnce.Do(func() { close(r.evict) }) }

func (r *udrRaw) Control(func(uintptr)) error   { return errors.New("unused") }
func (r *udrRaw) Read(func(uintptr) bool) error { return errors.New("unused") }

func (r *udrRaw) Write(f func(uintptr) bool) error {
	r.writes.Add(1)
	defer r.dOnce.Do(func() { close(r.rawDone) })
	switch r.mode {
	case udrBeforeCb:
		r.entered <- struct{}{}
		<-r.evict
		return net.ErrClosed
	case udrWaitError:
		r.cbCalls.Add(1)
		if f(7) {
			return nil
		}
		r.entered <- struct{}{}
		<-r.gate
		return r.waitErr // waitWrite failure: RawWrite returns it without another callback
	}
	return errors.New("unexpected RawWrite")
}

var _ syscall.RawConn = (*udrRaw)(nil)

func udrComponent(r *udrRaw, w *urfWritev, o *urfObs, closeFn func() error) *unixRemainderWriter {
	if closeFn == nil {
		closeFn = func() error { return nil }
	}
	return newUnixRemainderWithDeps(unixRemainderDeps{Raw: r, Writev: w.fn, Close: closeFn, Observe: o.observe})
}

func udrOne(n int) []byte { return make([]byte, n) }

// Mutant caught: counting/limiting by 9th vector missing, or checking the limit
// after building iovecs/attempting (any Write or writev), or splitting the input.
func TestUnixRemainderDomainNineVectorsRejectedWithoutWrite(t *testing.T) {
	r, o, w := newUDRRaw(t, udrIdle), newURFObs(t), &urfWritev{}
	c := udrComponent(r, w, o, nil)
	parts := make([][]byte, 9)
	for i := range parts {
		parts[i] = udrOne(1)
	}
	s := urfSubmit(t, c, parts)
	if s.err != errUnixRemainderTooManyVectors {
		t.Fatalf("err=%v, want exactly errUnixRemainderTooManyVectors", s.err)
	}
	if w.calls.Load() != 0 || r.writes.Load() != 0 {
		t.Fatalf("rejected input performed work: writev=%d RawWrite=%d", w.calls.Load(), r.writes.Load())
	}
}

// Mutant caught: counting empty vectors toward the 8 limit (12 slots here), or
// rejecting/ splitting an input with exactly 8 nonempty vectors.
func TestUnixRemainderDomainEmptiesIgnoredAndEightAccepted(t *testing.T) {
	r, o, w := newURFRawForDomain(t), newURFObs(t), &urfWritev{script: []urfStep{{n: 8}}}
	c := newUnixRemainderWithDeps(unixRemainderDeps{Raw: r, Writev: w.fn, Close: func() error { return nil }, Observe: o.observe})
	var parts [][]byte
	for i := 0; i < 8; i++ {
		parts = append(parts, udrOne(1), nil, []byte{})
	}
	s := urfSubmit(t, c, parts)
	if s.err != nil || !s.res.Done || s.res.N != 8 || s.res.Err != nil {
		t.Fatalf("8 nonempty + empties: %+v err=%v", s.res, s.err)
	}
	if w.calls.Load() != 1 {
		t.Fatalf("writev attempts=%d, want exactly 1", w.calls.Load())
	}
	urfRecv(t, o.workerExit, "worker_exit")
}

// newURFRawForDomain gives a RawConn that runs the callback once and completes it.
func newURFRawForDomain(t *testing.T) *urfRaw {
	r := newURFRaw(t)
	r.releaseWait()
	return r
}

// Mutant caught: off-by-one limit (>= 64MiB rejected, or 64MiB+1 accepted), limit
// checked after the syscall, or a size computed from a truncated/derived value.
func TestUnixRemainderDomainSixtyFourMiBBoundary(t *testing.T) {
	const max = 64 << 20
	buf := make([]byte, max+1) // one real allocation; no fabricated slices
	r, o, w := newURFRawForDomain(t), newURFObs(t), &urfWritev{script: []urfStep{{n: max}}}
	c := newUnixRemainderWithDeps(unixRemainderDeps{Raw: r, Writev: w.fn, Close: func() error { return nil }, Observe: o.observe})

	if s := urfSubmit(t, c, [][]byte{buf}); s.err != errUnixRemainderTooLarge {
		t.Fatalf("64MiB+1 err=%v, want exactly errUnixRemainderTooLarge", s.err)
	}
	if w.calls.Load() != 0 || r.cbCalls.Load() != 0 {
		t.Fatalf("rejected oversize input performed work: writev=%d cb=%d", w.calls.Load(), r.cbCalls.Load())
	}
	s := urfSubmit(t, c, [][]byte{buf[:max]})
	if s.err != nil || !s.res.Done || s.res.N != max {
		t.Fatalf("exactly 64MiB: %+v err=%v", s.res, s.err)
	}
	urfRecv(t, o.workerExit, "worker_exit for the accepted cycle")
}

// Mutant caught: all-empty input starting a worker/RawWrite/writev, returning
// N!=0 or !Done, or leaving the state non-Idle (the following Submit must work).
func TestUnixRemainderAllEmptyDoneWithoutWorker(t *testing.T) {
	r, o, w := newURFRawForDomain(t), newURFObs(t), &urfWritev{script: []urfStep{{n: 3}}}
	c := newUnixRemainderWithDeps(unixRemainderDeps{Raw: r, Writev: w.fn, Close: func() error { return nil }, Observe: o.observe})
	s := urfSubmit(t, c, [][]byte{nil, {}})
	if s.err != nil || !s.res.Done || s.res.N != 0 || s.res.Err != nil || s.res.Block {
		t.Fatalf("all-empty: %+v err=%v", s.res, s.err)
	}
	// Absence checks, taken after Submit returned synchronously (no worker means no later edge to wait for).
	if w.calls.Load() != 0 || r.cbCalls.Load() != 0 || len(o.workerExit) != 0 {
		t.Fatalf("all-empty did work: writev=%d cb=%d worker_exit=%d", w.calls.Load(), r.cbCalls.Load(), len(o.workerExit))
	}
	n := urfSubmit(t, c, [][]byte{[]byte("abc")})
	if n.err != nil || !n.res.Done || n.res.N != 3 {
		t.Fatalf("Submit after all-empty (state not Idle?): %+v err=%v", n.res, n.err)
	}
	urfRecv(t, o.workerExit, "worker_exit of the real cycle")
}

// Mutant caught: Kick from Idle accepted or reported Busy; Kick after Close not
// preferring Closed (including for a foreign token); either performing work.
func TestUnixRemainderKickIdleAndClosed(t *testing.T) {
	r, o, w := newUDRRaw(t, udrIdle), newURFObs(t), &urfWritev{}
	c := udrComponent(r, w, o, nil)
	tok := unixRemainderReady{Conn: 1, Cycle: 1}
	if k := urfKick(t, c, tok, [][]byte{[]byte("x")}); k.err != errUnixRemainderNotReady {
		t.Fatalf("Kick in Idle err=%v, want exactly errUnixRemainderNotReady", k.err)
	}
	if err := urfClose(t, c); err != nil {
		t.Fatalf("Close: %v", err)
	}
	foreign := unixRemainderReady{Conn: 1 << 60, Cycle: 1 << 40}
	if k := urfKick(t, c, foreign, [][]byte{[]byte("x")}); k.err != errUnixRemainderClosed {
		t.Fatalf("Kick after Close with foreign token err=%v, want exactly errUnixRemainderClosed", k.err)
	}
	if k := urfKick(t, c, tok, [][]byte{[]byte("x")}); k.err != errUnixRemainderClosed {
		t.Fatalf("Kick after Close err=%v, want exactly errUnixRemainderClosed", k.err)
	}
	if w.calls.Load() != 0 || r.writes.Load() != 0 {
		t.Fatalf("rejected Kick did work: writev=%d RawWrite=%d", w.calls.Load(), r.writes.Load())
	}
}

// Mutant caught: dropping/wrapping the waitWrite error in the token, treating an
// error token as Kickable, trusting the caller-supplied token.Err (a copy with
// Err=nil becomes Kickable), or issuing a new attempt for it.
func TestUnixRemainderWaitErrorTokenPreservedAndNotKickable(t *testing.T) {
	r, o, w := newUDRRaw(t, udrWaitError), newURFObs(t), &urfWritev{script: []urfStep{{n: 2}, {n: 6}}}
	r.waitErr = errors.New("waitWrite failed")
	c := udrComponent(r, w, o, nil)

	s := urfSubmit(t, c, urfParts())
	if s.err != nil || s.res.N != 2 || s.res.Done || s.res.Err != nil {
		t.Fatalf("Submit: %+v err=%v", s.res, s.err)
	}
	urfRecv(t, r.entered, "driver wait entry")
	r.release()
	urfRecv(t, o.workerExit, "worker_exit after wait failure")
	urfRecv(t, c.Ready(), "Ready for the error token")
	toks := c.Drain(nil)
	if len(toks) != 1 || toks[0].Err != r.waitErr || toks[0].Cycle != s.res.Cycle || toks[0].Conn != s.res.Conn {
		t.Fatalf("Drain = %+v, want one token with exact Err %v for %+v", toks, r.waitErr, s.res)
	}
	// Adversarial: the caller scrubs its copy of the token. The helper must own the
	// terminal readiness error and still refuse (not trust the caller-supplied Err).
	scrubbed := toks[0]
	scrubbed.Err = nil
	if k := urfKick(t, c, scrubbed, [][]byte{[]byte("cdefgh")}); k.err != errUnixRemainderNotReady {
		t.Fatalf("Kick with Err-cleared copy of error token err=%v, want exactly errUnixRemainderNotReady", k.err)
	}
	if k := urfKick(t, c, toks[0], [][]byte{[]byte("cdefgh")}); k.err != errUnixRemainderNotReady {
		t.Fatalf("Kick with error token err=%v, want exactly errUnixRemainderNotReady", k.err)
	}
	if w.calls.Load() != 1 {
		t.Fatalf("error-token Kick wrote: attempts=%d, want 1", w.calls.Load())
	}
}

// Mutant caught: Close not evicting a Submit parked before its callback, ack
// lost or error replaced/wrapped, a writev issued, or worker_exit skipped on
// the Closed/error path.
func TestUnixRemainderCloseEvictsSubmitBlockedBeforeCallback(t *testing.T) {
	r, o, w := newUDRRaw(t, udrBeforeCb), newURFObs(t), &urfWritev{}
	closeFn := func() error {
		r.doEvict()
		<-r.rawDone // FD.Close waits for the in-flight RawWrite
		return nil
	}
	c := udrComponent(r, w, o, closeFn)

	sub := urfAsync(func() (unixRemainderResult, error) { return c.Submit(urfParts()) })
	urfRecv(t, r.entered, "RawWrite parked before callback")
	if err := urfClose(t, c); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := urfWait(t, sub, "Submit woken by Close")
	if got.err != nil || got.res.Err != net.ErrClosed || got.res.N != 0 || got.res.Done || got.res.Block {
		t.Fatalf("Submit result %+v err=%v, want result.Err == net.ErrClosed, N=0", got.res, got.err)
	}
	urfRecv(t, o.workerExit, "worker_exit on the Closed path")
	if w.calls.Load() != 0 || r.cbCalls.Load() != 0 {
		t.Fatalf("writev=%d cb=%d, want 0/0", w.calls.Load(), r.cbCalls.Load())
	}
}

// Mutant caught: a repeated/concurrent Close that returns early (or nil, or a
// different error) while the first Close is still inside the underlying close,
// i.e. a completion signal that does not depend on fd retirement; a second
// underlying Close call; a first-error that is wrapped or not shared.
// Evidence: inside a synctest bubble the first Close is durably blocked on the
// gate and a correct second Close is durably blocked on the helper's done
// channel (mu released first). After synctest.Wait both result channels must be
// empty; an early-returning mutant is runnable, so Wait stabilizes only after it
// has returned and the nonblocking select catches it. Then the gate opens.
func TestUnixRemainderConcurrentCloseSharesGatedResult(t *testing.T) {
	synctest.Test(t, testUnixRemainderConcurrentClose)
}

func testUnixRemainderConcurrentClose(t *testing.T) {
	r, o, w := newUDRRaw(t, udrIdle), newURFObs(t), &urfWritev{}
	injected := errors.New("injected underlying close error")
	entered := make(chan struct{}, 4)
	gate := make(chan struct{})
	var gOnce sync.Once
	open := func() { gOnce.Do(func() { close(gate) }) }
	t.Cleanup(open)
	var calls atomic.Int32
	closeFn := func() error {
		calls.Add(1)
		entered <- struct{}{}
		<-gate
		return injected
	}
	c := udrComponent(r, w, o, closeFn)

	run := func() <-chan error {
		ch := make(chan error, 1)
		go func() { ch <- c.Close() }()
		return ch
	}
	first := run()
	urfRecv(t, entered, "first Close inside underlying close")
	second := run()
	synctest.Wait() // everything else durably blocked, gate still closed
	select {
	case err := <-first:
		t.Fatalf("first Close returned (%v) before the underlying close completed", err)
	default:
	}
	select {
	case err := <-second:
		t.Fatalf("second Close returned (%v) before the underlying close completed", err)
	default:
	}

	open()
	for i, ch := range []<-chan error{first, second} {
		select {
		case err := <-ch:
			if err != injected {
				t.Fatalf("Close #%d err=%v, want exactly the injected close error", i+1, err)
			}
		case <-time.After(urfGuard):
			t.Fatalf("guard expired: Close #%d did not return after gate release", i+1)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("underlying Close calls=%d, want 1", calls.Load())
	}
}
