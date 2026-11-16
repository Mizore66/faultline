//go:build linux || darwin || windows

package proofview

// PRIVATE physical helper for one overlapped, event-based WSASend on a
// syscall.RawConn. Scope: submit / result / pins / private low-bit event /
// cancel / fence ONLY. It never calls TCPConn.Close or Try, never touches HTTP,
// logical lengths, callback queues, timers or os.Exit, and never retries,
// reposts or cancels on its own.
//
// Caller preconditions (not checkable here):
//   - the caller owns the connection exclusively (Go READS are allowed; no Go
//     Write/Try while an op is pending);
//   - the submitting goroutine stays runtime.LockOSThread'd until Done() is
//     closed or the op is otherwise definitely retired (Windows cancels I/O
//     started by a thread when that thread exits). If a panic escapes Submit
//     after potential native ownership, the helper quarantines the op and
//     re-panics; the caller MUST keep the thread alive and MUST NOT treat the
//     panic as permission to release native resources;
//   - FIONBIO was set earlier by the writer constructor.
//
// Terminals are mutually exclusive:
//   - Done:          native retirement proven AND Unpin AND CloseHandle succeeded;
//   - FenceFailed:   ONLY when native retirement is UNKNOWN (Snapshot.Retired
//     false); everything stays retained;
//   - ReleaseFailed: native retirement IS established (Snapshot.Retired true: a
//     real fence, FenceProven, or a certain no-I/O outcome) but cleanup did not
//     finish: CloseEvent error, or ANY interruption/panic after retirement,
//     including between stampFence and the release claim. Callers must inspect
//     Unpinned/EventClosed/ReleaseErr/TerminalReason; nothing is assumed. The op
//     stays registered as the strong owner of whatever was not released. There is
//     no recovery path and no automatic retry. The submitting thread may be
//     unlocked only when Retired is true.
//
// Cancel before the post call has returned (during setup, the "registered"
// observer, or while WSASend is still executing) returns already_done with NO
// native use. That value is a PREPUBLICATION NO-OP, not a completion or
// retirement acknowledgement: callers MUST NOT interpret it as such. They may
// retry Cancel after Submit returns; the helper never cancels automatically.
// A lease requires mayOwn, postReturned and a published OVERLAPPED, all checked
// under the op lock. If Send panics, postReturned is still published, so Cancel
// on the quarantined uncertain op is allowed.
//
// Terminal ownership protocol (fix1): under the op lock exactly one party may
// claim the op. release() claims it (releasing=true) only if no terminal is set;
// quarantine() sets FenceFailed only if no terminal is set AND release has not
// been claimed. stampFence() and markNoIO() refuse once a terminal is set. So a
// quarantined op can never later advance, unpin, close or unregister, and a
// releasing op can never be quarantined over (a concurrent panic only appends a
// Fault; the releasing goroutine owns the terminal).

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
)

// windowsSocketDomainError is the typed sentinel for an invalid part list. The
// canonical text is produced by its Error method (a runtime producer inside a
// function body). errors.Is identity is the single pointer held in
// errWindowsSocketWriteDomain; wrapping with %w is unchanged.
type windowsSocketDomainError struct{ _ byte } // non-zero size: pointer identity is unique

func (*windowsSocketDomainError) Error() string { return "windows socket write: invalid part list" }

var errWindowsSocketWriteDomain error = &windowsSocketDomainError{}

const (
	windowsSocketWaitObject0  = 0
	windowsSocketWaitTimeout  = 258
	windowsSocketErrNotFound  = syscall.Errno(1168) // ERROR_NOT_FOUND from CancelIoEx
	windowsSocketErrIOPending = syscall.Errno(997)  // ERROR_IO_PENDING / WSA_IO_PENDING from WSASend
)

// windowsSocketHandle carries a Windows SOCKET or event HANDLE as a plain
// uintptr so the state machine below is portable. Only the platform adapter
// converts it to a real native handle type.
type windowsSocketHandle uintptr

// windowsSocketPost is a numeric status. String() preserves the exact rendered
// names that earlier versions stored as string values (receipts, faults, logs).
type windowsSocketPost uint8

func (p windowsSocketPost) String() string {
	switch p {
	case windowsSocketPostNotPosted:
		return "not_posted"
	case windowsSocketPostNative0:
		return "native0"
	case windowsSocketPostNative997:
		return "native997"
	case windowsSocketPostError:
		return "native_error"
	case windowsSocketPostUnclassified:
		return "native_unclassified"
	}
	return fmt.Sprintf("windowsSocketPost(%d)", uint8(p))
}

const (
	windowsSocketPostNotPosted windowsSocketPost = iota
	windowsSocketPostNative0
	windowsSocketPostNative997
	windowsSocketPostError
	// windowsSocketPostUnclassified: WSASend returned a non-nil error that is not a
	// non-zero syscall.Errno. Native ownership is uncertain: the op is quarantined
	// (FenceFailed), never treated as no-I/O. PostErrno stays 0.
	windowsSocketPostUnclassified
)

