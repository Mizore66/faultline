//go:build windows

package proofview

// STATIC, UNEXECUTED, TEST-ONLY proposal (TDD stage, revision 2).
//
// This file drives the four existing Windows native children THROUGH the
// not-yet-implemented production physical helper
//
//	submitWindowsSocketWrite(raw syscall.RawConn, parts [][]byte)
//	    (*windowsSocketWrite, error)
//
// (see CONTRACT.md beside this file). The earlier test-local native algorithm
// (CreateEventW/WSASend/WaitForSingleObject/WSAGetOverlappedResult/CancelIoEx,
// runtime.Pinner, release ordering) was REMOVED from this file on purpose; it
// now lives only in the helper. What remains here is harness: fixtures,
// scenarios, strict classification of the helper's reported physical facts,
// receipts, failure-only guards and the test-only processExit on an
// unfenceable operation.
//
// Until the helper exists this file does not compile (expected RED: undefined
// helper symbols only). Nothing here has been compiled or run, and it is not
// native proof or approval.
//
// Facts relied on (unchanged, now owned by the helper contract):
//   - OVERLAPPED.hEvent with the low-order bit set suppresses the IOCP packet;
//     wait on and close the ORIGINAL handle.
//   - WSASend: 0 = completed immediately; WSA_IO_PENDING (997) = started; any
//     other error = no completion will occur. The OVERLAPPED, WSABUF array and
//     every referenced byte array stay valid until the fence. All I/O started
//     by a thread is canceled when that thread exits, so the caller keeps the
//     submitting goroutine LockOSThread'd until the native fence.
//   - closesocket returning is NOT a completion fence.
//   - WSAGetOverlappedResult(fWait=FALSE) FALSE means pending OR failed.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	ovEnv       = "FAULTLINE_WINDOWS_OVERLAPPED_CHILD"
	ovNonceEnv  = "FAULTLINE_WINDOWS_OVERLAPPED_NONCE"
	ovNonceArg  = "faultline-overlapped-nonce="
	ovSource    = "faultline-windows-overlapped-probe-v1"
	ovResultTag = "OV_EVIDENCE "

	// Failure-only guards; none is used as a behavior heuristic. The 64MiB cap
	// is a TEST guard applied by the adapter before the helper is called; the
	// production helper has no such cap (its only size domain is total<=DWORD).
	ovPayloadCap = 64 << 20
	// Bounds: the parent context (<= 50s) and the cmd.WaitDelay (5s) run in
	// sequence, so a child is gone by <= 55s. The child's own -test.timeout
	// (limit+5s) is only a fallback if the parent dies; it runs in parallel with
	// the parent timer and is not additive to a normal overrun.
	ovChildCap     = 50 * time.Second // parent context cap per child (<= 60s)
	ovTimeoutSlack = 5 * time.Second  // child -test.timeout = limit + slack
	ovWaitDelay    = 5 * time.Second
	ovHeadroom     = 10 * time.Second // reserved from the outer test deadline
	ovChildBudget  = 35 * time.Second // child-wide watchdog (< ovChildCap)
	ovGuard        = 15 * time.Second // one internal wait
	ovReadDeadline = 15 * time.Second

	ovExitUnfenced = 86

	ovWSAECONNABORTED = syscall.Errno(10053)
	ovWSAECONNRESET   = syscall.Errno(10054)
	ovWSAIOIncomplete = syscall.Errno(996)
)

var (
	ovSpecs = []string{
		"healthy-interoperability",
		"pin-lifetime-completion",
		"paused-reader-close",
		"closed-before-post-identity",
	}
	ovScenarios = map[string]func(*ovRun) error{
		"healthy-interoperability":    ovScenarioHealthy,
		"pin-lifetime-completion":     ovScenarioPin,
		"paused-reader-close":         ovScenarioPausedClose,
		"closed-before-post-identity": ovScenarioClosedIdentity,
	}
	ovEmitted atomic.Bool
	ovActive  atomic.Pointer[ovRun]

	// Harness-only: the OS thread id of the locked submitter, to assert the
	// caller-owned LockOSThread precondition held for the whole child.
	ovGetCurrentThreadID = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentThreadId")
)

func ovThreadID() (uint32, error) {
	if err := ovGetCurrentThreadID.Find(); err != nil {
		return 0, fmt.Errorf("resolve GetCurrentThreadId: %w", err)
	}
	r1, _, _ := ovGetCurrentThreadID.Call()
	return uint32(r1), nil
}

// ---- evidence ----

type ovEvidence struct {
	mu     sync.Mutex
	seq    int
	events []string
	fields map[string]any
	faults []string
}

func (e *ovEvidence) event(name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	e.events = append(e.events, fmt.Sprintf("%03d %s", e.seq, name))
	return e.seq
}

func (e *ovEvidence) set(k string, v any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fields == nil {
		e.fields = map[string]any{}
	}
	e.fields[k] = v
}

func (e *ovEvidence) fault(msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.faults = append(e.faults, msg)
}

func (e *ovEvidence) faultCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.faults)
}

// ---- run state (one per child process) ----

type ovRun struct {
	spec, nonce string
	ev          ovEvidence

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	mu      sync.Mutex
	ops     []*ovOp
	closers []io.Closer

	// goroutines counts goroutines spawned by THIS HARNESS (readers, closers).
	// The helper's own waiter is not counted here.
	goroutines atomic.Int32

	threadID   uint32 // locked submitter OS thread
	cleanupErr string
}

func (r *ovRun) own(c io.Closer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closers = append(r.closers, c)
}

func (r *ovRun) spawn(fn func()) {
	r.wg.Add(1)
	r.goroutines.Add(1)
	go func() {
		defer r.wg.Done()
		fn()
	}()
}

func (r *ovRun) emit(ok bool, errText string) {
	if !ovEmitted.CompareAndSwap(false, true) {
		return
	}
	cnt := windowsSocketWriteCounters() // helper-owned process-wide counters
	r.ev.mu.Lock()
	out := map[string]any{
		"source": ovSource, "nonce": r.nonce, "scenario": r.spec,
		"goos": runtime.GOOS, "goarch": runtime.GOARCH, "go": runtime.Version(),
		"ok": ok, "events": append([]string{}, r.ev.events...),
		"faults":  append([]string{}, r.ev.faults...),
		"details": r.ev.fields,
		"cleanup": map[string]any{
			"events_created": cnt.EventsCreated, "events_closed": cnt.EventsClosed,
			"pins_released": cnt.UnpinOps, "ops_posted": cnt.OpsPosted, // pins_released == UnpinOps: once per op
			"harness_goroutines_spawned": r.goroutines.Load(), "error": r.cleanupErr, // harness-only; helper waiter not counted
		},
		// The order fields in details are HELPER-ATTESTED (see CONTRACT.md); the
		// harness cannot independently observe native ordering.
		"order_source": "helper_attested",
	}
	if errText != "" {
		out["error"] = errText
	}
	b, merr := json.Marshal(out)
	r.ev.mu.Unlock()
	if merr != nil {
		b = []byte(fmt.Sprintf(`{"source":%q,"ok":false,"error":"marshal failed"}`, ovSource))
	}
	fmt.Printf("%s%s\n", ovResultTag, b)
}

// fatalUnfenced is the TEST-HARNESS processExit used when a possibly
// kernel-owned operation cannot be fenced (or the child budget is exhausted).
// The production helper never exits; it retains ownership and reports
// FenceFailed(). The harness never unpins or frees; process exit reclaims
// everything and the parent fails on the exit code.
func (r *ovRun) fatalUnfenced(reason string) {
	r.ev.fault("FATAL unfenced/budget: " + reason)
	r.emit(false, "FATAL unfenced/budget: "+reason)
	os.Exit(ovExitUnfenced)
}

// ---- strict classification of the helper's reported physical result ----

