//go:build linux || darwin

package proofview

// Native tests for the private Unix controlled-readiness remainder adapter.
// The initial undefined-constructor compile RED is retained separately.
// These tests do not establish Node loop scheduling or HTTP integration.
// The adapter owns the RawConn.Write lifecycle and does one writev per cycle,
// never nested inside an existing Try. Writer side sets NO deadline (RawWrite
// parks in netpoll); only the peer reader has a deadline, as a failure guard.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	urWholeCap   = 80 * time.Second // every command must finish within 90s
	urSmallBuf   = 8192
	urHealthyLen = 8 << 20
	urForcedLen  = 16 << 20
	urMaxCycles  = 100000 // kick loop failure guard
)

// urAttemptCounter attests authorized Writev invocations, not every kernel syscall.
type urAttemptCounter interface{ NativeWritevAttempts() uint64 }

func urBody(n, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(97 + (i*7+i/251+seed*13)%26)
	}
	return b
}

func urFlatten(v [][]byte) []byte {
	var out []byte
	for _, b := range v {
		out = append(out, b...)
	}
	return out
}

func urTotal(v [][]byte) int {
	n := 0
	for _, b := range v {
		n += len(b)
	}
	return n
}

// urTail returns a one-vector view of the bytes after the first n accepted.
func urTail(v [][]byte, n int) [][]byte {
	var out [][]byte
	for _, b := range v {
		if n >= len(b) {
			n -= len(b)
			continue
		}
		out = append(out, b[n:])
		n = 0
	}
	return out
}

func urSHA(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

type urEnv struct {
	t       *testing.T
	guard   chan struct{} // closed at the failure deadline; never a success path
	until   time.Time
	srv     *net.TCPConn
	cli     *net.TCPConn
	w       *unixRemainderWriter
	exits   chan struct{} // one send per worker_exit observation
	started atomic.Int64  // workers whose Submit/Kick returned a nil API error
	seen    int           // exits consumed (test goroutine only)
}

// buf: 0 default buffers, 1 small writer SO_SNDBUF only, 2 both sides small.
func urNewEnv(t *testing.T, buf int) *urEnv {
	t.Helper()
	e := &urEnv{t: t, guard: make(chan struct{}), exits: make(chan struct{}, 1<<17)}
	e.until = time.Now().Add(urWholeCap)
	if td, ok := t.Deadline(); ok && td.Before(e.until) {
		e.until = td
	}
	tm := time.AfterFunc(time.Until(e.until), func() { close(e.guard) })
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		tm.Stop()
		t.Fatalf("listen: %v", err)
	}
	type acc struct {
		c   *net.TCPConn
		err error
	}
	ch := make(chan acc, 1)
	acceptDone := make(chan struct{})
	// Cleanup closes everything first, every join is guard-bounded, and the
	// guard timer is stopped only after the last join.
	t.Cleanup(func() {
		defer tm.Stop()
		_ = ln.Close()
		if e.w != nil {
			cd := make(chan struct{})
			go func() { _ = e.w.Close(); close(cd) }()
			select {
			case <-cd:
			case <-e.guard:
				t.Errorf("cleanup: adapter Close did not return")
			}
		}
		if e.srv != nil {
			_ = e.srv.Close()
		}
		if e.cli != nil {
			_ = e.cli.Close()
		}
		select {
		case <-acceptDone:
		case <-e.guard:
			t.Errorf("cleanup: accept goroutine did not exit")
		}
		select {
		case a := <-ch:
			if a.c != nil {
				_ = a.c.Close()
			}
		default:
		}
		for int64(e.seen) < e.started.Load() {
			select {
			case <-e.exits:
				e.seen++
			case <-e.guard:
				t.Errorf("cleanup: %d of %d workers never exited", e.started.Load()-int64(e.seen), e.started.Load())
				return
			}
		}
	})
	tl := ln.(*net.TCPListener)
	_ = tl.SetDeadline(e.until)
	go func() {
		defer close(acceptDone)
		c, err := tl.AcceptTCP()
		ch <- acc{c, err}
	}()
	nc, derr := (&net.Dialer{Deadline: e.until}).Dial("tcp4", ln.Addr().String())
	if derr != nil {
		t.Fatalf("dial: %v", derr)
	}
	e.cli = nc.(*net.TCPConn)
	a := <-ch // bounded by the listener deadline
	if a.err != nil {
		t.Fatalf("accept: %v", a.err)
	}
	e.srv = a.c
	if buf >= 1 && e.srv.SetWriteBuffer(urSmallBuf) != nil {
		t.Fatalf("set small SO_SNDBUF")
	}
	if buf >= 2 && e.cli.SetReadBuffer(urSmallBuf) != nil {
		t.Fatalf("set small SO_RCVBUF")
	}
	return e
}

// adapt builds the adapter and installs the exit observer before any worker
// can start (the ordinary constructor leaves Observe nil; no native fake).
func (e *urEnv) adapt(conn *net.TCPConn) (*unixRemainderWriter, error) {
	w, err := newUnixSocketRemainder(conn) // RED: undefined until implemented
	if err != nil || w == nil {
		return w, err
	}
	w.deps.Observe = func(step string) {
		if step == "worker_exit" {
			e.exits <- struct{}{}
		}
	}
	e.w = w
	return w, nil
}