type windowsSocketCancel string

const (
	windowsSocketCancelIssued       windowsSocketCancel = "cancel_issued"
	windowsSocketCancelNotFound     windowsSocketCancel = "not_found_completed_race"
	windowsSocketCancelAlreadyDone  windowsSocketCancel = "already_done"
	windowsSocketCancelControlClose windowsSocketCancel = "control_closed_no_cancel"
	windowsSocketCancelFaultControl windowsSocketCancel = "fault_control"
	windowsSocketCancelFaultCancel  windowsSocketCancel = "fault_cancel"
)

type windowsSocketResultSource string

const (
	windowsSocketResultNone          windowsSocketResultSource = ""
	windowsSocketResultPostQuery     windowsSocketResultSource = "post_query"
	windowsSocketResultFenceQuery    windowsSocketResultSource = "fence_query"
	windowsSocketResultControlClosed windowsSocketResultSource = "control_closed"
	windowsSocketResultControlFault  windowsSocketResultSource = "control_fault"
)

type windowsSocketWriteResult struct {
	Source     windowsSocketResultSource
	OK         bool
	N          uint32
	Errno      syscall.Errno
	ControlErr string
	// Err is set when the native result query returned FALSE with errno 0 or
	// otherwise could not be classified; Errno is never fabricated.
	Err string
}

type windowsSocketCounters struct{ EventsCreated, EventsClosed, UnpinOps, OpsPosted int64 }

type windowsSocketWriteSnapshot struct {
	Post                                       windowsSocketPost
	PostErrno                                  syscall.Errno
	Parts                                      int
	Bytes                                      uint64
	PinCalls                                   int
	WSASendCalls                               int
	EventSignaled, EventClosed                 bool
	EventPollErr                               string
	Unpinned                                   bool
	FenceSeq, UnpinSeq, EventCloseSeq, DoneSeq uint64
	Result                                     windowsSocketWriteResult
	Faults                                     []string
	ReleaseErr                                 string
	Terminal, TerminalReason                   string // "", done, fence_failed, release_failed
	FenceProven                                bool   // actual native fence observed (not markNoIO)
	Retired                                    bool   // native fence OR certain no-I/O
}

// ---- injectable native seam (per runtime instance) ----

// windowsSocketPinResult is the RAW outcome of one Pin call. The adapter never
// formats a message: it reports either an error or the recovered panic value,
// and windowsSocketPinError (core) builds the diagnostic.
type windowsSocketPinResult struct {
	Err      error
	Panic    any
	Panicked bool
}

func windowsSocketPinError(res windowsSocketPinResult) error {
	if res.Panicked {
		return fmt.Errorf("runtime.Pinner.Pin panic: %v", res.Panic)
	}
	return res.Err
}

type windowsSocketPinner interface {
	Pin(any) windowsSocketPinResult
	Unpin()
}

// windowsSocketPostShapeError validates the adapter's NewPost output. It is
// pure and runs before mayOwn, Send or any Pin call. It rejects a nil or typed
// nil post, a pin-target count other than exactly 2 (OVERLAPPED, WSABUF[0]),
// and any pin target that is not a non-nil pointer (scalars, nil, typed nil).
func windowsSocketPostShapeError(post any, pinTargets []any) error {
	if !windowsSocketNonNilPointer(post) {
		return errors.New("NewPost returned a nil or non-pointer post")
	}
	if len(pinTargets) != 2 {
		return fmt.Errorf("NewPost returned %d adapter pin targets, want exactly 2", len(pinTargets))
	}
	for i, t := range pinTargets {
		if !windowsSocketNonNilPointer(t) {
			return fmt.Errorf("NewPost pin target %d is nil or not a pointer", i)
		}
	}
	return nil
}

func windowsSocketNonNilPointer(x any) bool {
	if x == nil {
		return false
	}
	v := reflect.ValueOf(x)
	return v.Kind() == reflect.Pointer && !v.IsNil()
}

// windowsSocketCreateEventError classifies the RAW CreateEventW outcome: ok is
// whether the call returned a non-zero handle, callErr is the raw last-error
// value reported by the call.
func windowsSocketCreateEventError(ok bool, callErr error) error {
	if ok {
		return nil
	}
	var en syscall.Errno
	if x, isErrno := callErr.(syscall.Errno); isErrno {
		en = x
	}
	if en == 0 {
		return errors.New("CreateEventW failed without errno")
	}
	return fmt.Errorf("CreateEventW failed: %w", en)
}

// windowsSocketResolveError formats a raw proc-resolution failure.
func windowsSocketResolveError(name string, err error) error {
	return fmt.Errorf("resolve %s: %w", name, err)
}

