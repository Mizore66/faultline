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

// Failure guard only. A timeout is never evidence of an outcome.
const urfGuard = 10 * time.Second

func urfRecv(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(urfGuard):
		t.Fatalf("guard expired waiting for %s", what)
	}
}

// urfRaw is a fake syscall.RawConn reproducing internal/poll RawWrite:
// lock -> prepare -> loop{ f(fd) true -> break; wait } -> unlock -> return.
type urfRaw struct {
	preErr  error // returned after lock/prepare, before any callback
	useHold bool  // hold after callback returned true, before unlock

	waitGate chan struct{} // closed by releaseWait: parked waits return
	hold     chan struct{} // closed by releaseHold
	evict    chan struct{} // closed by doEvict (fake Close): waits fail net.ErrClosed

	waitEntered chan struct{} // one send per wait entry
	cbTrue      chan struct{} // one send per callback that returned true
	returned    chan struct{} // one send per RawWrite return (after unlock)
	rawDone     chan struct{} // closed on first RawWrite return

	cbCalls  atomic.Int32
	unlocked atomic.Bool // reset to false at the start of EVERY Write
	once     sync.Once
	wOnce    sync.Once
	hOnce    sync.Once
	eOnce    sync.Once
	mu       sync.Mutex
	log      []string
}

func newURFRaw(t *testing.T) *urfRaw {
	r := &urfRaw{
		waitGate:    make(chan struct{}),
		hold:        make(chan struct{}),
		evict:       make(chan struct{}),
		waitEntered: make(chan struct{}, 8),
		cbTrue:      make(chan struct{}, 8),
		returned:    make(chan struct{}, 8),
		rawDone:     make(chan struct{}),
	}
	t.Cleanup(func() { r.releaseWait(); r.releaseHold(); r.doEvict() }) // never leave gates closed-off
	return r
}

func (r *urfRaw) releaseWait() { r.wOnce.Do(func() { close(r.waitGate) }) }
func (r *urfRaw) releaseHold() { r.hOnce.Do(func() { close(r.hold) }) }
func (r *urfRaw) doEvict()     { r.eOnce.Do(func() { close(r.evict) }) }

func (r *urfRaw) rec(s string) { r.mu.Lock(); r.log = append(r.log, s); r.mu.Unlock() }

// snapshot copies the log under the mutex, for failure strings.
func (r *urfRaw) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.log...)
}

func (r *urfRaw) index(s string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, v := range r.log {
		if v == s {
			return i
		}
	}
	return -1
}

func (r *urfRaw) Control(func(uintptr)) error   { return errors.New("unused") }
func (r *urfRaw) Read(func(uintptr) bool) error { return errors.New("unused") }

func (r *urfRaw) Write(f func(uintptr) bool) error {
	r.unlocked.Store(false)
	r.rec("lock")
	defer func() {
		r.rec("unlock")
		r.unlocked.Store(true)
		r.rec("raw-return")
		r.returned <- struct{}{}
		r.once.Do(func() { close(r.rawDone) })
	}()
	if r.preErr != nil {
		return r.preErr
	}
	r.rec("prepare")
	for {
		r.cbCalls.Add(1)
		if f(42) {
			r.rec("cb-true")
			r.cbTrue <- struct{}{}
			if r.useHold {
				<-r.hold
			}
			return nil
		}
		r.rec("wait")
		r.waitEntered <- struct{}{}
		select {
		case <-r.waitGate:
		case <-r.evict:
			return net.ErrClosed
		}
	}
}

// urfWritev scripts native results; it records only attempt facts.
type urfWritev struct {
	script []urfStep
	calls  atomic.Int32
}
type urfStep struct {
	n     int
	block bool
	err   error
}

func (w *urfWritev) fn(_ uintptr, _ [][]byte) (int, bool, error) {
	i := int(w.calls.Add(1)) - 1
	if i >= len(w.script) {
		return 0, false, errors.New("unexpected extra writev attempt")
	}
	s := w.script[i]
	return s.n, s.block, s.err
}

// urfObs is the private lifecycle observer. It only signals (buffered, never
// blocks) except pre_ack_receive, which blocks on preGate when gatePre is set.
type urfObs struct {
	gatePre    bool
	preGate    chan struct{}
	preAck     chan struct{}
	workerExit chan struct{}
	gOnce      sync.Once
}