// ovClassify maps a queried (post_query or fence_query) helper result to a
// strict class. closeIssued is true only when this harness has already issued
// Cancel/Close for the operation. Every errno that is not a named, explained
// consequence of that local cancel/close (including wrong handle, bad
// parameter and mode errors) is a fault and can never pass.
func ovClassify(res windowsSocketWriteResult, total int, closeIssued bool) (class string, fault bool) {
	if res.OK {
		switch {
		case int64(res.N) == int64(total):
			return "complete_full", false
		case int64(res.N) < int64(total):
			return "complete_short", false
		default:
			return "fault_count_overrun", true
		}
	}
	switch res.Errno {
	case 0:
		return "fault_false_without_errno", true
	case ovWSAIOIncomplete, syscall.ERROR_IO_PENDING:
		return "fault_incomplete_after_signal", true
	case syscall.ERROR_OPERATION_ABORTED: // 995
		if closeIssued {
			return "aborted", false
		}
		return "fault_abort_without_local_cancel", true
	case ovWSAECONNABORTED, ovWSAECONNRESET: // 10053 / 10054
		if closeIssued {
			return "peer_abort_reset", false
		}
		return "fault_reset_without_local_close", true
	default:
		return "fault_unexpected_native_errno", true
	}
}

// ---- thin adapter over the production helper ----

type ovFinal struct {
	Class      string
	N          uint32
	Errno      uint32
	ControlErr string
}

// ovOp is a thin adapter. It deliberately keeps NO reference to the payload
// regions: after submit the helper's owned operation must be the only strong
// reference (the pin child drops every caller reference and GCs).
type ovOp struct {
	run   *ovRun
	name  string
	w     *windowsSocketWrite
	total int
	parts int
	post  string // helper Snapshot().Post captured at submit return

	closeIssued atomic.Bool // set by this harness before Cancel / Close

	mu         sync.Mutex
	finalDone  bool
	final      ovFinal
	snap       windowsSocketWriteSnapshot // valid once finalDone
	faultsSeen int
}

// submit applies the harness-only payload cap, checks the locked-thread
// precondition, then calls the production helper. A nil op means the helper
// reported nothing posted and everything released. A non-nil op with a non-nil
// error is a native_error post (no completion will occur, already released).
func (r *ovRun) submit(name string, rc syscall.RawConn, regions [][]byte) (*ovOp, error) {
	total := 0
	for _, g := range regions {
		total += len(g)
	}
	if total > ovPayloadCap { // test guard only
		return nil, fmt.Errorf("submit: total %d exceeds the test payload cap %d", total, ovPayloadCap)
	}
	tid, err := ovThreadID()
	if err != nil {
		return nil, err
	}
	if tid != r.threadID {
		return nil, fmt.Errorf("submit: caller is not on the locked submitter thread (have %d want %d)", tid, r.threadID)
	}
	w, err := submitWindowsSocketWrite(rc, regions)
	if w == nil {
		if err == nil {
			err = errors.New("helper returned nil operation and nil error")
			r.ev.fault(err.Error())
		}
		return nil, err
	}
	o := &ovOp{run: r, name: name, w: w, total: total, parts: len(regions)}
	s0 := w.Snapshot()
	o.post = string(s0.Post)
	r.mu.Lock()
	r.ops = append(r.ops, o) // registered BEFORE any further check: ownership may be retained
	r.mu.Unlock()
	o.drainFaults(s0)
	// Return-shape assertions. A helper wrapper/query/internal anomaly may
	// legitimately return (op, err) with Faults while a pending native997 stays
	// owned; those Faults already fail the child via drainFaults, and cleanup
	// still cancels and fences the retained op. Without any Fault the shape is exact.
	if len(s0.Faults) == 0 {
		if (err != nil) != (s0.Post == windowsSocketPostError) {
			r.ev.fault(fmt.Sprintf("%s: return shape err=%v with post=%s (want err iff native_error)", name, err, s0.Post))
		}
		var en syscall.Errno
		if s0.Post == windowsSocketPostError && (!errors.As(err, &en) || en != s0.PostErrno || en == 0) {
			r.ev.fault(fmt.Sprintf("%s: native_error err=%v does not wrap Snapshot.PostErrno=%d", name, err, uint32(s0.PostErrno)))
		}
		if s0.Post == windowsSocketPostNative997 && s0.PostErrno != syscall.ERROR_IO_PENDING {
			r.ev.fault(fmt.Sprintf("%s: native997 PostErrno=%d want 997", name, uint32(s0.PostErrno)))
		}
	}
	if s0.Post != windowsSocketPostNative997 && !ovClosed(w.Done()) {
		r.ev.fault(fmt.Sprintf("%s: post=%s but Done not closed on return", name, s0.Post))
	}
	if s0.Post != windowsSocketPostNative997 && !(s0.Unpinned && s0.EventClosed) {
		r.ev.fault(fmt.Sprintf("%s: post=%s but not released on return", name, s0.Post))
	}
	return o, err
}

// drainFaults copies helper-reported faults into the harness evidence once.
func (o *ovOp) drainFaults(s windowsSocketWriteSnapshot) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for ; o.faultsSeen < len(s.Faults); o.faultsSeen++ {
		o.run.ev.fault(o.name + ": helper fault: " + s.Faults[o.faultsSeen])
	}
}

// finalize classifies the helper's physical result. It is only valid after
// Done() is closed; before that it returns Class "not_done" without caching.
func (o *ovOp) finalize() ovFinal {
	if !ovClosed(o.w.Done()) {
		return ovFinal{Class: "not_done"}
	}
	o.mu.Lock()
	if o.finalDone {
		defer o.mu.Unlock()
		return o.final
	}
	o.mu.Unlock()
	s := o.w.Snapshot()
	o.drainFaults(s)
	var f ovFinal
	res := s.Result
	switch res.Source {
	case windowsSocketResultNone:
		f = ovFinal{Class: "native_error_no_completion", Errno: uint32(s.PostErrno)}
	case windowsSocketResultControlClosed:
		f = ovFinal{Class: "unknown_closed", ControlErr: res.ControlErr}
	case windowsSocketResultControlFault:
		o.run.ev.fault(fmt.Sprintf("%s: control fault: %s", o.name, res.ControlErr))
		f = ovFinal{Class: "fault_control", ControlErr: res.ControlErr}
	case windowsSocketResultPostQuery, windowsSocketResultFenceQuery:
		class, bad := ovClassify(res, o.total, o.closeIssued.Load())
		if bad {
			o.run.ev.fault(fmt.Sprintf("%s: result query class=%s errno=%d", o.name, class, uint32(res.Errno)))
		}
		if s.Post == windowsSocketPostNative0 && class != "complete_full" && class != "complete_short" {
			o.run.ev.fault(fmt.Sprintf("%s: native0 confirmed-result query class=%s errno=%d", o.name, class, uint32(res.Errno)))
		}
		if (s.Post == windowsSocketPostNative0) != (res.Source == windowsSocketResultPostQuery) {
			o.run.ev.fault(fmt.Sprintf("%s: result source %q inconsistent with post %q", o.name, res.Source, s.Post))
		}
		f = ovFinal{Class: class, N: res.N, Errno: uint32(res.Errno)}
	default:
		o.run.ev.fault(fmt.Sprintf("%s: unknown result source %q", o.name, res.Source))
		f = ovFinal{Class: "fault_unknown_result_source"}
	}
	// Helper-level invariants that do not depend on the scenario.
	// Helper-attested ordering. Fence is taken after the wait returns
	// WAIT_OBJECT_0 (or after a native0 WSASend returns 0) and BEFORE the result
	// query; native_error performed no I/O so FenceSeq must be 0.
	if !s.Unpinned || !s.EventClosed {
		o.run.ev.fault(fmt.Sprintf("%s: Done closed before release (unpinned=%v eventClosed=%v)", o.name, s.Unpinned, s.EventClosed))
	}
	if s.Post == windowsSocketPostError {
		if s.FenceSeq != 0 || !(0 < s.UnpinSeq && s.UnpinSeq < s.EventCloseSeq && s.EventCloseSeq < s.DoneSeq) {
			o.run.ev.fault(fmt.Sprintf("%s: native_error order fence=%d unpin=%d close_event=%d done=%d (want fence=0, 0<unpin<close<done)", o.name, s.FenceSeq, s.UnpinSeq, s.EventCloseSeq, s.DoneSeq))
		}
	} else if !(0 < s.FenceSeq && s.FenceSeq < s.UnpinSeq && s.UnpinSeq < s.EventCloseSeq && s.EventCloseSeq < s.DoneSeq) {
		o.run.ev.fault(fmt.Sprintf("%s: order violation fence=%d unpin=%d close_event=%d done=%d", o.name, s.FenceSeq, s.UnpinSeq, s.EventCloseSeq, s.DoneSeq))
	}
	if s.WSASendCalls != 1 {
		o.run.ev.fault(fmt.Sprintf("%s: helper issued %d WSASend calls (no automatic repost allowed)", o.name, s.WSASendCalls))
	}
	if s.Parts != o.parts || s.Bytes != uint64(o.total) || s.PinCalls != 2+o.parts {
		o.run.ev.fault(fmt.Sprintf("%s: helper accounting parts=%d bytes=%d pincalls=%d want parts=%d bytes=%d pincalls=%d", o.name, s.Parts, s.Bytes, s.PinCalls, o.parts, o.total, 2+o.parts))
	}
	o.run.ev.event("harness_observed_done:" + o.name) // harness observation, not a native event
	o.mu.Lock()
	o.final, o.snap, o.finalDone = f, s, true
	o.mu.Unlock()
	return f
}