type windowsSocketNativeResult struct {
	OK    bool
	N     uint32
	Errno syscall.Errno
}

// windowsSocketNativeAPI is the platform seam. It returns RAW values only
// (handles, errnos, errors, recovered panic values); every diagnostic message
// is built by the core in this file.
//
// post is an opaque native post object created by NewPost. It owns the native
// OVERLAPPED and WSABUF array. The core never inspects it, keeps it strongly
// referenced until release, and passes the same value to Send, Result and
// CancelIoEx, so the native pointer identity is stable for the whole lease.
type windowsSocketNativeAPI interface {
	// Resolve returns the name of the failing proc (or "") and its raw error.
	Resolve() (name string, err error)
	// CreateEvent returns the raw handle, whether the call succeeded, and the raw
	// call error; the core classifies it with windowsSocketCreateEventError.
	CreateEvent() (h windowsSocketHandle, ok bool, callErr error)
	// NewPost builds the post object for taggedEvent (event handle | 1, computed
	// by the core) and the parts. pinTargets are the adapter-owned pin targets in
	// order (OVERLAPPED, then the WSABUF array first element); the core appends
	// one payload pin target per part afterwards. CONTRACT, enforced by the core
	// (windowsSocketPostShapeError) before any ownership is assumed: post is a
	// non-nil pointer-kind value (a typed nil is rejected), pinTargets has exactly
	// two entries, and each is a non-nil pointer. The same post value must be
	// accepted back by Send, Result and CancelIoEx.
	NewPost(taggedEvent uintptr, parts [][]byte) (post any, pinTargets []any)
	Send(h windowsSocketHandle, post any) error
	WaitEvent(ev windowsSocketHandle) (status uint32, err error)
	PollEvent(ev windowsSocketHandle) (status uint32, err error)
	Result(h windowsSocketHandle, post any) windowsSocketNativeResult
	CancelIoEx(h windowsSocketHandle, post any) error
	CloseEvent(ev windowsSocketHandle) error
}

type windowsSocketWriteDeps struct {
	API       windowsSocketNativeAPI
	NewPinner func() windowsSocketPinner
	Observe   func(op *windowsSocketWrite, step string) // nil in production; called without any op lock
}

// ---- runtime ----

type windowsSocketWriteRuntime struct {
	deps windowsSocketWriteDeps
	seq  atomic.Uint64

	created, closed, unpins, posted atomic.Int64

	mu   sync.Mutex // guards live only; never held while taking an op lock
	live map[*windowsSocketWrite]struct{}
}

func newWindowsSocketWriteRuntime(deps windowsSocketWriteDeps) *windowsSocketWriteRuntime {
	if deps.API == nil {
		deps.API = newWindowsSocketDefaultAPI()
	}
	if deps.NewPinner == nil {
		deps.NewPinner = newWindowsSocketRuntimePinner
	}
	return &windowsSocketWriteRuntime{deps: deps, live: map[*windowsSocketWrite]struct{}{}}
}

// The single production runtime. Never replaced.
var defaultWindowsSocketWriteRuntime = newWindowsSocketWriteRuntime(windowsSocketWriteDeps{})

func submitWindowsSocketWrite(raw syscall.RawConn, parts [][]byte) (*windowsSocketWrite, error) {
	return defaultWindowsSocketWriteRuntime.Submit(raw, parts)
}

func windowsSocketWriteCounters() windowsSocketCounters {
	return defaultWindowsSocketWriteRuntime.Counters()
}

func (rt *windowsSocketWriteRuntime) Counters() windowsSocketCounters {
	return windowsSocketCounters{
		EventsCreated: rt.created.Load(),
		EventsClosed:  rt.closed.Load(),
		UnpinOps:      rt.unpins.Load(),
		OpsPosted:     rt.posted.Load(),
	}
}

func (rt *windowsSocketWriteRuntime) LiveCount() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return len(rt.live)
}

func (rt *windowsSocketWriteRuntime) LiveOps() []*windowsSocketWrite {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([]*windowsSocketWrite, 0, len(rt.live))
	for w := range rt.live {
		out = append(out, w)
	}
	return out
}

func (rt *windowsSocketWriteRuntime) register(w *windowsSocketWrite) {
	rt.mu.Lock()
	rt.live[w] = struct{}{}
	rt.mu.Unlock()
}

func (rt *windowsSocketWriteRuntime) unregister(w *windowsSocketWrite) {
	rt.mu.Lock()
	delete(rt.live, w)
	rt.mu.Unlock()
}

func (rt *windowsSocketWriteRuntime) nextSeq() uint64 { return rt.seq.Add(1) }