func (e *urEnv) attempts() uint64 {
	e.t.Helper()
	c, ok := any(e.w).(urAttemptCounter)
	if !ok {
		e.t.Fatalf("API gap: adapter lacks NativeWritevAttempts(); native attempt counts cannot be attested")
	}
	return c.NativeWritevAttempts()
}

func (e *urEnv) wantAttempts(want uint64, when string) {
	e.t.Helper()
	if got := e.attempts(); got != want {
		e.t.Fatalf("%s: native attempts = %d, want %d", when, got, want)
	}
}

func (e *urEnv) awaitExit() {
	e.t.Helper()
	select {
	case <-e.exits:
		e.seen++
	case <-e.guard:
		e.t.Fatalf("worker_exit never observed")
	}
}

type urCallRes struct {
	r   unixRemainderResult
	err error
}

// call runs a possibly blocking Submit/Kick off-goroutine with a buffered
// result; the guard bounds the wait. A worker is counted only after the API
// returned a nil error (no worker exists otherwise).
func (e *urEnv) call(f func() (unixRemainderResult, error)) (unixRemainderResult, error) {
	e.t.Helper()
	ch := make(chan urCallRes, 1)
	go func() {
		r, err := f()
		if err == nil {
			e.started.Add(1)
		}
		ch <- urCallRes{r, err}
	}()
	select {
	case c := <-ch:
		return c.r, c.err
	case <-e.guard:
		e.t.Fatalf("Submit/Kick did not return before the failure guard")
		return unixRemainderResult{}, nil
	}
}

func (e *urEnv) submit(parts [][]byte) unixRemainderResult {
	e.t.Helper()
	r, err := e.call(func() (unixRemainderResult, error) { return e.w.Submit(parts) })
	if err != nil {
		e.t.Fatalf("Submit API error: %v", err)
	}
	return r
}

func (e *urEnv) kick(tok unixRemainderReady, parts [][]byte) unixRemainderResult {
	e.t.Helper()
	r, err := e.call(func() (unixRemainderResult, error) { return e.w.Kick(tok, parts) })
	if err != nil {
		e.t.Fatalf("Kick API error: %v", err)
	}
	return r
}

// awaitToken waits for a drained, current readiness token. Ready is a
// coalescing wake: an empty Drain after a stale wake just waits for the next.
func (e *urEnv) awaitToken() unixRemainderReady {
	e.t.Helper()
	for {
		select {
		case <-e.w.Ready():
		case <-e.guard:
			e.t.Fatalf("Ready never fired")
		}
		switch evs := e.w.Drain(nil); len(evs) {
		case 0:
		case 1:
			return evs[0]
		default:
			e.t.Fatalf("Drain returned %d events for one armed cycle", len(evs))
		}
	}
}

type urReadRes struct {
	data []byte
	err  error
}

func (e *urEnv) startReader() func() urReadRes {
	e.t.Helper()
	ch := make(chan urReadRes, 1)
	_ = e.cli.SetReadDeadline(e.until)
	go func() {
		d, err := io.ReadAll(e.cli) // nil error means clean EOF
		ch <- urReadRes{d, err}
	}()
	var once sync.Once
	var res urReadRes
	join := func() urReadRes { once.Do(func() { res = <-ch }); return res }
	e.t.Cleanup(func() { _ = e.cli.Close(); join() })
	return join
}

// initialPartial submits once and requires a genuine partial (inconclusive otherwise).
func (e *urEnv) initialPartial(parts [][]byte) unixRemainderResult {
	e.t.Helper()
	total := urTotal(parts)
	r := e.submit(parts)
	e.t.Logf("initial Submit: N=%d Block=%v Done=%v Err=%v total=%d", r.N, r.Block, r.Done, r.Err, total)
	if r.Err != nil {
		e.t.Fatalf("initial Submit failed: %v", r.Err)
	}
	if r.Done || r.N >= total || (r.N == 0 && !r.Block) {
		e.t.Fatalf("INCONCLUSIVE: initial Submit was not partial/EAGAIN (N=%d Block=%v Done=%v)", r.N, r.Block, r.Done)
	}
	e.wantAttempts(1, "after initial Submit")
	return r
}

func TestUnixSocketRemainderNative(t *testing.T) {
	t.Run("healthy_full_drain_exact_order", urHealthy)
	t.Run("close_with_peer_paused_drops_suffix", urClosePaused)
	t.Run("closed_before_start", urClosedBeforeStart)
}