// pendingObserved is one non-waiting observation: native997 at post, event
// unsignaled and unclosed, Done not closed. The event poll is performed by the
// helper inside Snapshot while the event is guaranteed not closed.
func (o *ovOp) pendingObserved() (pending, signaled, released bool, err error) {
	s := o.w.Snapshot()
	o.drainFaults(s)
	if s.EventPollErr != "" {
		return false, s.EventSignaled, s.EventClosed, errors.New(s.EventPollErr)
	}
	pending = s.Post == windowsSocketPostNative997 && !s.EventSignaled && !s.EventClosed && !ovClosed(o.w.Done())
	return pending, s.EventSignaled, s.EventClosed, nil
}

// cancel records the local cancel intent first (so a later 995 is explained),
// then asks the helper to CancelIoEx. It never closes the socket.
func (o *ovOp) cancel() string {
	res := string(o.w.Cancel())
	// Only a cancel that reached CancelIoEx explains a later 995. Set before
	// any finalize (same goroutine, and Done is waited after cancel returns).
	switch res {
	case "cancel_issued", "not_found_completed_race":
		o.closeIssued.Store(true)
	}
	return res
}

// ovCancelOK reports the non-fault Cancel result states (idempotent re-cancel).
func ovCancelOK(res string) bool {
	switch res {
	case "cancel_issued", "not_found_completed_race", "already_done":
		return true
	}
	return false
}

// requireFull demands a fully completed send (used by non-cancel scenarios).
func (o *ovOp) requireFull() error {
	f := o.finalize()
	if f.Class != "complete_full" {
		return fmt.Errorf("%s: send final class=%s n=%d errno=%d control=%q (want complete_full)", o.name, f.Class, f.N, f.Errno, f.ControlErr)
	}
	if int(f.N) != o.total {
		return fmt.Errorf("%s: send count %d != %d", o.name, f.N, o.total)
	}
	return nil
}

func (o *ovOp) describe() map[string]any {
	f := o.finalize()
	o.mu.Lock()
	s := o.snap
	o.mu.Unlock()
	return map[string]any{
		"post_class": o.post, "post_errno": uint32(s.PostErrno), "bytes": o.total,
		"final_class": f.Class, "final_n": f.N, "final_errno": f.Errno,
		"control_err": f.ControlErr,
	}
}

// orderSeqs returns the helper's own monotonic sequence numbers (fence, unpin,
// event-close, done). They are HELPER-ATTESTED: the helper stamps them itself;
// the harness cannot independently prove native ordering (that needs a
// helper-package test with an injected syscall seam).
func (o *ovOp) orderSeqs() []uint64 {
	o.finalize()
	o.mu.Lock()
	defer o.mu.Unlock()
	return []uint64{o.snap.FenceSeq, o.snap.UnpinSeq, o.snap.EventCloseSeq, o.snap.DoneSeq}
}

func (r *ovRun) waitDone(o *ovOp) error {
	select {
	case <-o.w.Done():
		o.finalize()
		return nil
	case <-o.w.FenceFailed():
		r.fatalUnfenced("helper reported fence failure for " + o.name)
	case <-o.w.ReleaseFailed():
		// Native retirement is proven; cleanup may have stopped before Unpin or
		// after event closure. Read actual flags, never infer an event-only leak.
		// Ordinary test failure, never wait on Done or retry cleanup.
		return o.releaseFailure()
	case <-time.After(ovGuard):
		r.fatalUnfenced("completion not delivered for " + o.name)
	}
	return nil
}

// releaseFailure records the leak as a fault and returns it as an ordinary error.
func (o *ovOp) releaseFailure() error {
	s := o.w.Snapshot()
	o.drainFaults(s)
	msg := fmt.Sprintf("%s: ReleaseFailed (cleanup interrupted): retired=%v fenceProven=%v ReleaseErr=%q unpinned=%v eventClosed=%v", o.name, s.Retired, s.FenceProven, s.ReleaseErr, s.Unpinned, s.EventClosed)
	if !s.Retired || s.ReleaseErr == "" || s.DoneSeq != 0 || ovClosed(o.w.Done()) || ovClosed(o.w.FenceFailed()) {
		msg += " [terminal-state contract violation: want Retired, ReleaseErr set, DoneSeq 0, Done/FenceFailed open]"
	}
	o.run.ev.fault(msg)
	return errors.New(msg)
}

// ---- cleanup (runs on the still-locked submitter thread) ----

func (r *ovRun) cleanup() {
	r.stopOnce.Do(func() { close(r.stop) })
	if tid, err := ovThreadID(); err != nil || tid != r.threadID {
		r.ev.fault(fmt.Sprintf("cleanup: submitter OS thread changed (have %d err=%v want %d)", tid, err, r.threadID))
	}
	r.mu.Lock()
	ops := append([]*ovOp(nil), r.ops...)
	r.mu.Unlock()
	// 1. cancel while the submitter thread is alive (never closes the socket).
	for _, o := range ops {
		o.cancel()
	}
	// 2. close every owned socket/listener (unblocks readers).
	r.mu.Lock()
	cl := r.closers
	r.closers = nil
	r.mu.Unlock()
	for i := len(cl) - 1; i >= 0; i-- {
		// Only net.ErrClosed from an owned, already-closed conn/listener is
		// tolerated. Anything else is a fault; cleanup always continues so the
		// fence and join below still run.
		if err := cl[i].Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			r.ev.fault(fmt.Sprintf("cleanup: unexpected Close error on owned closer %d: %v", i, err))
		}
	}
	// 3. fence every op; an unfenceable op ends the child, never unpins.
	for _, o := range ops {
		select {
		case <-o.w.Done():
			o.finalize()
		case <-o.w.FenceFailed():
			r.fatalUnfenced("helper reported fence failure for " + o.name)
		case <-o.w.ReleaseFailed():
			o.releaseFailure() // fault recorded; cleanup continues (no fatal exit, no retry)
		case <-time.After(ovGuard):
			r.fatalUnfenced("fence not observed for " + o.name)
		}
	}
	// 4. join harness goroutines.
	joined := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(ovGuard):
		r.fatalUnfenced("goroutine join timeout")
	}
	var leaks []string
	// Counter semantics (CONTRACT.md): each counter moves once per op that
	// reached the protected Write callback; adapter-registered ops are exactly
	// those. Equal-and-positive, never merely 0==0.
	if c := windowsSocketWriteCounters(); c.EventsCreated != int64(len(ops)) || c.EventsClosed != c.EventsCreated || c.UnpinOps != c.EventsCreated || c.OpsPosted != c.EventsCreated {
		leaks = append(leaks, fmt.Sprintf("counters %+v want all equal to registered ops %d", c, len(ops)))
	}
	for _, o := range ops {
		s := o.w.Snapshot()
		o.drainFaults(s)
		if !s.Unpinned || !s.EventClosed {
			leaks = append(leaks, o.name+" not released")
		}
	}
	if len(leaks) > 0 {
		r.cleanupErr = strings.Join(leaks, "; ")
		r.ev.fault("cleanup leak: " + r.cleanupErr)
	}
}

// ---- fixtures ----

type ovPair struct{ w, p *net.TCPConn } // w: exclusively owned accepted writer; p: client peer