func newURFObs(t *testing.T) *urfObs {
	o := &urfObs{preGate: make(chan struct{}), preAck: make(chan struct{}, 8), workerExit: make(chan struct{}, 8)}
	t.Cleanup(o.openPre)
	return o
}
func (o *urfObs) openPre() { o.gOnce.Do(func() { close(o.preGate) }) }
func (o *urfObs) observe(step string) {
	switch step {
	case "pre_ack_receive":
		o.preAck <- struct{}{}
		if o.gatePre {
			<-o.preGate
		}
	case "worker_exit":
		o.workerExit <- struct{}{}
	}
}

func newURFComponent(r *urfRaw, w *urfWritev, o *urfObs, closeFn func() error) *unixRemainderWriter {
	if closeFn == nil {
		closeFn = func() error { return nil }
	}
	return newUnixRemainderWithDeps(unixRemainderDeps{Raw: r, Writev: w.fn, Close: closeFn, Observe: o.observe})
}

type urfOut struct {
	res unixRemainderResult
	err error
}

// urfAsync runs a helper call off the test goroutine; result channel is buffered
// so an abandoned call never leaks a blocked send.
func urfAsync(fn func() (unixRemainderResult, error)) <-chan urfOut {
	ch := make(chan urfOut, 1)
	go func() { res, err := fn(); ch <- urfOut{res, err} }()
	return ch
}

func urfWait(t *testing.T, ch <-chan urfOut, what string) urfOut {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(urfGuard):
		t.Fatalf("guard expired: %s did not return", what)
		return urfOut{}
	}
}

func urfSubmit(t *testing.T, c *unixRemainderWriter, p [][]byte) urfOut {
	t.Helper()
	return urfWait(t, urfAsync(func() (unixRemainderResult, error) { return c.Submit(p) }), "Submit")
}

func urfKick(t *testing.T, c *unixRemainderWriter, tok unixRemainderReady, p [][]byte) urfOut {
	t.Helper()
	return urfWait(t, urfAsync(func() (unixRemainderResult, error) { return c.Kick(tok, p) }), "Kick")
}

func urfClose(t *testing.T, c *unixRemainderWriter) error {
	t.Helper()
	ch := make(chan error, 1)
	go func() { ch <- c.Close() }()
	select {
	case err := <-ch:
		return err
	case <-time.After(urfGuard):
		t.Fatal("guard expired: Close did not return")
		return nil
	}
}

// urfBubble runs a whole test body inside a synctest bubble: every channel,
// goroutine and fake is created inside it, so synctest.Wait gives a positive
// "all other goroutines are durably blocked" edge instead of a timing guess.
func urfBubble(t *testing.T, body func(t *testing.T)) { synctest.Test(t, body) }

func urfParts() [][]byte { return [][]byte{[]byte("abcd"), []byte("efgh")} } // total 8

var _ syscall.RawConn = (*urfRaw)(nil)

// Test 1: partial Submit attempts once; readiness never writes; unlock precedes
// notification; Kick needs Drain and then performs exactly one attempt.
func TestUnixRemainderPartialAttemptsThenReadinessIsWriteFree(t *testing.T) {
	urfBubble(t, testUnixRemainderPartialAttempts)
}