func urHealthy(t *testing.T) {
	e := urNewEnv(t, 1) // writer SO_SNDBUF small only; peer default so 8 MiB drains fast
	w, err := e.adapt(e.srv)
	if err != nil || w == nil {
		t.Fatalf("newUnixSocketRemainder: w=%v err=%v", w, err)
	}
	parts := [][]byte{urBody(61, 1), urBody(urHealthyLen-100, 2), urBody(39, 3)}
	want, total := urFlatten(parts), urTotal(parts)

	r := e.initialPartial(parts) // peer has not read: reader starts AFTER this ack
	accepted, expect := r.N, uint64(1)
	rd := e.startReader()
	for cycles := 0; !r.Done; cycles++ {
		if cycles >= urMaxCycles {
			t.Fatalf("failure guard: %d cycles, accepted %d of %d", cycles, accepted, total)
		}
		tok := e.awaitToken()
		if tok.Err != nil || tok.Conn != r.Conn || tok.Cycle != r.Cycle {
			t.Fatalf("token %+v does not match armed cycle %+v", tok, r)
		}
		e.awaitExit() // Ready and worker exit both arrived
		e.wantAttempts(expect, "ready+exit before Kick (no extra authorized Writev)")
		r = e.kick(tok, urTail(parts, accepted))
		if r.Err != nil {
			t.Fatalf("Kick result error: %v", r.Err)
		}
		expect++
		e.wantAttempts(expect, "after Kick") // exactly one attempt per cycle
		accepted += r.N
	}
	e.awaitExit() // final Done worker
	if accepted != total {
		t.Fatalf("accepted %d of %d bytes at Done", accepted, total)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	res := rd()
	if res.err != nil || !bytes.Equal(res.data, want) {
		t.Errorf("stream: err=%v got %d bytes sha %s, want %d bytes sha %s",
			res.err, len(res.data), urSHA(res.data), len(want), urSHA(want))
	}
}

func urClosePaused(t *testing.T) {
	e := urNewEnv(t, 2)
	w, err := e.adapt(e.srv)
	if err != nil || w == nil {
		t.Fatalf("newUnixSocketRemainder: w=%v err=%v", w, err)
	}
	parts := [][]byte{urBody(4<<20, 4), urBody(4<<20, 5), urBody(4<<20, 6), urBody(4<<20, 7)}
	want, total := urFlatten(parts), urTotal(parts)
	if total != urForcedLen {
		t.Fatalf("fixture total %d", total)
	}
	e.initialPartial(parts) // one 16MiB attempt, no enlarge, no retry

	// Close BEFORE the peer reads anything; it must not wait on the peer.
	closeDone := make(chan error, 1)
	go func() { closeDone <- w.Close() }()
	e.awaitExit() // worker returned; readiness versus Close eviction is not distinguished
	select {
	case err := <-closeDone:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-e.guard:
		t.Fatalf("Close did not return while peer paused")
	}
	if evs := w.Drain(nil); len(evs) != 0 {
		t.Errorf("Drain after Close: %d events", len(evs))
	}
	if _, err := e.call(func() (unixRemainderResult, error) { return w.Kick(unixRemainderReady{}, nil) }); err != errUnixRemainderClosed {
		t.Errorf("Kick after Close error = %v, want errUnixRemainderClosed", err)
	}
	if _, err := e.call(func() (unixRemainderResult, error) { return w.Submit(parts) }); err != errUnixRemainderClosed {
		t.Errorf("Submit after Close error = %v, want errUnixRemainderClosed", err)
	}
	e.wantAttempts(1, "after Close (suffix dropped, nothing written)")

	// Only now does the peer read; the unsent suffix was dropped.
	res := e.startReader()()
	// Source-qualified end states: clean EOF (FIN after Close), a closed-conn
	// error, or ECONNRESET. A timeout (guard expiry) never passes.
	var ne net.Error
	switch {
	case res.err == nil, errors.Is(res.err, net.ErrClosed), errors.Is(res.err, syscall.ECONNRESET):
	default:
		t.Errorf("reader end state %v is not nil EOF/ErrClosed/ECONNRESET", res.err)
	}
	if errors.As(res.err, &ne) && ne.Timeout() {
		t.Errorf("reader timed out: %v", res.err)
	}
	if len(res.data) >= total {
		t.Errorf("peer received %d bytes, want an incomplete prefix of %d", len(res.data), total)
	}
	if !bytes.HasPrefix(want, res.data) {
		t.Errorf("peer bytes (%d, sha %s) are not a prefix of the input", len(res.data), urSHA(res.data))
	}
}

func urClosedBeforeStart(t *testing.T) {
	e := urNewEnv(t, 0)
	if err := e.srv.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	w, err := e.adapt(e.srv)
	if err != nil {
		if !errors.Is(err, net.ErrClosed) {
			t.Errorf("constructor error = %v, want errors.Is net.ErrClosed", err)
		}
		return // no adapter, hence no native attempt is possible
	}
	r, serr := e.call(func() (unixRemainderResult, error) { return w.Submit([][]byte{urBody(4096, 8)}) })
	if serr == nil {
		e.awaitExit() // a worker ran RawWrite on the closed fd and must exit
	}
	if !errors.Is(serr, net.ErrClosed) && !errors.Is(r.Err, net.ErrClosed) {
		t.Errorf("Submit on closed conn: api=%v result.Err=%v, want errors.Is net.ErrClosed", serr, r.Err)
	}
	if r.N != 0 || r.Done || r.Block {
		t.Errorf("Submit on closed conn result = %+v, want N=0 and neither Done nor Block", r)
	}
	e.wantAttempts(0, "closed before start")
}