func (r *ovRun) newPair() (*ovPair, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	r.own(ln)
	tl := ln.(*net.TCPListener)
	if err := tl.SetDeadline(time.Now().Add(ovGuard)); err != nil {
		return nil, err
	}
	dc, err := net.DialTimeout("tcp4", ln.Addr().String(), ovGuard)
	if err != nil {
		return nil, err
	}
	dtc, ok := dc.(*net.TCPConn)
	if !ok {
		dc.Close()
		return nil, errors.New("dial: not a *net.TCPConn")
	}
	r.own(dtc)
	sc, err := ln.Accept()
	if err != nil {
		return nil, err
	}
	stc, ok := sc.(*net.TCPConn)
	if !ok {
		sc.Close()
		return nil, errors.New("accept: not a *net.TCPConn")
	}
	r.own(stc)
	if err := ln.Close(); err != nil {
		return nil, fmt.Errorf("listener close: %w", err)
	}
	return &ovPair{w: stc, p: dtc}, nil
}

func ovByte(seed, i int) byte { return byte(seed*31 + i*131 + i>>7 + 17) }

func ovBytes(seed, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = ovByte(seed, i)
	}
	return b
}

func ovSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type ovRead struct {
	b   []byte
	err error
}

// startReader reads p until EOF/error, but only after gate is closed (or
// immediately when gate is nil). It exits on r.stop.
func (r *ovRun) startReader(p *net.TCPConn, gate <-chan struct{}) chan ovRead {
	ch := make(chan ovRead, 1)
	r.spawn(func() {
		if gate != nil {
			select {
			case <-gate:
			case <-r.stop:
				ch <- ovRead{err: errors.New("reader stopped before release")}
				return
			}
		}
		if err := p.SetReadDeadline(time.Now().Add(ovReadDeadline)); err != nil {
			ch <- ovRead{err: err}
			return
		}
		b, err := io.ReadAll(p)
		ch <- ovRead{b: b, err: err}
	})
	return ch
}

func ovRecv[T any](ch chan T, what string) (T, error) {
	select {
	case v := <-ch:
		return v, nil
	case <-time.After(ovGuard):
		var z T
		return z, fmt.Errorf("guard timeout waiting for %s", what)
	}
}

func ovClosed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// ---- scenario 1: healthy interoperability ----

func ovScenarioHealthy(r *ovRun) error {
	pr, err := r.newPair()
	if err != nil {
		return err
	}
	rc, err := pr.w.SyscallConn()
	if err != nil {
		return err
	}
	tw, err := newSocketTryWriter(pr.w) // prepares FIONBIO=1 (P1, not edited)
	if err != nil {
		return fmt.Errorf("newSocketTryWriter: %w", err)
	}
	sizes := []int{9, 1700, 4096, 65536, 100000, 3, 262144, 1500000}
	parts := make([][]byte, len(sizes))
	total := 0
	for i, n := range sizes {
		parts[i] = ovBytes(i+1, n)
		total += n
	}
	n, wb, terr := tw.Try(parts)
	if terr != nil {
		return fmt.Errorf("first Try: %w", terr)
	}
	if n < 0 || n > total || (wb && n != 0) {
		return fmt.Errorf("first Try impossible result n=%d wb=%v total=%d", n, wb, total)
	}
	var regions [][]byte
	rem := n
	for _, p := range parts {
		if rem >= len(p) {
			rem -= len(p)
			continue
		}
		regions = append(regions, p[rem:])
		rem = 0
	}
	mode := "exact-unaccepted-tail"
	var independent []byte
	if len(regions) == 0 {
		mode = "independent-explicit-send"
		independent = ovBytes(77, 4<<20)
		regions = [][]byte{independent}
	}
	clientMarker := []byte("CLIENT-MARKER-16")
	mA, mB, mC := []byte("FIFO-A:go-write-after-fence"), []byte("FIFO-B:try-after-fence"), []byte("FIFO-C:go-write-last")
	// Expected outbound hash is computed BEFORE the post.
	h := sha256.New()
	wantLen := 0
	for _, p := range parts {
		h.Write(p)
		wantLen += len(p)
	}
	if independent != nil {
		h.Write(independent)
		wantLen += len(independent)
	}
	for _, m := range [][]byte{mA, mB, mC} {
		h.Write(m)
		wantLen += len(m)
	}
	wantSHA := hex.EncodeToString(h.Sum(nil))

	gate := make(chan struct{})
	readCh := r.startReader(pr.p, gate)
	srvCh := make(chan ovRead, 1)
	r.spawn(func() { // ordinary concurrent Go READ on the writer socket
		m := make([]byte, len(clientMarker))
		_, err := io.ReadFull(pr.w, m)
		srvCh <- ovRead{b: m, err: err}
	})

	op, err := r.submit("healthy", rc, regions)
	r.ev.event("submit_returned")
	if err != nil {
		return err
	}
	// Reader is released only after the post returned. First prove the Go read
	// path with the client marker while the custom send may still be pending.
	if _, err := pr.p.Write(clientMarker); err != nil {
		return fmt.Errorf("client marker write: %w", err)
	}
	sr, err := ovRecv(srvCh, "server marker read")
	if err != nil {
		return err
	}
	r.ev.event("server_marker_read")
	pendingAtMarker := !ovClosed(op.w.Done())
	if sr.err != nil || !bytes.Equal(sr.b, clientMarker) {
		return fmt.Errorf("server marker read: err=%v data=%q", sr.err, sr.b)
	}
	close(gate)
	r.ev.event("reader_released")
	if err := r.waitDone(op); err != nil {
		return err
	}
	r.ev.event("completion_delivered")
	if err := op.requireFull(); err != nil {
		return err
	}
	// Ordinary Go Write / Try resume FIFO only after fence and delivery.
	if k, err := pr.w.Write(mA); err != nil || k != len(mA) {
		return fmt.Errorf("Write A: n=%d err=%v", k, err)
	}
	bn, bwb, berr := tw.Try([][]byte{mB})
	if berr != nil {
		return fmt.Errorf("Try B: %w", berr)
	}
	if bn < len(mB) {
		if k, err := pr.w.Write(mB[bn:]); err != nil || k != len(mB)-bn {
			return fmt.Errorf("Write B remainder: n=%d err=%v", k, err)
		}
	}
	if k, err := pr.w.Write(mC); err != nil || k != len(mC) {
		return fmt.Errorf("Write C: n=%d err=%v", k, err)
	}
	if err := pr.w.CloseWrite(); err != nil {
		return fmt.Errorf("CloseWrite: %w", err)
	}
	rd, err := ovRecv(readCh, "peer read")
	if err != nil {
		return err
	}
	if rd.err != nil {
		return fmt.Errorf("peer read: %w", rd.err)
	}
	gotSHA := ovSum(rd.b)
	r.ev.set("mode", mode)
	r.ev.set("try_first_n", n)
	r.ev.set("try_first_wouldblock", wb)
	r.ev.set("try_b_n", bn)
	r.ev.set("try_b_wouldblock", bwb)
	r.ev.set("batch_total", total)
	r.ev.set("custom_send", op.describe())
	r.ev.set("native_class", op.post)
	r.ev.set("server_marker_read_while_custom_pending", pendingAtMarker)
	r.ev.set("want_len", wantLen)
	r.ev.set("got_len", len(rd.b))
	r.ev.set("want_sha256", wantSHA)
	r.ev.set("got_sha256", gotSHA)
	if len(rd.b) != wantLen || gotSHA != wantSHA {
		return fmt.Errorf("outbound stream mismatch: got %d bytes sha %s want %d sha %s", len(rd.b), gotSHA, wantLen, wantSHA)
	}
	return nil
}

// ---- scenario 2: pin lifetime / completion ----

// ovPostOwned builds and posts a payload whose only remaining references after
// return are the helper-owned operation's own strong refs (the caller's locals
// are dead and the adapter retains none).
//
//go:noinline
func ovPostOwned(r *ovRun, name string, rc syscall.RawConn, seed int, cuts []int) (*ovOp, string, int, error) {
	data := ovBytes(seed, cuts[len(cuts)-1])
	h := sha256.Sum256(data)
	var regions [][]byte
	prev := 0
	for _, c := range cuts {
		regions = append(regions, data[prev:c])
		prev = c
	}
	op, err := r.submit(name, rc, regions)
	return op, hex.EncodeToString(h[:]), len(data), err
}

// ovGCRounds is the existing GC/churn loop of the pin child, unchanged.
func ovGCRounds() int {
	const rounds = 8
	for i := 0; i < rounds; i++ {
		runtime.GC()
		churn := make([][]byte, 0, 16)
		for k := 0; k < 16; k++ {
			g := make([]byte, 1<<20)
			g[0], g[len(g)-1] = byte(i), byte(k)
			churn = append(churn, g)
		}
		_ = churn
	}
	return rounds
}