func testUnixRemainderPartialAttempts(t *testing.T) {
	r, o, w := newURFRaw(t), newURFObs(t), &urfWritev{script: []urfStep{{n: 3}, {n: 5}}}
	r.useHold = true // cb#1 returns false (not held); the readiness cb#2 returns true and is held before unlock
	c := newURFComponent(r, w, o, nil)

	sub := urfSubmit(t, c, urfParts())
	res := sub.res
	if sub.err != nil {
		t.Fatalf("Submit API error: %v", sub.err)
	}
	if got := w.calls.Load(); got != 1 {
		t.Fatalf("attempts at Submit return = %d, want 1", got)
	}
	if res.N != 3 || res.Block || res.Done || res.Err != nil || res.Cycle == 0 {
		t.Fatalf("unexpected partial result: %+v", res)
	}
	urfRecv(t, r.waitEntered, "fake wait entry")

	// The worker is parked in the fake wait (happens-before edge): token not yet
	// drained, so Kick must be busy and perform nothing.
	tok := unixRemainderReady{Conn: res.Conn, Cycle: res.Cycle}
	if k := urfKick(t, c, tok, [][]byte{[]byte("defgh")}); k.err != errUnixRemainderBusy {
		t.Fatalf("Kick while Armed err=%v, want errUnixRemainderBusy", k.err)
	}
	if got := w.calls.Load(); got != 1 {
		t.Fatalf("rejected Kick wrote: attempts=%d", got)
	}

	r.releaseWait() // readiness: cb#2 must run without writing
	urfRecv(t, r.cbTrue, "readiness callback returning true")
	// RawWrite is now held between cb true and unlock. Everyone else is durably
	// blocked after Wait, so an early append/notify mutant has already manifested.
	synctest.Wait()
	if r.unlocked.Load() {
		t.Fatal("fake unlocked while held")
	}
	if n := len(c.Ready()); n != 0 {
		t.Fatalf("Ready notified before fd lock release (len=%d); log=%v", n, r.snapshot())
	}
	if d := c.Drain(nil); len(d) != 0 {
		t.Fatalf("event appended before fd lock release: %+v", d)
	}
	r.releaseHold()
	urfRecv(t, c.Ready(), "Ready after unlock")
	if !r.unlocked.Load() {
		t.Fatalf("Ready observed before worker's RawWrite unlock; log=%v", r.snapshot())
	}
	if got := r.cbCalls.Load(); got != 2 {
		t.Fatalf("callback calls = %d, want 2", got)
	}
	if got := w.calls.Load(); got != 1 {
		t.Fatalf("readiness callback wrote: attempts = %d, want 1", got)
	}

	// Ready but undrained: still not kickable.
	if k := urfKick(t, c, tok, [][]byte{[]byte("defgh")}); k.err != errUnixRemainderBusy {
		t.Fatalf("Kick before Drain err=%v, want exactly errUnixRemainderBusy", k.err)
	}
	if got := w.calls.Load(); got != 1 {
		t.Fatalf("undrained Kick wrote: attempts=%d", got)
	}

	toks := c.Drain(nil)
	if len(toks) != 1 || toks[0].Conn != res.Conn || toks[0].Cycle != res.Cycle || toks[0].Err != nil {
		t.Fatalf("Drain tokens = %+v, want one matching %+v", toks, res)
	}
	if got := w.calls.Load(); got != 1 {
		t.Fatalf("Drain wrote: attempts = %d, want 1", got)
	}
	for name, bad := range map[string]unixRemainderReady{
		"stale cycle":  {Conn: res.Conn, Cycle: res.Cycle + 99},
		"foreign conn": {Conn: res.Conn + 1000, Cycle: res.Cycle},
	} {
		if k := urfKick(t, c, bad, [][]byte{[]byte("defgh")}); k.err != errUnixRemainderNotReady {
			t.Fatalf("Kick with %s err=%v, want errUnixRemainderNotReady", name, k.err)
		}
	}
	if got := w.calls.Load(); got != 1 {
		t.Fatalf("rejected tokens wrote: attempts = %d, want 1", got)
	}

	k := urfKick(t, c, toks[0], [][]byte{[]byte("defgh")})
	if k.err != nil {
		t.Fatalf("Kick API error: %v", k.err)
	}
	if got := w.calls.Load(); got != 2 {
		t.Fatalf("attempts after Kick = %d, want exactly 2", got)
	}
	if !k.res.Done || k.res.N != 5 || k.res.Conn != res.Conn || k.res.Cycle <= res.Cycle {
		t.Fatalf("unexpected Kick result: %+v (first %+v)", k.res, res)
	}
}

// Test 2: full Submit returns only after RawWrite unlock, allows next Submit
// (against a real per-Write unlock check), and produces no ready event.
func TestUnixRemainderFullSubmitWaitsForUnlockAndNeverReady(t *testing.T) {
	urfBubble(t, testUnixRemainderFullSubmit)
}