// windowsSocketWriteDomain validates part lengths in uint64: non-empty list,
// every part non-empty, each part and the total <= MaxUint32 (DWORD).
func windowsSocketWriteDomain(partLens []uint64) (uint64, error) {
	const maxDWORD = uint64(^uint32(0))
	if len(partLens) == 0 {
		return 0, fmt.Errorf("%w: no parts", errWindowsSocketWriteDomain)
	}
	var total uint64
	for i, l := range partLens {
		switch {
		case l == 0:
			return 0, fmt.Errorf("%w: part %d is empty", errWindowsSocketWriteDomain, i)
		case l > maxDWORD:
			return 0, fmt.Errorf("%w: part %d length %d exceeds DWORD", errWindowsSocketWriteDomain, i, l)
		case total > maxDWORD-l:
			return 0, fmt.Errorf("%w: total exceeds DWORD at part %d", errWindowsSocketWriteDomain, i)
		}
		total += l
	}
	return total, nil
}

// ---- op ----

const (
	windowsSocketTermNone = iota
	windowsSocketTermDone
	windowsSocketTermFenceFailed
	windowsSocketTermReleaseFailed
)

const (
	relPhaseClaimed  = iota // release claimed, Unpin not yet completed
	relPhaseUnpinned        // Unpin completed (or no pin to release)
	relPhaseClosing         // CloseEvent in progress / attempted
	relPhaseClosed          // CloseEvent succeeded, Done not yet closed
)

type windowsSocketWrite struct {
	rt  *windowsSocketWriteRuntime
	raw syscall.RawConn

	done, fenceFailed, releaseFailed chan struct{}

	cbCalls atomic.Int32

	mu   sync.Mutex
	cond *sync.Cond

	// Native-owned memory; strongly held (here and via rt.live) until released.
	parts  [][]byte
	native any // opaque native post object (owns OVERLAPPED and WSABUF array); nil until published
	ev     windowsSocketHandle

	pinner windowsSocketPinner
	pinOK  bool // at least one Pin call succeeded

	haveEvent  bool
	pollable   bool
	signaled   bool
	pollErr    string
	post       windowsSocketPost
	postErrno  syscall.Errno
	nParts     int
	bytes      uint64
	pinCalls   int
	sendCalls  int
	eventClose bool
	unpinned   bool
	fenceSeq   uint64
	unpinSeq   uint64
	closeSeq   uint64
	doneSeq    uint64
	result     windowsSocketWriteResult
	faults     []string
	releaseErr string
	termReason string

	mayOwn       bool // set before the WSASend crossing
	fenceProven  bool
	postReturned bool // set under the op lock once the Send call has returned or panicked
	retired      bool // native retirement proven (fence observed, or certain no I/O)
	releasing    bool
	relPhase     int
	leases       int
	drainFired   bool
	terminal     int
}

func (w *windowsSocketWrite) Done() <-chan struct{}          { return w.done }
func (w *windowsSocketWrite) FenceFailed() <-chan struct{}   { return w.fenceFailed }
func (w *windowsSocketWrite) ReleaseFailed() <-chan struct{} { return w.releaseFailed }

func (w *windowsSocketWrite) with(f func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f()
}

func (w *windowsSocketWrite) faultLocked(msg string) { w.faults = append(w.faults, msg) }
func (w *windowsSocketWrite) fault(msg string)       { w.with(func() { w.faultLocked(msg) }) }

func (w *windowsSocketWrite) observe(step string) {
	if obs := w.rt.deps.Observe; obs != nil {
		obs(w, step)
	}
}

// quarantine records a Fault and, only if nobody owns a terminal yet and release
// has not been claimed, makes a terminal final: FenceFailed when native
// retirement is unknown, ReleaseFailed when it is already established. It never
// unpins, closes, drops the registry entry or exits.
func (w *windowsSocketWrite) quarantine(reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.faultLocked(reason)
	if w.terminal != windowsSocketTermNone || w.releasing {
		return
	}
	w.termReason = reason
	if w.retired {
		w.terminal = windowsSocketTermReleaseFailed
		w.releaseErr = reason
		close(w.releaseFailed)
		return
	}
	w.terminal = windowsSocketTermFenceFailed
	close(w.fenceFailed)
}

// stampFence records the native fence. It refuses (false) on a quarantined op.
func (w *windowsSocketWrite) stampFence() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.terminal != windowsSocketTermNone {
		return false
	}
	w.retired = true
	w.fenceProven = true
	w.signaled = true
	w.fenceSeq = w.rt.nextSeq()
	return true
}

// markNoIO records a certain no-I/O outcome (WSASend error, setup rollback).
func (w *windowsSocketWrite) markNoIO() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.terminal != windowsSocketTermNone || w.mayOwnIOLocked() {
		return false
	}
	w.retired = true
	return true
}