// ovScenarioPin is a pin-lifetime/pending-completion SMOKE test. The helper
// keeps strong references to the payload and Go's heap does not move objects,
// so a missing pin would NOT make this fail; it is not proof of pin
// correctness. It uses the same reviewed unbuffered diagnostic configuration as
// the paused child and the original single 8MiB payload (no retry, enlargement
// or sleep). It directly requires: (1) pending (native997, unsignaled) before
// reader release, (2) an exact ordinary Go READ of a peer-written marker on the
// helper-write-owned socket while that send is still pending (no ordinary Go
// write on that socket), (3) GC rounds while still pending with the payload
// pins unreleased, then (4) full successful native completion after the reader
// is released. A missing condition fails INCONCLUSIVE. This does not claim the
// API guarantees a pending outcome.
func ovScenarioPin(r *ovRun) error {
	pr, err := r.newPair()
	if err != nil {
		return err
	}
	rc, err := pr.w.SyscallConn()
	if err != nil {
		return err
	}
	if err := ovConfigureUnbuffered(r, pr); err != nil {
		return err
	}
	if _, err := newSocketTryWriter(pr.w); err != nil {
		return fmt.Errorf("newSocketTryWriter: %w", err)
	}
	gate := make(chan struct{})
	readCh := r.startReader(pr.p, gate)
	marker := []byte("PIN-CLIENT-MARKER-20")
	srvCh := make(chan ovRead, 1)
	r.spawn(func() { // ordinary bounded Go READ on the helper-write-owned socket
		if err := pr.w.SetReadDeadline(time.Now().Add(ovReadDeadline)); err != nil {
			srvCh <- ovRead{err: err}
			return
		}
		m := make([]byte, len(marker))
		_, err := io.ReadFull(pr.w, m)
		srvCh <- ovRead{b: m, err: err}
	})
	op, wantSHA, wantLen, err := ovPostOwned(r, "pin", rc, 5, []int{1<<20 + 13, 3<<20 + 13, 3<<20 + 13 + 512<<10 + 7, 8 << 20})
	r.ev.event("submit_returned")
	if err != nil {
		return err
	}
	r.ev.set("post_class_at_post", op.post)
	r.ev.set("native_class", op.post)
	pending, sig, _, perr := op.pendingObserved()
	if perr != nil {
		return fmt.Errorf("peek event: %w", perr)
	}
	r.ev.set("event_signaled_at_peek", sig)
	r.ev.set("pending_observed", pending)
	r.ev.set("go_read_while_pending", false)
	r.ev.set("pending_after_go_read", false)
	r.ev.set("gc_while_pending", false)
	r.ev.set("pending_after_gc", false)
	r.ev.set("completed_before_reader_release", ovClosed(op.w.Done()))
	if !pending {
		r.ev.set("coverage_pending_pin_path", "UNKNOWN: pending path not observed")
		return fmt.Errorf("INCONCLUSIVE sizing: no pending overlapped operation observed (post=%s signaled=%v); pending completion not qualified, no retry/enlargement", op.post, sig)
	}

	// (2) Ordinary Go read of an exact peer-written marker while still pending.
	if err := pr.p.SetWriteDeadline(time.Now().Add(ovReadDeadline)); err != nil {
		return fmt.Errorf("peer SetWriteDeadline: %w", err)
	}
	if k, err := pr.p.Write(marker); err != nil || k != len(marker) {
		return fmt.Errorf("peer marker write: n=%d err=%v", k, err)
	}
	sr, err := ovRecv(srvCh, "server marker read")
	if err != nil {
		return err
	}
	r.ev.event("go_read_marker_while_pending")
	if sr.err != nil || !bytes.Equal(sr.b, marker) {
		return fmt.Errorf("go read marker: err=%v data=%q", sr.err, sr.b)
	}
	r.ev.set("go_read_marker_len", len(sr.b))
	pendingAfterRead, _, _, perr := op.pendingObserved()
	if perr != nil {
		return fmt.Errorf("peek event after read: %w", perr)
	}
	r.ev.set("pending_after_go_read", pendingAfterRead)
	r.ev.set("go_read_while_pending", pendingAfterRead)
	if !pendingAfterRead {
		return errors.New("INCONCLUSIVE coverage: helper send no longer pending after Go read; marker read not proven concurrent, no retry")
	}

	// (3) Existing GC rounds while the op is pending and its pins are held.
	cb := windowsSocketWriteCounters()
	rounds := ovGCRounds()
	r.ev.event("gc_rounds_done")
	pendingAfterGC, _, _, perr := op.pendingObserved()
	if perr != nil {
		return fmt.Errorf("peek event after gc: %w", perr)
	}
	ca := windowsSocketWriteCounters()
	r.ev.set("gc_rounds", rounds)
	r.ev.set("pending_after_gc", pendingAfterGC)
	r.ev.set("pins_released_before_reader_release", ca.UnpinOps)
	r.ev.set("events_closed_before_reader_release", ca.EventsClosed)
	r.ev.set("events_created_while_pending", ca.EventsCreated)
	r.ev.set("ops_posted_while_pending", ca.OpsPosted)
	one := windowsSocketCounters{EventsCreated: 1, OpsPosted: 1}
	pinsHeld := cb == one && ca == one // created 1, posted 1, closed 0, unpinned 0 before and after GC
	r.ev.set("pins_held_during_gc", pinsHeld)
	r.ev.set("gc_while_pending", pendingAfterGC && pinsHeld)
	doneBeforeRelease := ovClosed(op.w.Done())
	r.ev.set("completed_before_reader_release", doneBeforeRelease)
	if !pendingAfterGC || !pinsHeld || doneBeforeRelease {
		return fmt.Errorf("INCONCLUSIVE coverage: GC rounds not proven during pending with pins held (pending=%v pinsHeld=%v done=%v), no retry", pendingAfterGC, pinsHeld, doneBeforeRelease)
	}
	r.ev.set("coverage_pending_pin_path", "observed: native997 pending across Go read and 8 GC rounds with caller refs dropped and pins held")

	// (4) Release the peer reader; require full successful native completion.
	close(gate)
	r.ev.event("reader_released")
	if err := r.waitDone(op); err != nil {
		return err
	}
	r.ev.set("completion_after_reader_release", true)
	r.ev.set("custom_send", op.describe())
	if err := op.requireFull(); err != nil {
		return err
	}
	ord := op.orderSeqs()
	if !(ord[0] > 0 && ord[0] < ord[1] && ord[1] < ord[2] && ord[2] < ord[3]) {
		return fmt.Errorf("order violation: fence=%d unpin=%d close_event=%d done=%d", ord[0], ord[1], ord[2], ord[3])
	}
	r.ev.set("helper_attested_order_fence_unpin_closeevent_done", ord)
	if err := pr.w.CloseWrite(); err != nil {
		return fmt.Errorf("CloseWrite: %w", err)
	}
	rd, err := ovRecv(readCh, "peer read")
	if err != nil {
		return err
	}
	if rd.err != nil {
		return fmt.Errorf("peer read: %w", rd.err)
	}
	gotSHA := ovSum(rd.b)
	r.ev.set("want_len", wantLen)
	r.ev.set("got_len", len(rd.b))
	r.ev.set("want_sha256", wantSHA)
	r.ev.set("got_sha256", gotSHA)
	if len(rd.b) != wantLen || gotSHA != wantSHA {
		return fmt.Errorf("pin stream mismatch: got %d bytes sha %s want %d sha %s", len(rd.b), gotSHA, wantLen, wantSHA)
	}
	return nil
}

// ---- scenario 3: paused reader, cancel + close ----

