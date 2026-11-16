//go:build linux || darwin || windows

package proofview

import (
	"sync"
	"sync/atomic"
	"syscall"
)

// Private, unwired Unix controlled-readiness remainder controller. It owns no
// native syscalls: the only write path is the injected Writev, invoked from the
// first raw.Write callback of each prepared cycle. See CONTRACT.md.

type unixRemainderConnID uint64
type unixRemainderCycle uint64

type unixRemainderResult struct {
	Conn  unixRemainderConnID
	Cycle unixRemainderCycle
	N     int
	Block bool
	Done  bool
	Err   error
}

type unixRemainderReady struct {
	Conn  unixRemainderConnID
	Cycle unixRemainderCycle
	Err   error
}

type unixRemainder interface {
	Submit(parts [][]byte) (unixRemainderResult, error)
	Kick(token unixRemainderReady, parts [][]byte) (unixRemainderResult, error)
	Ready() <-chan struct{}
	Drain(dst []unixRemainderReady) []unixRemainderReady
	Close() error
}

// unixRemainderError is a comparable enum so the package declares no errors.New
// sentinel values. Zero is not a valid member.
type unixRemainderError uint8

const (
	urErrBusy unixRemainderError = iota + 1
	urErrNotReady
	urErrTooManyVectors
	urErrTooLarge
	urErrClosed
	urErrInvalidNative
	urErrNoAttempt
)

func (e unixRemainderError) Error() string {
	switch e {
	case urErrBusy:
		return "unix remainder: busy"
	case urErrNotReady:
		return "unix remainder: token not ready"
	case urErrTooManyVectors:
		return "unix remainder: too many vectors"
	case urErrTooLarge:
		return "unix remainder: input too large"
	case urErrClosed:
		return "unix remainder: closed"
	case urErrInvalidNative:
		return "unix remainder: invalid native write result"
	case urErrNoAttempt:
		return "unix remainder: raw write returned without an attempt"
	}
	return "unix remainder: unknown error"
}

var (
	errUnixRemainderBusy           error = urErrBusy
	errUnixRemainderNotReady       error = urErrNotReady
	errUnixRemainderTooManyVectors error = urErrTooManyVectors
	errUnixRemainderTooLarge       error = urErrTooLarge
	errUnixRemainderClosed         error = urErrClosed
	errUnixRemainderInvalidNative  error = urErrInvalidNative
	errUnixRemainderNoAttempt      error = urErrNoAttempt
)

const (
	unixRemainderMaxVectors = 8
	unixRemainderMaxBytes   = 64 << 20
)

type unixRemainderState int

const (
	urIdle unixRemainderState = iota
	urAttempting
	urArmed
	urReadyPending
	urKickable
	urClosed
)

type unixRemainderDeps struct {
	Raw     syscall.RawConn
	Writev  func(uintptr, [][]byte) (int, bool, error)
	Close   func() error
	Observe func(string) // nil by default; never called with mu held
}

var unixRemainderConnSeq atomic.Uint64

type unixRemainderWriter struct {
	deps     unixRemainderDeps
	id       unixRemainderConnID
	unusable bool // nil Raw/Writev: every start fails closed
	ready    chan struct{}
	attempts atomic.Uint64 // logical Writev invocations, not EINTR-retried syscalls

	mu           sync.Mutex
	state        unixRemainderState
	cycle        unixRemainderCycle // last assigned
	events       []unixRemainderReady
	readyErr     error // owner-side waitError for the armed cycle; never taken from caller tokens
	closeStarted bool
	closeDone    chan struct{}
	closeErr     error
}

var _ unixRemainder = (*unixRemainderWriter)(nil)

func newUnixRemainderWithDeps(deps unixRemainderDeps) *unixRemainderWriter {
	return &unixRemainderWriter{
		deps:     deps,
		id:       unixRemainderConnID(unixRemainderConnSeq.Add(1)),
		unusable: deps.Raw == nil || deps.Writev == nil || deps.Close == nil,
		ready:    make(chan struct{}, 1),
	}
}

func (w *unixRemainderWriter) observe(step string) {
	if w.deps.Observe != nil {
		w.deps.Observe(step)
	}
}

// NativeWritevAttempts counts authorized logical transfer attempts. An injected
// fake Writev also increments it; it does not attest individual native syscalls.
func (w *unixRemainderWriter) NativeWritevAttempts() uint64 { return w.attempts.Load() }

func (w *unixRemainderWriter) Ready() <-chan struct{} { return w.ready }

func (w *unixRemainderWriter) Submit(parts [][]byte) (unixRemainderResult, error) {
	return w.start(false, unixRemainderReady{}, parts)
}

func (w *unixRemainderWriter) Kick(token unixRemainderReady, parts [][]byte) (unixRemainderResult, error) {
	return w.start(true, token, parts)
}

