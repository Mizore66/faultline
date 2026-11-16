//go:build windows

package proofview

// STATIC, UNEXECUTED, TEST-ONLY prototype (future overlay target:
// internal/proofview/socket_overlapped_windows_test.go).
//
// It probes whether a test-local, event-based OVERLAPPED WSASend can be posted
// directly inside RawConn.Write on a Go-owned, IOCP-associated TCP socket,
// return to the caller after the native post but before any peer read, and be
// fenced by its own event without disturbing Go's netpoller.
//
// Nothing here is production code and nothing here has been compiled or run.
// Every claim about native behavior is UNKNOWN until the existing Windows CI
// executes it.
//
// Facts relied on (all primary documentation relayed by the parent, or Go
// 1.27.4 source read):
//   - OVERLAPPED.hEvent: a VALID event handle with the low-order bit set
//     suppresses the completion packet for an IOCP-associated handle; wait on
//     and close the ORIGINAL handle (GetQueuedCompletionStatus docs).
//   - WSASend: return 0 = completed immediately; WSA_IO_PENDING (997) = started;
//     any other error = no completion will occur. lpNumberOfBytesSent must be
//     NULL when lpOverlapped is non-NULL. The OVERLAPPED, the WSABUF array (kept
//     conservatively) and every referenced byte array must stay valid until
//     completion. All I/O initiated by a thread is canceled when it exits.
//   - closesocket returning is NOT a completion fence; the handle may be reused.
//   - WSAGetOverlappedResult(fWait=FALSE): FALSE means pending OR failed OR bad
//     parameters; the transfer count is not updated on FALSE.
//   - Go 1.27.4 syscall.SyscallN runs SetLastError(0) before the call and reads
//     the thread's last error right after it, so the errno returned by
//     Proc.Call / syscall.WSASend is the post-call thread last error. A separate
//     WSAGetLastError call through SyscallN would observe a zeroed value, so it
//     is NOT used; WSAGetLastError==GetLastError aliasing is not independently
//     proven here and is recorded as such in the evidence.

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
	"unsafe"
)