// ovConfigureUnbuffered applies the reviewed test-only zero-buffer diagnostic
// configuration and reads back the effective values. It is shared by the
// paused-reader-close and pin-lifetime-completion children only.
func ovConfigureUnbuffered(r *ovRun, pr *ovPair) error {
	// Only the paused-reader-close and pin-lifetime-completion children use
	// documented unbuffered socket options as a diagnostic pending-I/O fixture, never as production/Node socket policy.
	// The former 4096-byte requests did not bound transport acceptance: the
	// entire 24MiB operation completed immediately on both architectures.
	// Keep one original-size post; even this mode must fail inconclusive if
	// no pending operation is observed, rather than retry or enlarge it.
	r.ev.set("socket_configuration", "unbuffered-diagnostic")
	r.ev.set("requested_send_buffer", 0)
	r.ev.set("requested_receive_buffer", 0)
	r.ev.set("writer_endpoint", pr.w.LocalAddr().String())
	r.ev.set("peer_endpoint", pr.p.LocalAddr().String())
	if err := pr.w.SetWriteBuffer(0); err != nil {
		return fmt.Errorf("SetWriteBuffer: %w", err)
	}
	if err := pr.p.SetReadBuffer(0); err != nil {
		return fmt.Errorf("SetReadBuffer: %w", err)
	}
	for _, opt := range []struct {
		conn *net.TCPConn
		name string
		kind int
	}{
		{pr.w, "effective_send_buffer", syscall.SO_SNDBUF},
		{pr.p, "effective_receive_buffer", syscall.SO_RCVBUF},
	} {
		raw, err := opt.conn.SyscallConn()
		if err != nil {
			return fmt.Errorf("%s SyscallConn: %w", opt.name, err)
		}
		var value int
		var queryErr error
		if err := raw.Control(func(fd uintptr) {
			value, queryErr = syscall.GetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, opt.kind)
		}); err != nil {
			return fmt.Errorf("%s Control: %w", opt.name, err)
		}
		if queryErr != nil {
			return fmt.Errorf("%s getsockopt: %w", opt.name, queryErr)
		}
		r.ev.set(opt.name, value)
		if value != 0 {
			return fmt.Errorf("%s=%d: unbuffered diagnostic requires zero", opt.name, value)
		}
	}
	return nil
}

func ovAcceptedCloseReadErr(err error) bool {
	if err == nil {
		return true
	}
	var en syscall.Errno
	return errors.As(err, &en) && (en == ovWSAECONNRESET || en == ovWSAECONNABORTED)
}

func ovScenarioPausedClose(r *ovRun) error {
	pr, err := r.newPair()
	if err != nil {
		return err
	}
	rc, err := pr.w.SyscallConn()
	if err != nil {
		return err
	}
	if err := ovConfigureUnbuffered(r, pr); err != nil {
		return err
	}
	if _, err := newSocketTryWriter(pr.w); err != nil {
		return fmt.Errorf("newSocketTryWriter: %w", err)
	}
	gate := make(chan struct{})
	readCh := r.startReader(pr.p, gate)
	const mib8 = 8 << 20
	data := ovBytes(9, 3*mib8)
	op, err := r.submit("paused", rc, [][]byte{data[:mib8], data[mib8 : 2*mib8], data[2*mib8:]})
	r.ev.event("submit_returned")
	if err != nil {
		return err
	}
	pending, sig, rel, perr := op.pendingObserved()
	if perr != nil {
		return fmt.Errorf("peek event: %w", perr)
	}
	r.ev.set("pending_observed", pending)
	r.ev.set("post_class_at_post", op.post)
	r.ev.set("event_signaled_at_peek", sig)
	cancelClass := op.cancel()
	r.ev.event("cancel_issued:" + cancelClass)
	// Cancel must NOT close or invalidate the socket: the caller-owned conn is
	// still usable through RawConn.Control after Cancel returns.
	ranAfterCancel := false
	if cerr := rc.Control(func(uintptr) { ranAfterCancel = true }); cerr != nil || !ranAfterCancel {
		return fmt.Errorf("Cancel closed or invalidated the socket: ran=%v err=%v", ranAfterCancel, cerr)
	}
	r.ev.set("socket_open_after_cancel", true)
	// Cancel is idempotent and concurrency-safe: repeat it sequentially and from
	// two goroutines; every result must be a non-fault state.
	again := []string{op.cancel()}
	cc := make(chan string, 2)
	for i := 0; i < 2; i++ {
		r.spawn(func() { cc <- string(op.w.Cancel()) })
	}
	for i := 0; i < 2; i++ {
		v, rerr := ovRecv(cc, "concurrent Cancel")
		if rerr != nil {
			r.fatalUnfenced(rerr.Error())
		}
		again = append(again, v)
	}
	for _, v := range again {
		if !ovCancelOK(v) {
			return fmt.Errorf("repeat Cancel returned fault state %q (all: %v)", v, again)
		}
	}
	r.ev.set("repeat_cancel_results", again)
	op.closeIssued.Store(true)
	// NATIVE-MECHANISM EVIDENCE ONLY: this immediate caller Close right after
	// Cancel (before the completion is processed) probes the Windows kernel
	// behavior. It is NOT Node parity and NOT production policy; Node's physical
	// close ordering (cancel, process write completion, then close) remains the
	// caller/actor policy decided later.
	closeCh := make(chan error, 1)
	r.spawn(func() { closeCh <- pr.w.Close() })
	cerr, err := ovRecv(closeCh, "Close return")
	if err != nil {
		r.fatalUnfenced("Close did not return: " + err.Error())
	}
	r.ev.event("close_returned")
	if cerr != nil {
		return fmt.Errorf("Close: %w", cerr)
	}
	// Test the fence without allowing application reads to rescue it. This
	// wait is a bounded test observer, not a production response-close hold.
	if err := r.waitDone(op); err != nil {
		return err
	}
	r.ev.set("completion_before_reader_release", true)
	close(gate)
	r.ev.event("reader_released")
	rd, err := ovRecv(readCh, "peer read")
	if err != nil {
		return err
	}
	fin := op.finalize()
	r.ev.set("cancel_class", cancelClass)
	r.ev.set("custom_send", op.describe())
	r.ev.set("received_bytes", len(rd.b))
	r.ev.set("peer_read_error", fmt.Sprint(rd.err))
	if !ovAcceptedCloseReadErr(rd.err) {
		return fmt.Errorf("peer read ended with unaccepted error: %w", rd.err)
	}
	if len(rd.b) > len(data) || !bytes.Equal(rd.b, data[:len(rd.b)]) { // data read only after the fence
		return fmt.Errorf("received %d bytes are not a valid prefix of the payload", len(rd.b))
	}
	ord := op.orderSeqs()
	if !(ord[0] > 0 && ord[0] < ord[1] && ord[1] < ord[2] && ord[2] < ord[3]) {
		return fmt.Errorf("order violation: fence=%d unpin=%d close_event=%d done=%d", ord[0], ord[1], ord[2], ord[3])
	}
	r.ev.set("helper_attested_order_fence_unpin_closeevent_done", ord)
	switch fin.Class {
	case "aborted", "peer_abort_reset", "unknown_closed", "complete_short", "complete_full":
	default:
		return fmt.Errorf("unexpected final class %s", fin.Class)
	}
	if !pending {
		return fmt.Errorf("INCONCLUSIVE sizing: no pending overlapped operation observed (post=%s signaled=%v released=%v); cancellation not qualified, no retry/enlargement", op.post, sig, rel)
	}
	if cancelClass != "cancel_issued" {
		return fmt.Errorf("INCONCLUSIVE cancellation race: %s; no successful cancellation request observed, no retry", cancelClass)
	}
	return nil
}

// ---- scenario 4: closed before post / identity ----