// mayOwnIOLocked: native I/O may exist unless the post was a definite error or
// the op never reached the post.
func (w *windowsSocketWrite) mayOwnIOLocked() bool {
	return w.mayOwn && w.post != windowsSocketPostError
}

func (w *windowsSocketWrite) Snapshot() windowsSocketWriteSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.haveEvent && !w.eventClose && w.pollable {
		st, err := w.rt.deps.API.PollEvent(w.ev)
		switch {
		case err != nil:
			w.pollErr = err.Error()
		case st == windowsSocketWaitObject0:
			w.signaled = true
		case st == windowsSocketWaitTimeout:
		default:
			w.pollErr = fmt.Sprintf("unexpected poll status %d", st)
		}
	}
	term := ""
	switch w.terminal {
	case windowsSocketTermDone:
		term = "done"
	case windowsSocketTermFenceFailed:
		term = "fence_failed"
	case windowsSocketTermReleaseFailed:
		term = "release_failed"
	}
	return windowsSocketWriteSnapshot{
		Post: w.post, PostErrno: w.postErrno,
		Parts: w.nParts, Bytes: w.bytes, PinCalls: w.pinCalls, WSASendCalls: w.sendCalls,
		EventSignaled: w.signaled, EventClosed: w.eventClose, EventPollErr: w.pollErr,
		Unpinned: w.unpinned,
		FenceSeq: w.fenceSeq, UnpinSeq: w.unpinSeq, EventCloseSeq: w.closeSeq, DoneSeq: w.doneSeq,
		Result: w.result, Faults: append([]string(nil), w.faults...), ReleaseErr: w.releaseErr,
		Terminal: term, TerminalReason: w.termReason,
		FenceProven: w.fenceProven, Retired: w.retired,
	}
}

// ---- submit ----

func (rt *windowsSocketWriteRuntime) Submit(raw syscall.RawConn, parts [][]byte) (op *windowsSocketWrite, err error) {
	if raw == nil {
		return nil, errors.New("windows socket write: nil RawConn")
	}
	lens := make([]uint64, len(parts))
	for i, p := range parts {
		lens[i] = uint64(len(p))
	}
	total, derr := windowsSocketWriteDomain(lens)
	if derr != nil {
		return nil, derr
	}
	if name, rerr := rt.deps.API.Resolve(); rerr != nil {
		return nil, fmt.Errorf("windows socket write: resolve: %w", windowsSocketResolveError(name, rerr))
	}
	w := &windowsSocketWrite{
		rt: rt, raw: raw,
		done: make(chan struct{}), fenceFailed: make(chan struct{}), releaseFailed: make(chan struct{}),
		parts:  append([][]byte(nil), parts...), // outer headers only; payload is neither copied nor modified
		post:   windowsSocketPostNotPosted,
		nParts: len(parts), bytes: total,
	}
	w.cond = sync.NewCond(&w.mu)

	defer func() {
		r := recover()
		if r == nil {
			return
		}
		// Panic reached Submit. Never swallow it. Quarantine BEFORE re-panic when
		// native ownership is possible; roll back only when it provably is not.
		if w.hasEvent() {
			var rollback bool
			w.with(func() {
				rollback = !w.mayOwn && !w.releasing && w.terminal == windowsSocketTermNone
			})
			if rollback && w.markNoIO() {
				w.fault(fmt.Sprintf("panic before post, rolled back: %v", r))
				w.release(false) // classifies its own cleanup panics
			} else {
				w.quarantine(fmt.Sprintf("panic during submit (native ownership possible or release already claimed): %v", r))
			}
		}
		panic(r)
	}()

	var setupErr, postErr error
	cbRan := false
	werr := raw.Write(func(fd uintptr) bool {
		if w.cbCalls.Add(1) > 1 {
			w.fault("RawConn.Write callback invoked more than once; no second post")
			return true
		}
		cbRan = true
		setupErr, postErr = w.submitInCallback(windowsSocketHandle(fd))
		return true // never retry
	})
	if !cbRan {
		if werr == nil {
			return nil, errors.New("windows socket write: RawConn.Write returned without running the callback")
		}
		return nil, werr // original refusal error, unchanged
	}
	if !w.hasEvent() {
		return nil, joinWindowsSocketErrs(setupErr, werr)
	}
	var errs []error
	if setupErr != nil {
		errs = append(errs, setupErr)
	}
	if postErr != nil {
		errs = append(errs, postErr)
	}
	if werr != nil {
		w.fault(fmt.Sprintf("RawConn.Write error after callback: %v", werr))
		errs = append(errs, fmt.Errorf("windows socket write: raw write: %w", werr))
	}
	var post windowsSocketPost
	var term int
	w.with(func() { post, term = w.post, w.terminal })
	if post != windowsSocketPostNative997 && term == windowsSocketTermNone {
		// native0, native_error or not_posted rollback; native_unclassified is
		// already FenceFailed so term != None.
		if rerr := w.release(true); rerr != nil {
			errs = append(errs, rerr)
		}
	}
	return w, joinWindowsSocketErrs(errs...)
}