const (
	ovEnv       = "FAULTLINE_WINDOWS_OVERLAPPED_CHILD"
	ovNonceEnv  = "FAULTLINE_WINDOWS_OVERLAPPED_NONCE"
	ovNonceArg  = "faultline-overlapped-nonce="
	ovSource    = "faultline-windows-overlapped-probe-v1"
	ovResultTag = "OV_EVIDENCE "

	// Failure-only guards; none is used as a behavior heuristic.
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
)

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
	procs       *ovProcs

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	mu      sync.Mutex
	ops     []*ovOp
	closers []io.Closer

	eventsCreated atomic.Int32
	eventsClosed  atomic.Int32
	pinsReleased  atomic.Int32
	opsPosted     atomic.Int32
	goroutines    atomic.Int32

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
	r.ev.mu.Lock()
	out := map[string]any{
		"source": ovSource, "nonce": r.nonce, "scenario": r.spec,
		"goos": runtime.GOOS, "goarch": runtime.GOARCH, "go": runtime.Version(),
		"ok": ok, "events": append([]string{}, r.ev.events...),
		"faults":  append([]string{}, r.ev.faults...),
		"details": r.ev.fields,
		"cleanup": map[string]any{
			"events_created": r.eventsCreated.Load(), "events_closed": r.eventsClosed.Load(),
			"pins_released": r.pinsReleased.Load(), "ops_posted": r.opsPosted.Load(),
			"goroutines_spawned": r.goroutines.Load(), "error": r.cleanupErr,
		},
		"errno_mapping": "SyscallN last-error after the call (SetLastError(0) precedes it); separate WSAGetLastError call not used; aliasing to GetLastError not independently proven",
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

// fatalUnfenced is the only exit when a possibly kernel-owned operation cannot
// be fenced (or the child budget is exhausted). It never unpins or frees: the
// process exit reclaims everything, and the parent fails on the exit code.
func (r *ovRun) fatalUnfenced(reason string) {
	r.ev.fault("FATAL unfenced/budget: " + reason)
	r.emit(false, "FATAL unfenced/budget: "+reason)
	os.Exit(ovExitUnfenced)
}

// ---- dynamic system procs ----

type ovProcs struct {
	createEvent *syscall.LazyProc
	getResult   *syscall.LazyProc
}

// ovResolve resolves every needed proc before any I/O. syscall.LoadDLL loads
// names registered by the syscall package ("kernel32.dll", "ws2_32.dll") with
// LOAD_LIBRARY_SEARCH_SYSTEM32 only; there is no other DLL fallback.
func ovResolve() (*ovProcs, error) {
	p := &ovProcs{
		createEvent: syscall.NewLazyDLL("kernel32.dll").NewProc("CreateEventW"),
		getResult:   syscall.NewLazyDLL("ws2_32.dll").NewProc("WSAGetOverlappedResult"),
	}
	for _, pr := range []*syscall.LazyProc{p.createEvent, p.getResult} {
		if err := pr.Find(); err != nil {
			return nil, fmt.Errorf("resolve %s: %w", pr.Name, err)
		}
	}
	return p, nil
}

// newEvent creates an unnamed manual-reset, initially non-signaled event.
func (p *ovProcs) newEvent() (syscall.Handle, error) {
	r1, _, e := p.createEvent.Call(0, 1, 0, 0)
	if r1 == 0 {
		var en syscall.Errno
		if x, ok := e.(syscall.Errno); ok {
			en = x
		}
		return 0, fmt.Errorf("CreateEventW failed: errno=%d", uint32(en))
	}
	return syscall.Handle(r1), nil
}

type ovQuery struct {
	OK    bool
	N     uint32
	Flags uint32
	Errno syscall.Errno
}

// result is one non-waiting WSAGetOverlappedResult. The transfer count is only
// returned when the call reports TRUE; on FALSE it is never read. It must be
// called inside a RawConn callback so the socket identity is protected.
func (p *ovProcs) result(h syscall.Handle, ov *syscall.Overlapped) ovQuery {
	var n, flags uint32
	r1, _, e := p.getResult.Call(uintptr(h), uintptr(unsafe.Pointer(ov)), uintptr(unsafe.Pointer(&n)), 0, uintptr(unsafe.Pointer(&flags)))
	if uint32(r1) != 0 { // BOOL is 32 bits
		return ovQuery{OK: true, N: n, Flags: flags}
	}
	var en syscall.Errno
	if x, ok := e.(syscall.Errno); ok {
		en = x
	}
	return ovQuery{Errno: en}
}

// ovClassify maps a completed-operation query to a strict class. closeIssued is
// true only when this probe has already issued CancelIoEx/Close for the
// operation. Every errno that is not a named, explained consequence of that
// local cancel/close (including wrong handle, bad parameter and mode errors) is
// a fault and can never pass.
func ovClassify(q ovQuery, total int, closeIssued bool) (class string, fault bool) {
	if q.OK {
		switch {
		case int64(q.N) == int64(total):
			return "complete_full", false
		case int64(q.N) < int64(total):
			return "complete_short", false
		default:
			return "fault_count_overrun", true
		}
	}
	switch q.Errno {
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

// ---- overlapped operation ----

type ovFinal struct {
	Class      string
	N          uint32
	Errno      uint32
	ControlErr string
}

type ovOp struct {
	run     *ovRun
	name    string
	rc      syscall.RawConn
	regions [][]byte // strong refs to the exact payload backers
	total   int

	ov   *syscall.Overlapped
	bufs []syscall.WSABuf
	ev   syscall.Handle // ORIGINAL handle: waited on and closed
	pin  runtime.Pinner

	postClass string // not_posted | native0 | native997 | native_error
	postErrno uint32
	syncQ     ovQuery // native0 only

	done  chan struct{} // closed after fence, release and final are all set
	final ovFinal

	mu       sync.Mutex
	released bool
	evClosed bool

	closeIssued atomic.Bool // set before CancelIoEx / Close is issued

	seqFence, seqUnpin, seqClose int
}

type ovPostError struct{ Errno syscall.Errno }

func (e *ovPostError) Error() string {
	return fmt.Sprintf("WSASend post error errno=%d (no completion will occur)", uint32(e.Errno))
}

// submit posts one event-based OVERLAPPED WSASend of regions directly inside
// RawConn.Write (callback always returns true). It returns after the native
// post and never waits for the peer. The caller's goroutine MUST be the locked
// submitter thread and MUST keep it locked until op.done is closed.
//
// Contract: payload memory is not read or modified by this code after the post
// until the fence. A nil op is returned only when nothing was posted and all
// resources were released.
func (r *ovRun) submit(name string, rc syscall.RawConn, regions [][]byte) (*ovOp, error) {
	total := 0
	for _, g := range regions {
		if len(g) == 0 {
			return nil, errors.New("submit: empty region")
		}
		total += len(g)
	}
	if total == 0 || total > ovPayloadCap {
		return nil, fmt.Errorf("submit: total %d outside (0,%d]", total, ovPayloadCap)
	}
	o := &ovOp{run: r, name: name, rc: rc, regions: regions, total: total, done: make(chan struct{}), postClass: "not_posted"}
	h, err := r.procs.newEvent()
	if err != nil {
		return nil, err
	}
	r.eventsCreated.Add(1)
	o.ev = h
	o.ov = new(syscall.Overlapped)
	o.ov.HEvent = syscall.Handle(uintptr(h) | 1) // low bit: suppress the IOCP packet
	o.bufs = make([]syscall.WSABuf, 0, len(regions))
	for _, g := range regions {
		o.bufs = append(o.bufs, syscall.WSABuf{Len: uint32(len(g)), Buf: &g[0]})
	}
	// Pin the actual OVERLAPPED, the WSABUF backing array and every referenced
	// byte array (not slice headers). Released only after the fence.
	o.pin.Pin(o.ov)
	o.pin.Pin(&o.bufs[0])
	for _, g := range regions {
		o.pin.Pin(&g[0])
	}
	r.mu.Lock()
	r.ops = append(r.ops, o)
	r.mu.Unlock()

	called := false
	var postErr error
	werr := rc.Write(func(fd uintptr) bool {
		called = true
		hd := syscall.Handle(fd)
		postErr = syscall.WSASend(hd, &o.bufs[0], uint32(len(o.bufs)), nil, 0, o.ov, nil)
		switch {
		case postErr == nil:
			o.postClass = "native0"
			// Completed immediately: obtain the real count with a non-waiting
			// query inside this protected callback.
			o.syncQ = r.procs.result(hd, o.ov)
		case postErr == syscall.ERROR_IO_PENDING:
			o.postClass = "native997"
			o.postErrno = uint32(syscall.ERROR_IO_PENDING)
		default:
			o.postClass = "native_error"
			var en syscall.Errno
			if errors.As(postErr, &en) {
				o.postErrno = uint32(en)
			}
		}
		return true
	})
	if !called {
		// RawConn.Write refused (closed/closing): nothing was posted.
		o.release()
		close(o.done)
		if werr == nil {
			err := errors.New("internal fault: RawConn.Write returned nil without running its callback; nothing was posted")
			r.ev.fault(err.Error())
			return nil, err
		}
		return nil, werr
	}
	if werr != nil {
		r.ev.fault("RawConn.Write returned an error after its callback ran: " + werr.Error())
	}
	r.opsPosted.Add(1)
	switch o.postClass {
	case "native0":
		o.seqFence = r.ev.event("fence_native0:" + name)
		class, f := ovClassify(o.syncQ, total, o.closeIssued.Load())
		if f || class != "complete_full" && class != "complete_short" {
			r.ev.fault(fmt.Sprintf("%s: native0 confirmed-result query class=%s errno=%d", name, class, uint32(o.syncQ.Errno)))
		}
		o.final = ovFinal{Class: class, N: o.syncQ.N, Errno: uint32(o.syncQ.Errno)}
		o.release() // return 0 means the operation is complete
		close(o.done)
	case "native997":
		r.spawn(func() { r.waiter(o) })
	default:
		o.final = ovFinal{Class: "native_error_no_completion", Errno: o.postErrno}
		o.release() // no completion will occur
		close(o.done)
		return o, &ovPostError{Errno: syscall.Errno(o.postErrno)}
	}
	return o, nil
}

// waiter is the single waiter for a native997 op. It never runs application
// callbacks; it only fences, classifies, releases and signals o.done.
func (r *ovRun) waiter(o *ovOp) {
	st, werr := syscall.WaitForSingleObject(o.ev, syscall.INFINITE)
	if werr != nil || st != syscall.WAIT_OBJECT_0 {
		// Completion NOT observed: never release.
		r.fatalUnfenced(fmt.Sprintf("%s: WaitForSingleObject st=%d err=%v", o.name, st, werr))
		return
	}
	o.seqFence = r.ev.event("fence_event:" + o.name)
	var q ovQuery
	ran := false
	cerr := o.rc.Control(func(fd uintptr) {
		ran = true
		q = r.procs.result(syscall.Handle(fd), o.ov)
	})
	switch {
	case cerr != nil && !ran && errors.Is(cerr, net.ErrClosed):
		o.final = ovFinal{Class: "unknown_closed", ControlErr: cerr.Error()}
	case cerr != nil || !ran:
		r.ev.fault(fmt.Sprintf("%s: Control fault ran=%v err=%v", o.name, ran, cerr))
		o.final = ovFinal{Class: "fault_control", ControlErr: fmt.Sprint(cerr)}
	default:
		class, f := ovClassify(q, o.total, o.closeIssued.Load())
		if f {
			r.ev.fault(fmt.Sprintf("%s: result query class=%s errno=%d", o.name, class, uint32(q.Errno)))
		}
		o.final = ovFinal{Class: class, N: q.N, Errno: uint32(q.Errno)}
	}
	o.release() // strictly after the fence: Unpin, then CloseHandle
	close(o.done)
}

// release unpins and then closes the event. The caller guarantees the fence
// (native0 return, event observed, or nothing posted).
func (o *ovOp) release() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.released {
		return
	}
	o.released = true
	o.pin.Unpin()
	o.run.pinsReleased.Add(1)
	o.seqUnpin = o.run.ev.event("unpin:" + o.name)
	if err := syscall.CloseHandle(o.ev); err != nil {
		o.run.ev.fault(fmt.Sprintf("%s: CloseHandle: %v", o.name, err))
	}
	o.evClosed = true
	o.run.eventsClosed.Add(1)
	o.seqClose = o.run.ev.event("close_event:" + o.name)
	runtime.KeepAlive(o.regions)
	runtime.KeepAlive(o.bufs)
}

// peekSignaled does one zero-timeout poll of the event while it is guaranteed
// not to be closed (guarded by o.mu against release).
func (o *ovOp) peekSignaled() (signaled bool, released bool, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.evClosed {
		return false, true, nil
	}
	st, werr := syscall.WaitForSingleObject(o.ev, 0)
	if werr != nil {
		return false, false, werr
	}
	switch st {
	case syscall.WAIT_OBJECT_0:
		return true, false, nil
	case syscall.WAIT_TIMEOUT:
		return false, false, nil
	}
	return false, false, fmt.Errorf("WaitForSingleObject(0) unexpected status %d", st)
}

// cancel issues CancelIoEx on this operation's OVERLAPPED inside Control.
// ERROR_NOT_FOUND may mean the operation already completed (race).
func (o *ovOp) cancel() string {
	o.closeIssued.Store(true) // before the cancel, so a later query sees it
	select {
	case <-o.done:
		return "already_done"
	default:
	}
	var cerr error
	ran := false
	err := o.rc.Control(func(fd uintptr) {
		ran = true
		cerr = syscall.CancelIoEx(syscall.Handle(fd), o.ov)
	})
	switch {
	case err != nil && !ran && errors.Is(err, net.ErrClosed):
		return "control_closed_no_cancel"
	case err != nil || !ran:
		o.run.ev.fault(fmt.Sprintf("%s: cancel Control fault ran=%v err=%v", o.name, ran, err))
		return "fault_control"
	case cerr == nil:
		return "cancel_issued"
	case cerr == syscall.ERROR_NOT_FOUND:
		return "not_found_completed_race"
	}
	o.run.ev.fault(fmt.Sprintf("%s: CancelIoEx: %v", o.name, cerr))
	return "fault_cancel"
}

// requireFull demands a fully completed send (used by non-cancel scenarios).
func (o *ovOp) requireFull() error {
	if o.final.Class != "complete_full" {
		return fmt.Errorf("%s: send final class=%s n=%d errno=%d control=%q (want complete_full)", o.name, o.final.Class, o.final.N, o.final.Errno, o.final.ControlErr)
	}
	if int(o.final.N) != o.total {
		return fmt.Errorf("%s: send count %d != %d", o.name, o.final.N, o.total)
	}
	return nil
}

func (o *ovOp) describe() map[string]any {
	return map[string]any{
		"post_class": o.postClass, "post_errno": o.postErrno, "bytes": o.total,
		"final_class": o.final.Class, "final_n": o.final.N, "final_errno": o.final.Errno,
		"control_err": o.final.ControlErr,
	}
}

// ---- cleanup (runs on the still-locked submitter thread) ----

func (r *ovRun) cleanup() {
	r.stopOnce.Do(func() { close(r.stop) })
	r.mu.Lock()
	ops := append([]*ovOp(nil), r.ops...)
	r.mu.Unlock()
	// 1. cancel while the submitter thread is alive.
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
	// 3. fence every op; an unfenceable op kills the child, never unpins.
	for _, o := range ops {
		select {
		case <-o.done:
		case <-time.After(ovGuard):
			r.fatalUnfenced("fence not observed for " + o.name)
		}
	}
	// 4. join goroutines.
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
	if c, e := r.eventsCreated.Load(), r.eventsClosed.Load(); c != e {
		leaks = append(leaks, fmt.Sprintf("events created %d closed %d", c, e))
	}
	for _, o := range ops {
		o.mu.Lock()
		if !o.released || !o.evClosed {
			leaks = append(leaks, o.name+" not released")
		}
		o.mu.Unlock()
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

func (r *ovRun) waitDone(o *ovOp) error {
	select {
	case <-o.done:
		return nil
	case <-time.After(ovGuard):
		r.fatalUnfenced("completion not delivered for " + o.name)
		return nil
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
	pendingAtMarker := !ovClosed(op.done)
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
	r.ev.set("native_class", op.postClass)
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
// return are the operation's own strong refs (the caller's locals are dead).
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

// ovStillPending is a non-waiting observation: native997 at post, event not
// signaled, event not closed/released, done not closed.
func ovStillPending(op *ovOp) (bool, bool, error) {
	sig, rel, err := op.peekSignaled()
	if err != nil {
		return false, sig, err
	}
	return op.postClass == "native997" && !sig && !rel && !ovClosed(op.done), sig, nil
}

// ovScenarioPin is a pin-lifetime/pending-completion SMOKE test. The op keeps
// strong references to the payload and Go's heap does not move objects, so a
// missing pin would NOT make this fail; it is not proof of pin correctness.
// It uses the same reviewed unbuffered diagnostic configuration as the paused
// child and the original single 8MiB payload (no retry, enlargement or sleep).
// It directly requires: (1) pending (native997, unsignaled) before reader
// release, (2) an exact ordinary Go READ of a peer-written marker on the
// custom-write-owned socket while that send is still pending (no ordinary Go
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
	r.spawn(func() { // ordinary bounded Go READ on the custom-write-owned socket
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
	r.ev.set("post_class_at_post", op.postClass)
	r.ev.set("native_class", op.postClass)
	pending, sig, perr := ovStillPending(op)
	if perr != nil {
		return fmt.Errorf("peek event: %w", perr)
	}
	r.ev.set("event_signaled_at_peek", sig)
	r.ev.set("pending_observed", pending)
	r.ev.set("go_read_while_pending", false)
	r.ev.set("pending_after_go_read", false)
	r.ev.set("gc_while_pending", false)
	r.ev.set("pending_after_gc", false)
	r.ev.set("completed_before_reader_release", ovClosed(op.done))
	if !pending {
		r.ev.set("coverage_pending_pin_path", "UNKNOWN: pending path not observed")
		return fmt.Errorf("INCONCLUSIVE sizing: no pending overlapped operation observed (post=%s signaled=%v); pending completion not qualified, no retry/enlargement", op.postClass, sig)
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
	pendingAfterRead, _, perr := ovStillPending(op)
	if perr != nil {
		return fmt.Errorf("peek event after read: %w", perr)
	}
	r.ev.set("pending_after_go_read", pendingAfterRead)
	r.ev.set("go_read_while_pending", pendingAfterRead)
	if !pendingAfterRead {
		return errors.New("INCONCLUSIVE coverage: custom send no longer pending after Go read; marker read not proven concurrent, no retry")
	}

	// (3) Existing GC rounds while the op is pending and its pins are held.
	pinsBefore, closedBefore := r.pinsReleased.Load(), r.eventsClosed.Load()
	rounds := ovGCRounds()
	r.ev.event("gc_rounds_done")
	pendingAfterGC, _, perr := ovStillPending(op)
	if perr != nil {
		return fmt.Errorf("peek event after gc: %w", perr)
	}
	pinsAfter, closedAfter := r.pinsReleased.Load(), r.eventsClosed.Load()
	r.ev.set("gc_rounds", rounds)
	r.ev.set("pending_after_gc", pendingAfterGC)
	r.ev.set("pins_released_before_reader_release", pinsAfter)
	r.ev.set("events_closed_before_reader_release", closedAfter)
	pinsHeld := pinsBefore == 0 && pinsAfter == 0 && closedBefore == 0 && closedAfter == 0
	r.ev.set("pins_held_during_gc", pinsHeld)
	r.ev.set("gc_while_pending", pendingAfterGC && pinsHeld)
	doneBeforeRelease := ovClosed(op.done)
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
	if !(op.seqFence > 0 && op.seqFence < op.seqUnpin && op.seqUnpin < op.seqClose) {
		return fmt.Errorf("order violation: fence=%d unpin=%d close_event=%d", op.seqFence, op.seqUnpin, op.seqClose)
	}
	r.ev.set("order_fence_unpin_closeevent", []int{op.seqFence, op.seqUnpin, op.seqClose})
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
		return fmt.Errorf("pin stream mismatch: got %d sha %s want %d sha %s", len(rd.b), gotSHA, wantLen, wantSHA)
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
	sig, rel, perr := op.peekSignaled()
	if perr != nil {
		return fmt.Errorf("peek event: %w", perr)
	}
	pending := op.postClass == "native997" && !sig && !rel && !ovClosed(op.done)
	r.ev.set("pending_observed", pending)
	r.ev.set("post_class_at_post", op.postClass)
	r.ev.set("event_signaled_at_peek", sig)
	cancelClass := op.cancel()
	r.ev.event("cancel_issued:" + cancelClass)
	op.closeIssued.Store(true)
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
	if !(op.seqFence > 0 && op.seqFence < op.seqUnpin && op.seqUnpin < op.seqClose) {
		return fmt.Errorf("order violation: fence=%d unpin=%d close_event=%d", op.seqFence, op.seqUnpin, op.seqClose)
	}
	r.ev.set("order_fence_unpin_closeevent", []int{op.seqFence, op.seqUnpin, op.seqClose})
	switch op.final.Class {
	case "aborted", "peer_abort_reset", "unknown_closed", "complete_short", "complete_full":
	default:
		return fmt.Errorf("unexpected final class %s", op.final.Class)
	}
	if !pending {
		return fmt.Errorf("INCONCLUSIVE sizing: no pending overlapped operation observed (post=%s signaled=%v released=%v); cancellation not qualified, no retry/enlargement", op.postClass, sig, rel)
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
	// (b) post on a closed fd: zero ownership.
	c0, k0, p0 := r.eventsCreated.Load(), r.eventsClosed.Load(), r.pinsReleased.Load()
	op, perr := r.submit("closed", rc, [][]byte{ovBytes(3, 4096)})
	if op != nil || perr == nil || !errors.Is(perr, net.ErrClosed) {
		return fmt.Errorf("post on closed fd: op=%v err=%v (want nil op, net.ErrClosed)", op != nil, perr)
	}
	var poe *net.OpError
	if !errors.As(perr, &poe) || poe.Op != "raw-write" {
		return fmt.Errorf("post error is not raw-write OpError: %v", perr)
	}
	dc, dk, dp := r.eventsCreated.Load()-c0, r.eventsClosed.Load()-k0, r.pinsReleased.Load()-p0
	if dc != dk || dk != dp || r.opsPosted.Load() != 0 {
		return fmt.Errorf("closed-post resource delta created=%d closed=%d unpinned=%d posted=%d", dc, dk, dp, r.opsPosted.Load())
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
			freshNative = fop.postClass
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
	r.ev.set("closed_post_resource_delta", map[string]int32{"created": dc, "closed": dk, "unpinned": dp})
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
		// The scenario goroutine is the submitter: it stays locked to one OS
		// thread until every operation it posted is fenced. Deferred order is
		// LIFO: recover, then cleanup (cancel/fence/join), then Unlock.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer r.cleanup()
		defer func() {
			if x := recover(); x != nil {
				failure = fmt.Sprintf("panic: %v", x)
			}
		}()
		procs, err := ovResolve()
		if err != nil {
			failure = err.Error()
			return
		}
		r.procs = procs
		if err := fn(r); err != nil {
			failure = err.Error()
		}
	}()
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
	ord, ok := d["order_fence_unpin_closeevent"].([]any)
	if !ok || len(ord) != 3 {
		return fmt.Errorf("pin receipt: order_fence_unpin_closeevent missing or not 3 elements: %v", d["order_fence_unpin_closeevent"])
	}
	var seq [3]float64
	for i, v := range ord {
		f, fok := v.(float64)
		if !fok {
			return fmt.Errorf("pin receipt: order element %d not a number: %T", i, v)
		}
		seq[i] = f
	}
	if !(seq[0] > 0 && seq[0] < seq[1] && seq[1] < seq[2]) {
		return fmt.Errorf("pin receipt: want 0 < fence < unpin < event-close, got %v", seq)
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
			if ec, ok1 := res["cleanup"].(map[string]any)["events_created"].(float64); !ok1 || ec != res["cleanup"].(map[string]any)["events_closed"] {
				t.Fatalf("child %s: events created/closed mismatch: %v", spec, res["cleanup"])
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