func testUnixRemainderFullSubmit(t *testing.T) {
	r, o, w := newURFRaw(t), newURFObs(t), &urfWritev{script: []urfStep{{n: 8}, {n: 8}}}
	r.useHold = true
	c := newURFComponent(r, w, o, nil)

	done := urfAsync(func() (unixRemainderResult, error) { return c.Submit(urfParts()) })
	urfRecv(t, r.cbTrue, "callback returning true")
	synctest.Wait() // all other goroutines durably blocked: an early-ack Submit would have returned by now
	select {
	case <-done:
		t.Fatal("Submit returned while RawWrite still held the fd lock")
	default:
	}
	r.releaseHold()
	first := urfWait(t, done, "first Submit")
	if !r.unlocked.Load() {
		t.Fatal("first Submit returned before RawWrite unlock")
	}
	if first.err != nil || !first.res.Done || first.res.N != 8 || first.res.Err != nil || first.res.Block {
		t.Fatalf("unexpected full result: %+v err=%v", first.res, first.err)
	}
	urfRecv(t, o.workerExit, "first worker_exit")

	second := urfSubmit(t, c, urfParts()) // Write resets unlocked=false first
	if second.err != nil || !second.res.Done || second.res.Cycle <= first.res.Cycle {
		t.Fatalf("second Submit: %+v err=%v", second.res, second.err)
	}
	if !r.unlocked.Load() {
		t.Fatal("second Submit returned before its own RawWrite unlock")
	}
	urfRecv(t, o.workerExit, "second worker_exit")
	select {
	case <-c.Ready():
		t.Fatal("ready event after full completion")
	default:
	}
	if d := c.Drain(nil); len(d) != 0 {
		t.Fatalf("Drain after full completions = %+v, want empty", d)
	}
}

// Test 3: readiness is published before Submit consumes its ack (forced with
// the pre_ack_receive gate); the ack path must not clobber ReadyPending.
func TestUnixRemainderReadyBeforeAckConsumedKeepsState(t *testing.T) {
	r, o, w := newURFRaw(t), newURFObs(t), &urfWritev{script: []urfStep{{n: 2}, {n: 6}}}
	o.gatePre = true
	r.releaseWait() // every wait returns at once
	c := newURFComponent(r, w, o, nil)

	sub := urfAsync(func() (unixRemainderResult, error) { return c.Submit(urfParts()) })
	urfRecv(t, o.preAck, "pre_ack_receive")
	urfRecv(t, o.workerExit, "worker_exit while Submit still gated before ack receive")
	urfRecv(t, c.Ready(), "Ready published before ack consumed")
	o.openPre()

	first := urfWait(t, sub, "gated Submit")
	if first.err != nil || first.res.N != 2 || first.res.Done || first.res.Err != nil {
		t.Fatalf("Submit: %+v err=%v", first.res, first.err)
	}
	toks := c.Drain(nil)
	if len(toks) != 1 || toks[0].Cycle != first.res.Cycle || toks[0].Conn != first.res.Conn {
		t.Fatalf("Drain = %+v, want token for %+v (state clobbered?)", toks, first.res)
	}
	k := urfKick(t, c, toks[0], [][]byte{[]byte("cdefgh")})
	if k.err != nil {
		t.Fatalf("Kick after early readiness failed (state overwritten?): %v", k.err)
	}
	if !k.res.Done || k.res.N != 6 || w.calls.Load() != 2 {
		t.Fatalf("Kick result %+v attempts=%d", k.res, w.calls.Load())
	}
}

// Test 4: Close while parked evicts the wait via fake Close, waits for RawWrite
// return, needs no peer or Drain; worker exit is a separate positive edge.
func TestUnixRemainderCloseWhileParkedJoinsRawWrite(t *testing.T) {
	r, o, w := newURFRaw(t), newURFObs(t), &urfWritev{script: []urfStep{{n: 1}}}
	var closeCalls atomic.Int32
	var c *unixRemainderWriter
	closeFn := func() error {
		closeCalls.Add(1)
		r.doEvict()
		<-r.rawDone // FD.Close semantics: wait for in-flight RawWrite
		// Loop-side API must be usable while deps.Close runs: Drain takes mu, so a
		// Close that holds mu across deps.Close blocks here and fails the Close guard.
		if d := c.Drain(nil); len(d) != 0 {
			t.Errorf("Drain during Close returned events: %+v", d)
		}
		r.rec("close-return")
		return nil
	}
	c = newURFComponent(r, w, o, closeFn)

	sub := urfSubmit(t, c, urfParts())
	if sub.err != nil || sub.res.N != 1 || sub.res.Done {
		t.Fatalf("Submit: %+v err=%v", sub.res, sub.err)
	}
	urfRecv(t, r.waitEntered, "fake wait entry")

	if err := urfClose(t, c); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("underlying Close calls = %d, want 1", closeCalls.Load())
	}
	if rr, cr := r.index("raw-return"), r.index("close-return"); rr < 0 || cr < 0 || rr > cr {
		t.Fatalf("Close returned before RawWrite returned; log=%v", r.snapshot())
	}
	urfRecv(t, o.workerExit, "worker_exit after Close")
	if err := urfClose(t, c); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("idempotent Close re-invoked underlying Close: calls=%d", closeCalls.Load())
	}
	if got := w.calls.Load(); got != 1 {
		t.Fatalf("Close/evict caused a write: attempts=%d", got)
	}
	if d := c.Drain(nil); len(d) != 0 {
		t.Fatalf("closed component exposed events: %+v", d)
	}
	if s := urfSubmit(t, c, urfParts()); s.err != errUnixRemainderClosed {
		t.Fatalf("Submit after Close err=%v, want errUnixRemainderClosed", s.err)
	}
}