func (w *windowsSocketWrite) hasEvent() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.haveEvent
}

func joinWindowsSocketErrs(errs ...error) error {
	var nz []error
	for _, e := range errs {
		if e != nil {
			nz = append(nz, e)
		}
	}
	switch len(nz) {
	case 0:
		return nil
	case 1:
		return nz[0]
	}
	return errors.Join(nz...)
}

// submitInCallback runs INSIDE the protected RawConn.Write callback. fd is used
// only within this call. The event handle is owned and registered immediately
// after CreateEvent succeeds, before any other fallible step.
func (w *windowsSocketWrite) submitInCallback(fd windowsSocketHandle) (setupErr, postErr error) {
	rt, api := w.rt, w.rt.deps.API
	ev, evOK, evCallErr := api.CreateEvent()
	if err := windowsSocketCreateEventError(evOK, evCallErr); err != nil {
		return fmt.Errorf("windows socket write: CreateEvent: %w", err), nil
	}
	rt.created.Add(1)
	w.with(func() { w.ev, w.haveEvent, w.pollable = ev, true, true })
	rt.register(w) // strong root before NewPinner / allocation / pins

	// The core computes the tagged event: low bit set suppresses the IOCP packet;
	// the core still waits on and closes the ORIGINAL ev. The adapter builds the
	// native OVERLAPPED (HEvent = tagged) and WSABUF array inside the post object.
	post, adapterPins := api.NewPost(uintptr(ev)|1, w.parts)
	// Validate BEFORE publication, NewPinner, any Pin, mayOwn or Send. A malformed
	// post is a certain no-I/O setup fault: same path as a pin failure (fault,
	// markNoIO, return; Submit then rolls back and closes the created event).
	if shapeErr := windowsSocketPostShapeError(post, adapterPins); shapeErr != nil {
		err := fmt.Errorf("windows socket write: invalid native post: %w", shapeErr)
		w.fault(err.Error())
		w.markNoIO()
		return err, nil
	}
	// Publish under the op lock; w.native is immutable once published.
	w.with(func() { w.native = post })
	pn := rt.deps.NewPinner()
	w.with(func() { w.pinner = pn })

	objs := make([]any, 0, len(adapterPins)+len(w.parts))
	objs = append(objs, adapterPins...) // OVERLAPPED, then &WSABUF[0], in adapter order
	for _, p := range w.parts {
		objs = append(objs, &p[0])
	}
	for _, o := range objs {
		w.with(func() { w.pinCalls++ })
		if perr := windowsSocketPinError(pn.Pin(o)); perr != nil {
			err := fmt.Errorf("windows socket write: pin: %w", perr)
			w.fault(err.Error())
			w.markNoIO()
			return err, nil
		}
		w.with(func() { w.pinOK = true })
	}

	w.with(func() { w.mayOwn = true })
	w.observe("registered")
	rt.posted.Add(1)
	w.with(func() {
		w.sendCalls++
		if w.sendCalls > 1 {
			w.faultLocked("more than one WSASend")
		}
	})
	// postReturned is published by a defer scoped DIRECTLY around Send: it is set
	// after a normal return or after a panic crossed the call, never by a
	// pre-call observer panic and never by a defer around the whole Submit.
	sendErr := func() error {
		defer w.with(func() { w.postReturned = true })
		return api.Send(fd, post)
	}()

	var en syscall.Errno
	isErrno := sendErr != nil && errors.As(sendErr, &en)
	switch {
	case sendErr == nil:
		w.with(func() { w.post = windowsSocketPostNative0 })
	case isErrno && en == windowsSocketErrIOPending:
		w.with(func() { w.post, w.postErrno = windowsSocketPostNative997, windowsSocketErrIOPending })
	case isErrno && en != 0:
		w.with(func() { w.post, w.postErrno = windowsSocketPostError, en })
		w.markNoIO()
		postErr = fmt.Errorf("windows socket write: WSASend: %w", sendErr)
	default:
		// Unclassifiable: ownership uncertain. Quarantine; never claim no-I/O.
		w.with(func() { w.post = windowsSocketPostUnclassified })
		postErr = fmt.Errorf("windows socket write: WSASend returned unclassified error: %w", sendErr)
		w.quarantine("unclassified WSASend error; native ownership uncertain: " + sendErr.Error())
	}
	w.observe("post_send_returned")

	switch w.postClass() {
	case windowsSocketPostNative997:
		go w.waitAndRelease() // no fallible step between post and launch
		w.observe("waiter_launched")
	case windowsSocketPostNative0:
		if w.stampFence() {
			w.observe("pre_query")
			q := api.Result(fd, post)
			res := windowsSocketWriteResult{Source: windowsSocketResultPostQuery, OK: q.OK, N: q.N, Errno: q.Errno}
			if !q.OK && q.Errno == 0 {
				res.Err = "WSAGetOverlappedResult returned FALSE without errno"
				w.fault(res.Err)
				postErr = errors.New("windows socket write: " + res.Err)
			}
			w.with(func() { w.result = res })
		}
	}
	return nil, postErr
}