func (w *unixRemainderWriter) start(kick bool, tok unixRemainderReady, parts [][]byte) (unixRemainderResult, error) {
	w.mu.Lock()
	switch {
	case w.state == urClosed || w.unusable:
		w.mu.Unlock()
		return unixRemainderResult{}, errUnixRemainderClosed
	case w.state == urAttempting || w.state == urArmed || w.state == urReadyPending:
		w.mu.Unlock()
		return unixRemainderResult{}, errUnixRemainderBusy
	case !kick && w.state != urIdle:
		w.mu.Unlock()
		return unixRemainderResult{}, errUnixRemainderBusy
	case kick && (w.state != urKickable || tok.Conn != w.id || tok.Cycle != w.cycle || w.readyErr != nil):
		w.mu.Unlock()
		return unixRemainderResult{}, errUnixRemainderNotReady
	}

	// Domain precheck; API errors leave state (and a Kickable token) intact.
	total, nvec := 0, 0
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		nvec++
		if nvec > unixRemainderMaxVectors {
			w.mu.Unlock()
			return unixRemainderResult{}, errUnixRemainderTooManyVectors
		}
		if len(p) > unixRemainderMaxBytes-total {
			w.mu.Unlock()
			return unixRemainderResult{}, errUnixRemainderTooLarge
		}
		total += len(p)
	}
	if total == 0 {
		if kick {
			w.state = urIdle // valid token consumed by an empty suffix
		}
		w.mu.Unlock()
		return unixRemainderResult{Conn: w.id, Done: true}, nil
	}

	iov := make([][]byte, 0, nvec) // copies slice headers only
	for _, p := range parts {
		if len(p) != 0 {
			iov = append(iov, p)
		}
	}
	w.cycle++
	cycle := w.cycle
	w.state = urAttempting
	w.events = nil
	w.readyErr = nil
	ack := make(chan unixRemainderResult, 1)
	w.mu.Unlock()

	go w.run(cycle, iov, total, ack)
	w.observe("pre_ack_receive")
	return <-ack, nil
}

func (w *unixRemainderWriter) classify(cycle unixRemainderCycle, total, n int, block bool, err error) unixRemainderResult {
	r := unixRemainderResult{Conn: w.id, Cycle: cycle}
	switch {
	case n < 0 || n > total, block && (n != 0 || err != nil):
		r.Err = errUnixRemainderInvalidNative
	case err != nil:
		r.N, r.Err = n, err
	case block:
		r.Block = true
	default:
		r.N, r.Done = n, n == total
	}
	return r
}

// cas moves state from `from` to `to` iff cycle is current. Caller holds mu.
func (w *unixRemainderWriter) cas(cycle unixRemainderCycle, from, to unixRemainderState) bool {
	if w.cycle == cycle && w.state == from {
		w.state = to
		return true
	}
	return false
}

func (w *unixRemainderWriter) run(cycle unixRemainderCycle, iov [][]byte, total int, ack chan unixRemainderResult) {
	var (
		attempted, acked bool
		native           unixRemainderResult
	)
	drop := func() {
		for i := range iov {
			iov[i] = nil
		}
		iov = nil
	}
	cb := func(fd uintptr) bool {
		if attempted {
			return true // readiness: no write, no parts
		}
		attempted = true
		// Authorization linearization point: Closed is checked under mu, then mu
		// is released BEFORE Writev (never held across injected/native code). A
		// Close racing after this check may precede the actual syscall; that is
		// an in-flight attempt protected by RawConn, not a new attempt after Close.
		w.mu.Lock()
		closed := w.state == urClosed
		w.mu.Unlock()
		if closed {
			native = unixRemainderResult{Conn: w.id, Cycle: cycle, Err: errUnixRemainderClosed}
			return true
		}
		w.attempts.Add(1)
		n, block, err := w.deps.Writev(fd, iov)
		native = w.classify(cycle, total, n, block, err)
		if native.Err != nil || native.Done {
			return true
		}
		drop() // payload released before publication
		acked = true
		w.mu.Lock()
		w.cas(cycle, urAttempting, urArmed) // never overwrites a newer or Closed state
		w.mu.Unlock()
		ack <- native // cap 1, one send per cycle
		return false
	}

	rawErr := w.deps.Raw.Write(cb)
	drop()

	if !acked {
		res := native
		switch {
		case !attempted:
			res = unixRemainderResult{Conn: w.id, Cycle: cycle, Err: rawErr}
			if rawErr == nil {
				res.Err = errUnixRemainderNoAttempt
			}
		case rawErr != nil && res.Err == nil:
			res.Done, res.Err = false, rawErr
		}
		w.mu.Lock()
		w.cas(cycle, urAttempting, urIdle)
		w.mu.Unlock()
		ack <- res
	} else {
		w.mu.Lock()
		if w.cycle == cycle && w.state != urClosed && (w.state == urArmed || w.state == urAttempting) {
			w.events = append(w.events, unixRemainderReady{Conn: w.id, Cycle: cycle, Err: rawErr})
			w.readyErr = rawErr
			w.state = urReadyPending
			select {
			case w.ready <- struct{}{}:
			default:
			}
		}
		w.mu.Unlock()
	}
	w.observe("worker_exit")
}

func (w *unixRemainderWriter) Drain(dst []unixRemainderReady) []unixRemainderReady {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state == urClosed || len(w.events) == 0 {
		return dst
	}
	dst = append(dst, w.events...)
	w.events = nil
	if w.state == urReadyPending {
		w.state = urKickable
	}
	return dst
}

func (w *unixRemainderWriter) Close() error {
	w.mu.Lock()
	if w.closeStarted {
		done := w.closeDone
		w.mu.Unlock()
		<-done // shared barrier; no mu held
		return w.closeErr
	}
	w.closeStarted = true
	w.closeDone = make(chan struct{})
	done := w.closeDone
	w.state = urClosed
	w.events = nil
	w.readyErr = nil
	w.mu.Unlock() // never held across deps.Close
	var err error
	if w.deps.Close != nil {
		err = w.deps.Close()
	}
	w.closeErr = err // written before close(done); readers read after <-done
	close(done)
	return err
}