// Test 5: an unread wake must not block the next worker. Drain does NOT consume
// the wake channel. The first wake stays unread (cap 1 full), a second PARTIAL
// cycle via Kick produces a second notify attempt that must coalesce without
// blocking; worker_exit of the second worker is the positive evidence.
func TestUnixRemainderUnreadReadyDoesNotBlockClose(t *testing.T) {
	r, o, w := newURFRaw(t), newURFObs(t), &urfWritev{script: []urfStep{{n: 2}, {n: 2}}}
	r.releaseWait() // waits return at once
	c := newURFComponent(r, w, o, nil)

	s1 := urfSubmit(t, c, urfParts())
	if s1.err != nil || s1.res.N != 2 || s1.res.Done {
		t.Fatalf("Submit: %+v err=%v", s1.res, s1.err)
	}
	urfRecv(t, o.workerExit, "first worker_exit")
	if cap(c.Ready()) != 1 || len(c.Ready()) != 1 {
		t.Fatalf("Ready cap=%d len=%d, want pending unread wake 1/1", cap(c.Ready()), len(c.Ready()))
	}
	toks := c.Drain(nil) // wake deliberately left unread
	if len(toks) != 1 || toks[0].Cycle != s1.res.Cycle {
		t.Fatalf("Drain = %+v, want token for %+v", toks, s1.res)
	}
	if len(c.Ready()) != 1 {
		t.Fatal("Drain consumed the wake channel; contract says it does not")
	}
	k := urfKick(t, c, toks[0], [][]byte{[]byte("cdefgh")})
	if k.err != nil || k.res.N != 2 || k.res.Done {
		t.Fatalf("second partial Kick: %+v err=%v", k.res, k.err)
	}
	urfRecv(t, o.workerExit, "second worker_exit (blocking notify on full channel would hang here)")
	if len(c.Ready()) != 1 {
		t.Fatalf("Ready len=%d after coalesced second wake, want 1", len(c.Ready()))
	}
	if d := c.Drain(nil); len(d) != 1 || d[0].Cycle != k.res.Cycle {
		t.Fatalf("second Drain = %+v, want token for %+v", d, k.res)
	}
	if err := urfClose(t, c); err != nil {
		t.Fatalf("Close with unread Ready = %v", err)
	}
}

// Test 6: raw.Write failing before the callback returns that exact error and
// performs no writev.
func TestUnixRemainderRawWriteErrorBeforeCallbackReturnedExactly(t *testing.T) {
	r, o, w := newURFRaw(t), newURFObs(t), &urfWritev{}
	pre := errors.New("raw write refused before callback")
	r.preErr = pre
	c := newURFComponent(r, w, o, nil)

	s := urfSubmit(t, c, urfParts())
	if s.err != nil {
		t.Fatalf("Submit API error = %v, want nil (error travels in result)", s.err)
	}
	if s.res.Err != pre {
		t.Fatalf("result.Err = %v, want exact %v", s.res.Err, pre)
	}
	if s.res.N != 0 || s.res.Done || s.res.Block {
		t.Fatalf("unexpected result: %+v", s.res)
	}
	if !r.unlocked.Load() {
		t.Fatal("Submit returned before RawWrite unlock")
	}
	urfRecv(t, o.workerExit, "worker_exit")
	if w.calls.Load() != 0 || r.cbCalls.Load() != 0 {
		t.Fatalf("writev=%d callback=%d, want 0/0", w.calls.Load(), r.cbCalls.Load())
	}
}

var (
	_ = errUnixRemainderBusy
	_ = errUnixRemainderNotReady
)