func (w *windowsSocketWrite) postClass() (p windowsSocketPost) {
	w.with(func() { p = w.post })
	return
}

// ---- waiter / fence ----

func (w *windowsSocketWrite) waitAndRelease() {
	defer func() {
		if r := recover(); r != nil {
			w.quarantine(fmt.Sprintf("panic in waiter: %v", r))
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					w.fault(fmt.Sprintf("panic in waiter_exit observer: %v", r))
				}
			}()
			w.observe("waiter_exit")
		}()
	}()
	st, err := w.rt.deps.API.WaitEvent(w.ev)
	if err != nil || st != windowsSocketWaitObject0 {
		w.quarantine(fmt.Sprintf("wait for completion event not fenced: status=%d err=%v", st, err))
		return
	}
	if !w.stampFence() { // op was quarantined while the wait was blocked
		w.fault("fence observed after quarantine; op stays quarantined")
		return
	}
	w.observe("pre_query")
	res := w.fenceQuery()
	w.with(func() { w.result = res })
	_ = w.release(false) // failure is reported through the terminal channels
}

func (w *windowsSocketWrite) fenceQuery() windowsSocketWriteResult {
	ov, ok := w.acquireLease()
	if !ok {
		w.fault("fence query lease unavailable")
		return windowsSocketWriteResult{Source: windowsSocketResultControlFault, ControlErr: "lease unavailable"}
	}
	defer w.releaseLease()
	var ran bool
	var q windowsSocketNativeResult
	cerr := w.raw.Control(func(fd uintptr) {
		ran = true
		q = w.rt.deps.API.Result(windowsSocketHandle(fd), ov)
	})
	switch {
	case cerr != nil && !ran && errors.Is(cerr, net.ErrClosed):
		return windowsSocketWriteResult{Source: windowsSocketResultControlClosed, ControlErr: cerr.Error()}
	case cerr != nil || !ran:
		msg := "RawConn.Control did not run the callback"
		if cerr != nil {
			msg = cerr.Error()
		}
		w.fault("fence query control fault: " + msg)
		return windowsSocketWriteResult{Source: windowsSocketResultControlFault, ControlErr: msg}
	}
	res := windowsSocketWriteResult{Source: windowsSocketResultFenceQuery, OK: q.OK, N: q.N, Errno: q.Errno}
	if !q.OK && q.Errno == 0 {
		res.Err = "WSAGetOverlappedResult returned FALSE without errno"
		w.fault(res.Err) // the physical fence is still valid; release proceeds
	}
	return res
}

// ---- leases / cancel ----

// acquireLease and the transition to releasing are serialized by w.mu. A lease
// requires mayOwn and a published native post; it returns the stable post object to use
// under the lease. Before the post, or after release began, there is no lease and
// no native use.
func (w *windowsSocketWrite) acquireLease() (any, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.mayOwn || !w.postReturned || w.native == nil || w.releasing || w.terminal == windowsSocketTermDone || w.terminal == windowsSocketTermReleaseFailed {
		return nil, false
	}
	w.leases++
	return w.native, true
}

func (w *windowsSocketWrite) releaseLease() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.leases--
	if w.leases == 0 {
		w.cond.Broadcast()
	}
}

// Cancel issues one CancelIoEx for this op's OVERLAPPED inside RawConn.Control.
// It never holds the op lock across Control and never closes the socket. It stays
// valid on a FenceFailed op (the OVERLAPPED is still retained).
func (w *windowsSocketWrite) Cancel() windowsSocketCancel {
	ov, ok := w.acquireLease()
	if !ok {
		return windowsSocketCancelAlreadyDone
	}
	defer w.releaseLease()
	var ran bool
	var cerr error
	err := w.raw.Control(func(fd uintptr) {
		ran = true
		cerr = w.rt.deps.API.CancelIoEx(windowsSocketHandle(fd), ov)
	})
	switch {
	case err != nil && !ran && errors.Is(err, net.ErrClosed):
		return windowsSocketCancelControlClose
	case err != nil || !ran:
		w.fault(fmt.Sprintf("cancel control fault: ran=%v err=%v", ran, err))
		return windowsSocketCancelFaultControl
	case cerr == nil:
		return windowsSocketCancelIssued
	}
	var en syscall.Errno
	if errors.As(cerr, &en) && en == windowsSocketErrNotFound {
		return windowsSocketCancelNotFound
	}
	w.fault(fmt.Sprintf("CancelIoEx error: %v", cerr))
	return windowsSocketCancelFaultCancel
}