func ovScenarioClosedIdentity(r *ovRun) error {
	a, err := r.newPair()
	if err != nil {
		return err
	}
	rc, err := a.w.SyscallConn()
	if err != nil {
		return err
	}
	var oldFD uintptr
	if err := rc.Control(func(fd uintptr) { oldFD = fd }); err != nil {
		return fmt.Errorf("pre-close Control: %w", err)
	}
	if err := a.w.Close(); err != nil {
		return fmt.Errorf("close writer: %w", err)
	}
	// (a) constructor on a closed fd.
	_, cerr := newSocketTryWriter(a.w)
	if cerr == nil || !errors.Is(cerr, net.ErrClosed) {
		return fmt.Errorf("constructor on closed fd: want net.ErrClosed, got %v", cerr)
	}
	var coe *net.OpError
	if !errors.As(cerr, &coe) || coe.Op != "raw-control" {
		return fmt.Errorf("constructor error is not raw-control OpError: %v", cerr)
	}
	// (b0) helper domain: invalid part lists are rejected BEFORE any resource is
	// created or any RawConn call is made (so the closed fd does not mask them).
	for i, bad := range [][][]byte{nil, {}, {nil}, {{}}, {ovBytes(2, 8), {}}} {
		d0 := windowsSocketWriteCounters()
		if d0 != (windowsSocketCounters{}) {
			return fmt.Errorf("domain case %d: counters not zero before: %+v", i, d0)
		}
		w, derr := submitWindowsSocketWrite(rc, bad)
		d1 := windowsSocketWriteCounters()
		if w != nil || derr == nil || !errors.Is(derr, errWindowsSocketWriteDomain) || errors.Is(derr, net.ErrClosed) {
			return fmt.Errorf("domain case %d: op=%v err=%v (want nil op, errWindowsSocketWriteDomain only)", i, w != nil, derr)
		}
		if d1 != d0 {
			return fmt.Errorf("domain case %d allocated or released resources: before=%+v after=%+v", i, d0, d1)
		}
	}
	// (b) post on a closed fd: zero ownership.
	c0 := windowsSocketWriteCounters()
	if c0 != (windowsSocketCounters{}) {
		return fmt.Errorf("closed-post: counters not zero before: %+v", c0)
	}
	op, perr := r.submit("closed", rc, [][]byte{ovBytes(3, 4096)})
	if op != nil || perr == nil || !errors.Is(perr, net.ErrClosed) {
		return fmt.Errorf("post on closed fd: op=%v err=%v (want nil op, net.ErrClosed)", op != nil, perr)
	}
	var poe *net.OpError
	if !errors.As(perr, &poe) || poe.Op != "raw-write" {
		return fmt.Errorf("post error is not raw-write OpError: %v", perr)
	}
	c1 := windowsSocketWriteCounters()
	dc, dk, dp, dq := c1.EventsCreated-c0.EventsCreated, c1.EventsClosed-c0.EventsClosed, c1.UnpinOps-c0.UnpinOps, c1.OpsPosted-c0.OpsPosted
	if dc != 0 || dk != 0 || dp != 0 || dq != 0 {
		return fmt.Errorf("closed-post exact deltas created=%d closed=%d unpinned=%d posted=%d (all must be 0)", dc, dk, dp, dq)
	}
	// (c) old fd is never usable outside callbacks: Control must refuse.
	ran := false
	if err := rc.Control(func(uintptr) { ran = true }); err == nil || ran || !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("closed-old Control: err=%v ran=%v", err, ran)
	}
	// (d) fresh sockets; record (do not force) numeric handle reuse.
	const fresh = 32
	reused := 0
	var freshNative string
	for i := 0; i < fresh; i++ {
		b, err := r.newPair()
		if err != nil {
			return fmt.Errorf("fresh pair %d: %w", i, err)
		}
		brc, err := b.w.SyscallConn()
		if err != nil {
			return err
		}
		var nfd uintptr
		if err := brc.Control(func(fd uintptr) { nfd = fd }); err != nil {
			return err
		}
		if nfd == oldFD {
			reused++
		}
		if i == 0 {
			if _, err := newSocketTryWriter(b.w); err != nil {
				return fmt.Errorf("fresh newSocketTryWriter: %w", err)
			}
			readCh := r.startReader(b.p, nil)
			data := ovBytes(11, 256<<10)
			want := ovSum(data)
			fop, err := r.submit("fresh", brc, [][]byte{data[:65536], data[65536:165536], data[165536:]})
			if err != nil {
				return err
			}
			if err := r.waitDone(fop); err != nil {
				return err
			}
			if err := fop.requireFull(); err != nil {
				return err
			}
			if err := b.w.CloseWrite(); err != nil {
				return err
			}
			rd, err := ovRecv(readCh, "fresh peer read")
			if err != nil {
				return err
			}
			if rd.err != nil || len(rd.b) != len(data) || ovSum(rd.b) != want {
				return fmt.Errorf("fresh stream mismatch: len=%d err=%v", len(rd.b), rd.err)
			}
			freshNative = fop.post
		}
		if err := b.w.Close(); err != nil {
			return err
		}
		if err := b.p.Close(); err != nil {
			return err
		}
	}
	r.ev.set("old_fd_hex_numeric_only", fmt.Sprintf("%#x", oldFD))
	r.ev.set("fresh_pairs", fresh)
	r.ev.set("numeric_handle_reuse_observed", reused > 0)
	r.ev.set("numeric_handle_reuse_count", reused)
	r.ev.set("fresh_stream_native_class", freshNative)
	r.ev.set("constructor_closed_error", cerr.Error())
	r.ev.set("post_closed_error", perr.Error())
	r.ev.set("closed_post_resource_delta", map[string]int64{"created": dc, "closed": dk, "unpinned": dp, "posted": dq})
	fc := windowsSocketWriteCounters()
	r.ev.set("fresh_op_counters", map[string]int64{"created": fc.EventsCreated, "closed": fc.EventsClosed, "unpinned": fc.UnpinOps, "posted": fc.OpsPosted})
	if fc != (windowsSocketCounters{EventsCreated: 1, EventsClosed: 1, UnpinOps: 1, OpsPosted: 1}) {
		return fmt.Errorf("fresh-native counters %+v want all 1", fc)
	}
	return nil
}

// ---- child / parent plumbing ----

func ovRunChild(t *testing.T, spec string) {
	fn := ovScenarios[spec]
	if fn == nil {
		t.Fatalf("unknown overlapped child spec %q", spec)
	}
	nonce := os.Getenv(ovNonceEnv)
	r := &ovRun{spec: spec, nonce: nonce, stop: make(chan struct{})}
	ovActive.Store(r)
	wd := time.AfterFunc(ovChildBudget, func() { r.fatalUnfenced("child budget exceeded") })
	defer wd.Stop()
	failure := ""
	func() {
		// PRECONDITION OWNED BY THE CALLER, NOT THE HELPER: the scenario
		// goroutine is the submitter and stays locked to one OS thread until
		// every operation it posted is fenced (the helper cannot verify this).
		// Deferred order is LIFO: recover, then cleanup (cancel/fence/join),
		// then Unlock.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer r.cleanup()
		defer func() {
			if x := recover(); x != nil {
				failure = fmt.Sprintf("panic: %v", x)
			}
		}()
		tid, err := ovThreadID()
		if err != nil {
			failure = err.Error()
			return
		}
		r.threadID = tid
		if c := windowsSocketWriteCounters(); c != (windowsSocketCounters{}) {
			failure = fmt.Sprintf("helper counters not zero at child start: %+v", c)
			return
		}
		if err := fn(r); err != nil {
			failure = err.Error()
		}
	}()
	// Every child's total is exactly one op that reached the protected callback
	// (closed-identity: only the fresh op; refused/domain/closed leave zero).
	if c := windowsSocketWriteCounters(); failure == "" && c != (windowsSocketCounters{EventsCreated: 1, EventsClosed: 1, UnpinOps: 1, OpsPosted: 1}) {
		failure = fmt.Sprintf("final helper counters %+v want created=closed=unpin=posted=1", c)
	}
	if failure == "" && r.ev.faultCount() > 0 {
		failure = "faults recorded"
	}
	r.emit(failure == "", failure)
	if failure != "" {
		t.Fatalf("overlapped child %s failed: %s", spec, failure)
	}
}

type ovOutcome struct {
	stdout, stderr string
	exit           int
	timedOut       bool
	runErr         error
}

func ovSpawn(t *testing.T, spec, nonce string) ovOutcome {
	t.Helper()
	limit := ovChildCap
	if dl, ok := t.Deadline(); ok {
		rem := time.Until(dl) - ovHeadroom
		if rem < 15*time.Second {
			t.Fatalf("not enough outer test deadline left for child %s (%v)", spec, rem)
		}
		limit = min(limit, rem)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWindowsOverlappedTailGoInterop$", "-test.count=1",
		"-test.timeout="+(limit+ovTimeoutSlack).String(), ovNonceArg+nonce)
	cmd.Env = append(os.Environ(), ovEnv+"="+spec, ovNonceEnv+"="+nonce)
	cmd.WaitDelay = ovWaitDelay
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	runErr := cmd.Run()
	o := ovOutcome{stdout: so.String(), stderr: se.String(), exit: -1, runErr: runErr, timedOut: ctx.Err() == context.DeadlineExceeded}
	if cmd.ProcessState != nil {
		o.exit = cmd.ProcessState.ExitCode()
	}
	return o
}