// ---- release ----

// cleanupPanic classifies a panic inside release. release() is only entered
// with native retirement established, so any interruption is ReleaseFailed; the
// flags (Unpinned, EventClosed, ReleaseErr, TerminalReason) are truthful and the
// op stays registered as the strong owner of whatever is unreleased.
func (w *windowsSocketWrite) cleanupPanic(r any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	msg := fmt.Sprintf("cleanup panic in phase %d after retirement (unpinned=%v eventClosed=%v): %v", w.relPhase, w.unpinned, w.eventClose, r)
	w.faultLocked(msg)
	if w.terminal != windowsSocketTermNone {
		return
	}
	w.terminal = windowsSocketTermReleaseFailed
	w.releaseErr = msg
	w.termReason = msg
	close(w.releaseFailed)
}

// release requires native retirement to be proven (w.retired). It claims the op
// atomically; a terminal-owned or already-claimed op is never released. Order:
// lease drain, Unpin, one CloseEvent attempt, registry removal, Done. No retry.
// With repanic, a cleanup panic is classified and then re-panicked (sync caller);
// otherwise it is classified and swallowed (worker goroutine, no process crash).
func (w *windowsSocketWrite) release(repanic bool) (err error) {
	w.mu.Lock()
	refuse := func(msg string) error {
		w.faultLocked(msg)
		w.mu.Unlock()
		return errors.New("windows socket write: " + msg)
	}
	if w.terminal != windowsSocketTermNone {
		return refuse("release refused: terminal already owned")
	}
	if w.releasing {
		return refuse("double release")
	}
	if !w.retired {
		return refuse("release refused: native retirement not established")
	}
	w.releasing = true
	w.relPhase = relPhaseClaimed
	w.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			w.cleanupPanic(r)
			if repanic {
				panic(r)
			}
		}
	}()

	w.observe("release_begin")

	w.mu.Lock()
	for w.leases > 0 {
		if !w.drainFired {
			w.drainFired = true
			w.mu.Unlock()
			w.observe("lease_drain_wait")
			w.mu.Lock()
			continue
		}
		w.cond.Wait()
	}
	w.mu.Unlock()

	w.observe("pre_unpin")
	var pn windowsSocketPinner
	var doUnpin bool
	w.with(func() { pn, doUnpin = w.pinner, w.pinOK })
	if doUnpin {
		pn.Unpin()
		w.with(func() {
			w.rt.unpins.Add(1)
			w.unpinned = true
			w.unpinSeq = w.rt.nextSeq()
		})
	}
	w.with(func() { w.relPhase = relPhaseUnpinned })

	w.observe("pre_close_event")
	w.with(func() { w.relPhase = relPhaseClosing; w.pollable = false })
	cerr := w.rt.deps.API.CloseEvent(w.ev) // not under the op lock
	if cerr != nil {
		w.with(func() {
			w.releaseErr = cerr.Error()
			w.faultLocked("CloseEvent failed: " + cerr.Error())
			w.termReason = "CloseEvent failed"
			w.terminal = windowsSocketTermReleaseFailed
			close(w.releaseFailed)
		})
		// The op stays registered as the event owner. No retry, no exit.
		return fmt.Errorf("windows socket write: CloseEvent: %w", cerr)
	}
	w.with(func() {
		w.rt.closed.Add(1)
		w.eventClose = true
		w.closeSeq = w.rt.nextSeq()
		w.relPhase = relPhaseClosed
	})

	w.observe("pre_done")
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.terminal != windowsSocketTermNone {
		w.faultLocked("Done refused: terminal already owned")
		return errors.New("windows socket write: Done refused: terminal already owned")
	}
	w.rt.unregister(w) // lock order: op lock then rt lock; readers never reverse it
	w.terminal = windowsSocketTermDone
	w.termReason = "cleanup complete"
	w.doneSeq = w.rt.nextSeq()
	close(w.done)
	return nil
}

// ---- portable default pinner (runtime.Pinner is not Windows-specific) ----

type windowsSocketRuntimePinner struct{ p runtime.Pinner }

func newWindowsSocketRuntimePinner() windowsSocketPinner { return &windowsSocketRuntimePinner{} }

// Pin returns the RAW outcome; windowsSocketPinError formats a recovered panic.
func (p *windowsSocketRuntimePinner) Pin(x any) (res windowsSocketPinResult) {
	defer func() {
		if r := recover(); r != nil {
			res = windowsSocketPinResult{Panic: r, Panicked: true}
		}
	}()
	p.p.Pin(x)
	return windowsSocketPinResult{}
}

func (p *windowsSocketRuntimePinner) Unpin() { p.p.Unpin() }