func ovParse(stdout string) (map[string]any, int) {
	var res map[string]any
	count := 0
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if rest, ok := strings.CutPrefix(line, ovResultTag); ok {
			count++
			if err := json.Unmarshal([]byte(rest), &res); err != nil {
				return nil, count
			}
		}
	}
	return res, count
}

// ovCheckPinReceipt strictly validates the pin-lifetime-completion details. It
// uses only checked type assertions and exact comparisons, so a missing or
// wrong-typed field fails (never panics, never passes). JSON numbers decode as
// float64.
func ovCheckPinReceipt(details any) error {
	d, ok := details.(map[string]any)
	if !ok {
		return fmt.Errorf("pin receipt: details missing or not an object: %T", details)
	}
	const mib8 = float64(8 << 20)
	for k, want := range map[string]any{
		"socket_configuration":                "unbuffered-diagnostic",
		"requested_send_buffer":               float64(0),
		"requested_receive_buffer":            float64(0),
		"effective_send_buffer":               float64(0),
		"effective_receive_buffer":            float64(0),
		"post_class_at_post":                  "native997",
		"native_class":                        "native997",
		"pending_observed":                    true,
		"event_signaled_at_peek":              false,
		"go_read_marker_len":                  float64(20),
		"pending_after_go_read":               true,
		"go_read_while_pending":               true,
		"gc_rounds":                           float64(8),
		"pins_held_during_gc":                 true,
		"pending_after_gc":                    true,
		"gc_while_pending":                    true,
		"pins_released_before_reader_release": float64(0),
		"events_created_while_pending":        float64(1),
		"ops_posted_while_pending":            float64(1),
		"events_closed_before_reader_release": float64(0),
		"completed_before_reader_release":     false,
		"completion_after_reader_release":     true,
		"want_len":                            mib8,
		"got_len":                             mib8,
	} {
		got, present := d[k]
		if !present || got != want {
			return fmt.Errorf("pin receipt: %s=%v (present=%v) want %v", k, got, present, want)
		}
	}
	ws, wok := d["want_sha256"].(string)
	gs, gok := d["got_sha256"].(string)
	if !wok || !gok || len(ws) != 64 || ws != gs {
		return fmt.Errorf("pin receipt: want_sha256=%v got_sha256=%v must be equal 64-char strings", d["want_sha256"], d["got_sha256"])
	}
	cs, ok := d["custom_send"].(map[string]any)
	if !ok {
		return fmt.Errorf("pin receipt: custom_send missing or not an object: %T", d["custom_send"])
	}
	for k, want := range map[string]any{
		"post_class":  "native997",
		"post_errno":  float64(syscall.ERROR_IO_PENDING),
		"final_class": "complete_full",
		"final_n":     mib8,
		"bytes":       mib8,
		"final_errno": float64(0),
		"control_err": "",
	} {
		got, present := cs[k]
		if !present || got != want {
			return fmt.Errorf("pin receipt: custom_send.%s=%v (present=%v) want %v", k, got, present, want)
		}
	}
	ord, ok := d["helper_attested_order_fence_unpin_closeevent_done"].([]any)
	if !ok || len(ord) != 4 {
		return fmt.Errorf("pin receipt: helper_attested_order_fence_unpin_closeevent_done missing or not 4 elements: %v", d["helper_attested_order_fence_unpin_closeevent_done"])
	}
	var seq [4]float64
	for i, v := range ord {
		f, fok := v.(float64)
		if !fok {
			return fmt.Errorf("pin receipt: order element %d not a number: %T", i, v)
		}
		seq[i] = f
	}
	if !(seq[0] > 0 && seq[0] < seq[1] && seq[1] < seq[2] && seq[2] < seq[3]) {
		return fmt.Errorf("pin receipt: want 0 < fence < unpin < event-close < done (helper-attested), got %v", seq)
	}
	return nil
}

// TestWindowsOverlappedTailGoInterop spawns exactly four bounded native child
// processes (one per scenario). It is evidence gathering only; it is not
// production policy and not Node parity approval.
func TestWindowsOverlappedTailGoInterop(t *testing.T) {
	if spec := os.Getenv(ovEnv); spec != "" {
		// Child mode: never spawns children; refuses an ambient spec lacking
		// the per-parent nonce in both env and argv.
		want := ovNonceArg + os.Getenv(ovNonceEnv)
		if os.Getenv(ovNonceEnv) == "" || len(os.Args) == 0 || os.Args[len(os.Args)-1] != want {
			t.Fatalf("%s is set without a matching per-parent nonce; refusing to run as a child", ovEnv)
		}
		ovRunChild(t, spec)
		return
	}
	nb := make([]byte, 16)
	if _, err := rand.Read(nb); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(nb)
	for _, spec := range ovSpecs {
		t.Run(spec, func(t *testing.T) {
			o := ovSpawn(t, spec, nonce)
			t.Logf("child %s: exit=%d timedOut=%v runErr=%v\n--- stdout ---\n%s\n--- stderr ---\n%s", spec, o.exit, o.timedOut, o.runErr, o.stdout, o.stderr)
			if o.timedOut {
				t.Fatalf("child %s exceeded its cap and was killed", spec)
			}
			if o.exit != 0 {
				t.Fatalf("child %s exit=%d err=%v", spec, o.exit, o.runErr)
			}
			res, n := ovParse(o.stdout)
			if n != 1 || res == nil {
				t.Fatalf("child %s: want exactly one valid %q line, got %d", spec, ovResultTag, n)
			}
			for k, want := range map[string]any{
				"source": ovSource, "nonce": nonce, "scenario": spec,
				"goos": runtime.GOOS, "goarch": runtime.GOARCH, "ok": true,
			} {
				if res[k] != want {
					t.Fatalf("child %s: %s=%v want %v", spec, k, res[k], want)
				}
			}
			// The typed faults array must exist and be empty.
			if fl, ok := res["faults"].([]any); !ok || len(fl) != 0 {
				t.Fatalf("child %s: faults must be an existing empty array, got %v", spec, res["faults"])
			}
			if cl, ok := res["cleanup"].(map[string]any); !ok || cl["error"] != "" {
				t.Fatalf("child %s: cleanup must exist with empty error, got %v", spec, res["cleanup"])
			}
			if res["order_source"] != "helper_attested" {
				t.Fatalf("child %s: order_source=%v want helper_attested", spec, res["order_source"])
			}
			// Exact positive counter gates for all four children: one op reached
			// the protected callback, created=closed=unpinned=posted=1.
			cl := res["cleanup"].(map[string]any)
			for _, k := range []string{"events_created", "events_closed", "pins_released", "ops_posted"} {
				if v, vok := cl[k].(float64); !vok || v != 1 {
					t.Fatalf("child %s: cleanup.%s=%v want exactly 1", spec, k, cl[k])
				}
			}
			if spec == "paused-reader-close" {
				d, ok := res["details"].(map[string]any)
				if !ok {
					t.Fatalf("child %s: missing details", spec)
				}
				for k, want := range map[string]any{
					"socket_configuration":             "unbuffered-diagnostic",
					"requested_send_buffer":            float64(0),
					"requested_receive_buffer":         float64(0),
					"effective_send_buffer":            float64(0),
					"effective_receive_buffer":         float64(0),
					"pending_observed":                 true,
					"post_class_at_post":               "native997",
					"cancel_class":                     "cancel_issued",
					"socket_open_after_cancel":         true,
					"completion_before_reader_release": true,
				} {
					if d[k] != want {
						t.Fatalf("child %s: %s=%v want %v", spec, k, d[k], want)
					}
				}
			}
			if spec == "pin-lifetime-completion" {
				if err := ovCheckPinReceipt(res["details"]); err != nil {
					t.Fatalf("child %s: %v", spec, err)
				}
			}
			// Parent-side equality of the stream hash/length pairs the children
			// report (the child's own checks are unchanged).
			if spec == "healthy-interoperability" || spec == "pin-lifetime-completion" {
				d, ok := res["details"].(map[string]any)
				if !ok {
					t.Fatalf("child %s: missing details", spec)
				}
				for _, pair := range [][2]string{{"want_sha256", "got_sha256"}, {"want_len", "got_len"}} {
					a, aok := d[pair[0]]
					b, bok := d[pair[1]]
					if !aok || !bok || a != b {
						t.Fatalf("child %s: %s=%v != %s=%v", spec, pair[0], a, pair[1], b)
					}
				}
			}
		})
	}
}
