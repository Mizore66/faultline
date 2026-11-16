//go:build linux || darwin || windows

package proofview

// STATIC, UNEXECUTED, TEST-FIRST helper-internal fault-path tests (T1-T14 plus
// partial-setup rollback). Companion: UNIT-CONTRACT.md. fix1/CONTRACT.md is the
// behavioral contract. The production helper does not exist yet: this file is
// expected to fail to compile (RED) with undefined symbols only.
//
// Nothing here makes a native call. All native behavior is a per-instance fake
// (hxNative) injected through newWindowsSocketWriteRuntime; the fake handles are
// synthetic numbers, NOT live Winsock objects. The fake pinner records pins and
// never uses runtime.Pinner, so a quarantined (never released) op cannot trigger
// the runtime's unreachable-pinner leak panic. The tests drive the REAL future
// state machine (registry, leases, fence, release, Done/FenceFailed) and assert
// on its Snapshot/counters/registry, not merely on which fake calls were made.

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"net"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"weak"
)

const (
	hxWaitObject0 = 0
	hxWaitTimeout = 258
	hxWaitFailed  = 0xFFFFFFFF
	hxIOPending   = syscall.Errno(997) // independent of the core constant on purpose
	hxGuard       = 10 * time.Second   // failure-only guard, never a behavior heuristic
)

// ---- fake pinner (pure ownership model; no runtime.Pinner) ----

type hxPinner struct {
	set    *hxPins
	pinned []uintptr
	unpins int
}

func (p *hxPinner) Pin(x any) windowsSocketPinResult {
	p.set.mu.Lock()
	defer p.set.mu.Unlock()
	p.set.pinCalls++
	if p.set.failAt != 0 && p.set.pinCalls == p.set.failAt {
		if p.set.failPanic {
			return windowsSocketPinResult{Panic: "fake pin panic", Panicked: true}
		}
		return windowsSocketPinResult{Err: errors.New("fake pin failure")}
	}
	p.pinned = append(p.pinned, reflect.ValueOf(x).Pointer())
	return windowsSocketPinResult{}
}

func (p *hxPinner) Unpin() {
	p.set.mu.Lock()
	defer p.set.mu.Unlock()
	p.unpins++
	p.set.unpinCalls++
}

type hxPins struct {
	mu         sync.Mutex
	failAt     int  // 1-based Pin call that fails; 0 = never
	failPanic  bool // the failing Pin reports a recovered panic value instead of an error
	pinCalls   int
	unpinCalls int
	made       []*hxPinner
}

func (s *hxPins) New() windowsSocketPinner {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := &hxPinner{set: s}
	s.made = append(s.made, p)
	return p
}

func (s *hxPins) unpins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unpinCalls
}

// ---- fake native API (per instance, injected) ----

type hxWait struct {
	status uint32
	err    error
}

type hxNative struct {
	mu                              sync.Mutex
	pins                            *hxPins
	resolveErr, createErr, sendErr  error
	closeErr, cancelErr             error
	result                          windowsSocketNativeResult
	waitCh                          chan hxWait
	resolveName                     string
	next                            windowsSocketHandle
	resolves, creates, sends, waits int
	polls, results, cancels, closes int
	newPosts                        int
	postTagged                      []uintptr
	postPartLens                    [][]uint32
	sendBufLens                     [][]uint32
	sendN                           []int
	badNewPost                      func(tagged uintptr, parts [][]byte) (any, []any)
	madePosts                       map[*hxPost]bool
	sentPost                        *hxPost
	badPost                         []string // post-identity violations at Send/Result/CancelIoEx
	sendOVTagged                    bool
	cancelUnpinsAtCall              []int
	closedHandles                   []windowsSocketHandle
	cancelHook                      func()
}

// hxPost is the fake opaque post object. The two marker bytes stand in for the
// OVERLAPPED and WSABUF[0] pin targets; the stable pointer identity of the post
// itself is what Send/Result/CancelIoEx receive.
type hxPost struct {
	tagged   uintptr
	parts    [][]byte // the "WSABUF array": lengths are derived from this at Send, not at NewPost
	ovMark   byte
	bufsMark byte
}

// checkPost records a violation unless post is exactly a post this fake made
// (and, after Send, exactly the one Send received). Caller holds f.mu.
func (f *hxNative) checkPost(op string, post any) *hxPost {
	p, ok := post.(*hxPost)
	switch {
	case !ok || p == nil:
		f.badPost = append(f.badPost, op+": not an *hxPost or nil")
		return nil
	case !f.madePosts[p]:
		f.badPost = append(f.badPost, op+": post not created by NewPost")
	case op != "Send" && f.sentPost != nil && p != f.sentPost:
		f.badPost = append(f.badPost, op+": post differs from the one Send received")
	}
	return p
}

func (f *hxNative) Resolve() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolves++
	return f.resolveName, f.resolveErr
}

func (f *hxNative) CreateEvent() (windowsSocketHandle, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return 0, false, f.createErr
	}
	f.creates++
	f.next += 4
	return 0x1000 + f.next, true, nil
}

func (f *hxNative) NewPost(tagged uintptr, parts [][]byte) (any, []any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.newPosts++
	if f.badNewPost != nil {
		return f.badNewPost(tagged, parts)
	}
	f.postTagged = append(f.postTagged, tagged)
	lens := make([]uint32, 0, len(parts))
	for _, p := range parts {
		lens = append(lens, uint32(len(p)))
	}
	f.postPartLens = append(f.postPartLens, lens)
	post := &hxPost{tagged: tagged, parts: append([][]byte(nil), parts...)}
	if f.madePosts == nil {
		f.madePosts = map[*hxPost]bool{}
	}
	f.madePosts[post] = true
	return post, []any{&post.ovMark, &post.bufsMark}
}

func (f *hxNative) Send(h windowsSocketHandle, post any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends++
	if p := f.checkPost("Send", post); p != nil {
		// Lengths are read from the post's own state at Send time.
		lens := make([]uint32, 0, len(p.parts))
		for _, b := range p.parts {
			lens = append(lens, uint32(len(b)))
		}
		f.sendBufLens = append(f.sendBufLens, lens)
		f.sendN = append(f.sendN, len(p.parts))
		f.sendOVTagged = p.tagged&1 == 1
		f.sentPost = p
	}
	return f.sendErr
}

func (f *hxNative) WaitEvent(ev windowsSocketHandle) (uint32, error) {
	f.mu.Lock()
	f.waits++
	ch := f.waitCh
	f.mu.Unlock()
	r := <-ch
	return r.status, r.err
}

func (f *hxNative) PollEvent(ev windowsSocketHandle) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	return hxWaitTimeout, nil
}

func (f *hxNative) Result(h windowsSocketHandle, post any) windowsSocketNativeResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results++
	f.checkPost("Result", post)
	return f.result
}

func (f *hxNative) CancelIoEx(h windowsSocketHandle, post any) error {
	f.mu.Lock()
	f.cancels++
	f.checkPost("CancelIoEx", post)
	hook := f.cancelHook
	f.cancelUnpinsAtCall = append(f.cancelUnpinsAtCall, f.pins.unpins())
	err := f.cancelErr
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return err
}

func (f *hxNative) CloseEvent(ev windowsSocketHandle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	f.closedHandles = append(f.closedHandles, ev)
	return f.closeErr
}

// ---- fake RawConn ----

type hxRaw struct {
	mode        string // "run" | "refuse" | "nocb" | "errafter"
	refuseErr   error
	outerErr    error
	mu          sync.Mutex
	writeCalls  int
	cbRuns      int
	cbRets      []bool
	controls    int
	controlHook func(n int, f func(uintptr)) error
}

func (r *hxRaw) Read(func(uintptr) bool) error { return errors.New("hxRaw: Read unused") }

func (r *hxRaw) Control(f func(uintptr)) error {
	r.mu.Lock()
	r.controls++
	n, hook := r.controls, r.controlHook
	r.mu.Unlock()
	if hook != nil {
		return hook(n, f)
	}
	f(0x7001)
	return nil
}

func (r *hxRaw) Write(f func(uintptr) bool) error {
	r.mu.Lock()
	r.writeCalls++
	mode := r.mode
	r.mu.Unlock()
	switch mode {
	case "refuse":
		return r.refuseErr
	case "nocb":
		return nil
	}
	ret := f(0x7001)
	r.mu.Lock()
	r.cbRuns++
	r.cbRets = append(r.cbRets, ret)
	r.mu.Unlock()
	if mode == "errafter" {
		return r.outerErr
	}
	return nil
}

// ---- rig ----

type hxRig struct {
	api  *hxNative
	pins *hxPins
	rt   *windowsSocketWriteRuntime
	hook atomic.Pointer[func(*windowsSocketWrite, string)]
}

func newHxRig() *hxRig {
	r := &hxRig{pins: &hxPins{}}
	r.api = &hxNative{pins: r.pins, waitCh: make(chan hxWait, 8)}
	r.rt = newWindowsSocketWriteRuntime(windowsSocketWriteDeps{
		API: r.api, NewPinner: r.pins.New,
		Observe: func(op *windowsSocketWrite, step string) {
			if h := r.hook.Load(); h != nil {
				(*h)(op, step)
			}
		},
	})
	return r
}

func (r *hxRig) setHook(f func(*windowsSocketWrite, string)) { r.hook.Store(&f) }

func (r *hxRig) pending() { r.api.sendErr = hxIOPending }

func hxOK(t *testing.T, mode string) *hxRaw { t.Helper(); return &hxRaw{mode: "run"} }

func hxClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func hxAwait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(hxGuard):
		t.Fatalf("guard timeout waiting for %s", what)
	}
}

func hxParts(sizes ...int) [][]byte {
	out := make([][]byte, len(sizes))
	for i, n := range sizes {
		out[i] = bytes.Repeat([]byte{byte(i + 1)}, n)
	}
	return out
}

func hxTotal(p [][]byte) uint64 {
	var n uint64
	for _, b := range p {
		n += uint64(len(b))
	}
	return n
}

func hxCounters(t *testing.T, rig *hxRig, want windowsSocketCounters) {
	t.Helper()
	rig.api.mu.Lock()
	bad := append([]string(nil), rig.api.badPost...)
	rig.api.mu.Unlock()
	if len(bad) != 0 {
		t.Fatalf("post identity violations: %v", bad)
	}
	if got := rig.rt.Counters(); got != want {
		t.Fatalf("counters %+v want %+v", got, want)
	}
}

func hxOrdered(t *testing.T, s windowsSocketWriteSnapshot, fence bool) {
	t.Helper()
	if fence {
		if !(0 < s.FenceSeq && s.FenceSeq < s.UnpinSeq && s.UnpinSeq < s.EventCloseSeq && s.EventCloseSeq < s.DoneSeq) {
			t.Fatalf("order fence=%d unpin=%d close=%d done=%d", s.FenceSeq, s.UnpinSeq, s.EventCloseSeq, s.DoneSeq)
		}
		return
	}
	if !(s.FenceSeq == 0 && 0 < s.UnpinSeq && s.UnpinSeq < s.EventCloseSeq && s.EventCloseSeq < s.DoneSeq) {
		t.Fatalf("no-IO order fence=%d unpin=%d close=%d done=%d", s.FenceSeq, s.UnpinSeq, s.EventCloseSeq, s.DoneSeq)
	}
}

func hxGC() {
	for i := 0; i < 8; i++ {
		runtime.GC()
		_ = make([]byte, 1<<20)
	}
}

// ---- T1: unfenceable wait => quarantine, no release, no exit ----

func TestWindowsHelperT1FenceFailureQuarantines(t *testing.T) {
	rig := newHxRig()
	rig.pending()
	exited := make(chan struct{})
	rig.setHook(func(_ *windowsSocketWrite, step string) {
		if step == "waiter_exit" {
			close(exited)
		}
	})
	op, err := rig.rt.Submit(hxOK(t, ""), hxParts(10, 20))
	if err != nil || op == nil {
		t.Fatalf("submit: op=%v err=%v", op != nil, err)
	}
	rig.api.waitCh <- hxWait{status: hxWaitFailed, err: syscall.Errno(6)}
	hxAwait(t, op.FenceFailed(), "FenceFailed")
	hxAwait(t, exited, "waiter exit")
	if hxClosed(op.Done()) {
		t.Fatal("Done closed together with FenceFailed (must be exclusive)")
	}
	s := op.Snapshot()
	if s.Unpinned || s.EventClosed || s.UnpinSeq != 0 || s.EventCloseSeq != 0 || s.DoneSeq != 0 || len(s.Faults) == 0 {
		t.Fatalf("quarantined snapshot released or fault-free: %+v", s)
	}
	if s.Post != windowsSocketPostNative997 {
		t.Fatalf("post classification lost: %q", s.Post)
	}
	if rig.pins.unpins() != 0 || rig.api.closes != 0 || rig.rt.LiveCount() != 1 {
		t.Fatalf("quarantine violated: unpins=%d closes=%d live=%d", rig.pins.unpins(), rig.api.closes, rig.rt.LiveCount())
	}
	hxCounters(t, rig, windowsSocketCounters{EventsCreated: 1, OpsPosted: 1})
	// Cancel and Snapshot stay valid after FenceFailed; the lease is released.
	if got := op.Cancel(); got != "cancel_issued" {
		t.Fatalf("Cancel after FenceFailed = %q", got)
	}
	if got := op.Cancel(); got != "cancel_issued" {
		t.Fatalf("second Cancel after FenceFailed = %q (lease leaked?)", got)
	}
	if hxClosed(op.Done()) || rig.pins.unpins() != 0 {
		t.Fatal("Cancel after FenceFailed released or completed the op")
	}
	// No cleanup: ops, handles and pins are synthetic and owned by this per-instance
	// runtime. The production registry is never erased by a test (UNIT-CONTRACT.md).
}

// ---- T2: pending op survives caller drop + GC (SMOKE / liveness corroboration only) ----
// While pending, the blocked waiter goroutine's stack/closure roots the op and parts
// even if the helper had NO registry, so weak-pointer survival here does not prove
// registry retention. Registry sensitivity is tested directly via LiveCount/LiveOps in
// T2b and T3 (waiter exited or never started).

//go:noinline
func hxSubmitDropped(rig *hxRig) (weak.Pointer[windowsSocketWrite], weak.Pointer[byte], [32]byte) {
	data := bytes.Repeat([]byte{0x5a}, 1<<20)
	sum := sha256.Sum256(data)
	op, err := rig.rt.Submit(&hxRaw{mode: "run"}, [][]byte{data[:512<<10], data[512<<10:]})
	if err != nil || op == nil {
		panic("hxSubmitDropped: submit failed")
	}
	return weak.Make(op), weak.Make(&data[0]), sum
}

func TestWindowsHelperT2PendingOpSurvivesCallerDropGCSmoke(t *testing.T) {
	rig := newHxRig()
	rig.pending()
	var done <-chan struct{}
	rig.setHook(func(op *windowsSocketWrite, step string) {
		if step == "registered" {
			done = op.Done() // the channel does not reference the op
		}
	})
	wop, wdata, _ := hxSubmitDropped(rig)
	hxGC()
	if wop.Value() == nil || wdata.Value() == nil {
		t.Fatalf("pending op or payload collected after caller dropped it (op=%v data=%v)", wop.Value() != nil, wdata.Value() != nil)
	}
	if rig.rt.LiveCount() != 1 || rig.pins.unpins() != 0 {
		t.Fatalf("live=%d unpins=%d while pending", rig.rt.LiveCount(), rig.pins.unpins())
	}
	rig.api.waitCh <- hxWait{status: hxWaitObject0}
	hxAwait(t, done, "Done after fence")
	// NOTE (OPEN-4): "registry empty once Done is closed" is implied by fix1 ("removal
	// only after fence and release done"; Done follows release) but the exact removal
	// instant relative to close(done) is not separately specified; assert only after Done.
	if rig.rt.LiveCount() != 0 {
		t.Fatalf("registry not emptied after Done: live=%d", rig.rt.LiveCount())
	}
	hxCounters(t, rig, windowsSocketCounters{1, 1, 1, 1})
}

func TestWindowsHelperT2bQuarantinedOpSurvivesGC(t *testing.T) {
	rig := newHxRig()
	rig.pending()
	var ff <-chan struct{}
	rig.setHook(func(op *windowsSocketWrite, step string) {
		if step == "registered" {
			ff = op.FenceFailed()
		}
	})
	wop, wdata, _ := hxSubmitDropped(rig)
	rig.api.waitCh <- hxWait{status: hxWaitTimeout}
	hxAwait(t, ff, "FenceFailed")
	hxGC()
	if wop.Value() == nil || wdata.Value() == nil || rig.rt.LiveCount() != 1 || rig.pins.unpins() != 0 {
		t.Fatalf("quarantined op not retained: op=%v data=%v live=%d unpins=%d", wop.Value() != nil, wdata.Value() != nil, rig.rt.LiveCount(), rig.pins.unpins())
	}
	// The waiter has exited after the failure, so the registry is the discriminating
	// owner: the registry view must hold the very op the caller dropped.
	lo := rig.rt.LiveOps()
	if len(lo) != 1 || weak.Make(lo[0]) != wop {
		t.Fatalf("registry view does not hold the dropped quarantined op (len=%d)", len(lo))
	}
	// Fake pinner/handles are synthetic: nothing real is left unreleased and no
	// explicit synthetic-fence recovery exists (none is designed; see OPEN-1).
}

// ---- T3: panic between MayOwn and waiter launch retains, never releases ----

func TestWindowsHelperT3PanicAfterPostQuarantines(t *testing.T) {
	rig := newHxRig()
	rig.pending()
	rig.setHook(func(_ *windowsSocketWrite, step string) {
		if step == "post_send_returned" {
			panic("injected between post and waiter launch")
		}
	})
	var rec any
	func() {
		defer func() { rec = recover() }()
		rig.rt.Submit(hxOK(t, ""), hxParts(8))
	}()
	if rec == nil {
		t.Fatal("helper swallowed the panic (must re-panic after quarantining)")
	}
	ops := rig.rt.LiveOps()
	if len(ops) != 1 {
		t.Fatalf("live ops after panic = %d want 1", len(ops))
	}
	op := ops[0]
	hxAwait(t, op.FenceFailed(), "FenceFailed after panic")
	s := op.Snapshot()
	if hxClosed(op.Done()) || s.Unpinned || s.EventClosed || len(s.Faults) == 0 || s.Post != windowsSocketPostNative997 {
		t.Fatalf("panic path released or lost classification: %+v", s)
	}
	if rig.pins.unpins() != 0 || rig.api.closes != 0 || rig.api.waits != 0 {
		t.Fatalf("unpins=%d closes=%d waits=%d", rig.pins.unpins(), rig.api.closes, rig.api.waits)
	}
	hxCounters(t, rig, windowsSocketCounters{EventsCreated: 1, OpsPosted: 1})
}

// ---- T4: unexpected wait statuses are fence failures ----

func TestWindowsHelperT4UnexpectedWaitStatus(t *testing.T) {
	for _, st := range []uint32{hxWaitTimeout, 0x80, hxWaitFailed, 1} {
		rig := newHxRig()
		rig.pending()
		op, err := rig.rt.Submit(hxOK(t, ""), hxParts(4))
		if err != nil {
			t.Fatal(err)
		}
		rig.api.waitCh <- hxWait{status: st}
		hxAwait(t, op.FenceFailed(), "FenceFailed")
		s := op.Snapshot()
		if hxClosed(op.Done()) || s.Unpinned || s.EventClosed || len(s.Faults) == 0 || rig.pins.unpins() != 0 || rig.api.results != 0 {
			t.Fatalf("status %#x: not quarantined: %+v results=%d", st, s, rig.api.results)
		}
	}
}

// ---- T5: WSASend non-997 error => exact no-IO rollback ----

func TestWindowsHelperT5NativeErrorNoIO(t *testing.T) {
	rig := newHxRig()
	rig.api.sendErr = syscall.Errno(10054)
	parts := hxParts(100, 200, 300)
	op, err := rig.rt.Submit(hxOK(t, ""), parts)
	var en syscall.Errno
	if op == nil || !errors.As(err, &en) || en != 10054 {
		t.Fatalf("op=%v err=%v want op and errno 10054", op != nil, err)
	}
	s := op.Snapshot()
	if s.Post != windowsSocketPostError || s.PostErrno != 10054 || s.WSASendCalls != 1 || s.Result.Source != windowsSocketResultNone {
		t.Fatalf("snapshot %+v", s)
	}
	if !hxClosed(op.Done()) || hxClosed(op.FenceFailed()) || !s.Unpinned || !s.EventClosed {
		t.Fatalf("native_error not released on return: %+v", s)
	}
	hxOrdered(t, s, false)
	if rig.api.waits != 0 || rig.api.results != 0 || rig.api.sends != 1 || rig.rt.LiveCount() != 0 {
		t.Fatalf("waits=%d results=%d sends=%d live=%d", rig.api.waits, rig.api.results, rig.api.sends, rig.rt.LiveCount())
	}
	hxCounters(t, rig, windowsSocketCounters{1, 1, 1, 1})
}

// ---- T6 + T7: short native0/native997 count, no repost, helper-attested order ----

type hxStepRec struct {
	present    bool
	doneClosed bool
	s          windowsSocketWriteSnapshot
}

func TestWindowsHelperT6T7ShortNative0NoRepostOrder(t *testing.T) {
	for _, pend := range []bool{false, true} {
		rig := newHxRig()
		parts := hxParts(1000, 3000)
		total := hxTotal(parts)
		rig.api.result = windowsSocketNativeResult{OK: true, N: 1234}
		if pend {
			rig.pending()
		}
		recs := map[string]hxStepRec{}
		var mu sync.Mutex
		rig.setHook(func(op *windowsSocketWrite, step string) {
			switch step {
			case "pre_query", "pre_unpin", "pre_close_event", "pre_done":
				rec := hxStepRec{present: true, doneClosed: hxClosed(op.Done()), s: op.Snapshot()}
				mu.Lock()
				recs[step] = rec
				mu.Unlock()
			}
		})
		op, err := rig.rt.Submit(hxOK(t, ""), parts)
		if err != nil || op == nil {
			t.Fatalf("pend=%v submit: %v", pend, err)
		}
		if pend {
			rig.api.waitCh <- hxWait{status: hxWaitObject0}
		}
		hxAwait(t, op.Done(), "Done")
		s := op.Snapshot()
		want := windowsSocketPostNative0
		src := windowsSocketResultPostQuery
		if pend {
			want, src = windowsSocketPostNative997, windowsSocketResultFenceQuery
		}
		if s.Post != want || s.Result.Source != src || !s.Result.OK || s.Result.N != 1234 || uint64(s.Result.N) >= total {
			t.Fatalf("pend=%v result %+v", pend, s)
		}
		if s.WSASendCalls != 1 || rig.api.sends != 1 || len(rig.api.sendBufLens) != 1 || !rig.api.sendOVTagged {
			t.Fatalf("pend=%v repost/tag: sends=%d lens=%v tagged=%v", pend, rig.api.sends, rig.api.sendBufLens, rig.api.sendOVTagged)
		}
		if s.Parts != 2 || s.Bytes != total || s.PinCalls != 4 {
			t.Fatalf("accounting %+v", s)
		}
		hxOrdered(t, s, true)
		mu.Lock()
		for _, step := range []string{"pre_query", "pre_unpin", "pre_close_event", "pre_done"} {
			r := recs[step]
			if !r.present {
				t.Fatalf("pend=%v: observer step %s never fired", pend, step)
			}
			if r.doneClosed {
				t.Fatalf("pend=%v: Done already closed at %s", pend, step)
			}
		}
		if q := recs["pre_query"].s; q.FenceSeq == 0 || q.UnpinSeq != 0 || q.Unpinned || q.EventClosed {
			t.Fatalf("pend=%v: fence must be stamped and nothing released before the query: %+v", pend, q)
		}
		if u := recs["pre_unpin"].s; u.Result.Source == windowsSocketResultNone || u.Unpinned || u.EventClosed {
			t.Fatalf("pend=%v: result must be stored before unpin: %+v", pend, u)
		}
		if c := recs["pre_close_event"].s; !c.Unpinned || c.UnpinSeq == 0 || c.EventClosed {
			t.Fatalf("pend=%v: unpin must precede event close: %+v", pend, c)
		}
		if d := recs["pre_done"].s; !d.Unpinned || !d.EventClosed || d.EventCloseSeq == 0 || d.DoneSeq != 0 {
			t.Fatalf("pend=%v: event close must precede Done and DoneSeq unset at pre_done: %+v", pend, d)
		}
		mu.Unlock()
		if !pend && !hxClosed(op.Done()) {
			t.Fatal("native0 Done must be closed on return")
		}
		hxCounters(t, rig, windowsSocketCounters{1, 1, 1, 1})
	}
}

// ---- T8: Cancel inside Control holds a lease; release waits for it ----

func TestWindowsHelperT8CancelLeaseBlocksUnpin(t *testing.T) {
	rig := newHxRig()
	rig.pending()
	raw := hxOK(t, "")
	inControl := make(chan struct{})
	releaseControl := make(chan struct{})
	raw.controlHook = func(n int, f func(uintptr)) error {
		if n == 1 { // the explicit Cancel; the waiter's fence query is call 2
			close(inControl)
			<-releaseControl
		}
		f(0x7001)
		return nil
	}
	drain := make(chan struct{})
	var lateCancel windowsSocketCancel
	var cancelsBefore, cancelsAfter int
	var drainSnap windowsSocketWriteSnapshot
	drainUnpins := -1
	rig.setHook(func(op *windowsSocketWrite, step string) {
		switch step {
		case "release_begin":
			rig.api.mu.Lock()
			cancelsBefore = rig.api.cancels
			rig.api.mu.Unlock()
			lateCancel = op.Cancel() // new leases are excluded once releasing begins
			rig.api.mu.Lock()
			cancelsAfter = rig.api.cancels
			rig.api.mu.Unlock()
		case "lease_drain_wait":
			// Captured HERE, before the in-flight Cancel is unblocked: after main sees
			// the drain, release may legitimately finish.
			drainUnpins = rig.pins.unpins()
			drainSnap = op.Snapshot()
			close(drain)
			releaseControl <- struct{}{} // allow the in-flight Cancel to finish
		}
	})
	op, err := rig.rt.Submit(raw, hxParts(64))
	if err != nil {
		t.Fatal(err)
	}
	res := make(chan windowsSocketCancel, 1)
	go func() { res <- op.Cancel() }()
	hxAwait(t, inControl, "Cancel inside Control")
	rig.api.waitCh <- hxWait{status: hxWaitObject0}
	hxAwait(t, drain, "release waiting for lease drain")
	if drainUnpins != 0 || drainSnap.Unpinned || drainSnap.EventClosed || drainSnap.UnpinSeq != 0 {
		t.Fatalf("released while Cancel held a lease: unpins=%d snap=%+v", drainUnpins, drainSnap)
	}
	select {
	case got := <-res:
		if got != "cancel_issued" {
			t.Fatalf("in-flight Cancel = %q", got)
		}
	case <-time.After(hxGuard):
		t.Fatal("in-flight Cancel did not finish")
	}
	hxAwait(t, op.Done(), "Done")
	if lateCancel != "already_done" || cancelsAfter != cancelsBefore {
		t.Fatalf("Cancel after releasing began = %q (native cancels %d->%d), want already_done with no native use", lateCancel, cancelsBefore, cancelsAfter)
	}
	for i, u := range rig.api.cancelUnpinsAtCall {
		if u != 0 {
			t.Fatalf("CancelIoEx call %d ran after Unpin", i)
		}
	}
	hxCounters(t, rig, windowsSocketCounters{1, 1, 1, 1})
}

// ---- T9: concurrent Cancel x N racing release ----

func TestWindowsHelperT9ConcurrentCancelRelease(t *testing.T) {
	rig := newHxRig()
	// This case tests lease/release concurrency, not an invalid native query.
	rig.api.result = windowsSocketNativeResult{OK: true, N: 64}
	rig.pending()
	op, err := rig.rt.Submit(hxOK(t, ""), hxParts(64))
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	start := make(chan struct{})
	out := make(chan windowsSocketCancel, n)
	for i := 0; i < n; i++ {
		go func() { <-start; out <- op.Cancel() }()
	}
	close(start)
	rig.api.waitCh <- hxWait{status: hxWaitObject0}
	for i := 0; i < n; i++ {
		select {
		case c := <-out:
			switch c {
			case "cancel_issued", "not_found_completed_race", "already_done":
			default:
				t.Fatalf("Cancel returned fault state %q", c)
			}
		case <-time.After(hxGuard):
			t.Fatal("Cancel did not return")
		}
	}
	hxAwait(t, op.Done(), "Done")
	for i, u := range rig.api.cancelUnpinsAtCall {
		if u != 0 {
			t.Fatalf("CancelIoEx call %d used OVERLAPPED after Unpin", i)
		}
	}
	if got := op.Cancel(); got != "already_done" {
		t.Fatalf("Cancel after Done = %q", got)
	}
	s := op.Snapshot()
	if len(s.Faults) != 0 || rig.pins.unpins() != 1 || rig.api.closes != 1 {
		t.Fatalf("faults=%v unpins=%d closes=%d (release exactly once)", s.Faults, rig.pins.unpins(), rig.api.closes)
	}
	hxCounters(t, rig, windowsSocketCounters{1, 1, 1, 1})
}

// ---- T10: outer RawConn error after the callback ran ----

func TestWindowsHelperT10OuterErrorAfterCallback(t *testing.T) {
	outer := errors.New("outer raw-write error")
	for _, tc := range []struct {
		name    string
		sendErr error
		post    windowsSocketPost
	}{
		{"native997", hxIOPending, windowsSocketPostNative997},
		{"native0", nil, windowsSocketPostNative0},
	} {
		rig := newHxRig()
		rig.api.sendErr = tc.sendErr
		rig.api.result = windowsSocketNativeResult{OK: true, N: 16}
		op, err := rig.rt.Submit(&hxRaw{mode: "errafter", outerErr: outer}, hxParts(16))
		if op == nil || !errors.Is(err, outer) {
			t.Fatalf("%s: op=%v err=%v want op and wrapped outer error", tc.name, op != nil, err)
		}
		s := op.Snapshot()
		if s.Post != tc.post || len(s.Faults) == 0 {
			t.Fatalf("%s: classification or fault lost: %+v", tc.name, s)
		}
		if tc.post == windowsSocketPostNative997 {
			if s.PostErrno != hxIOPending || hxClosed(op.Done()) || hxClosed(op.FenceFailed()) || s.Unpinned || rig.pins.unpins() != 0 || rig.rt.LiveCount() != 1 {
				t.Fatalf("%s: pending op released because of outer error: %+v", tc.name, s)
			}
			rig.api.waitCh <- hxWait{status: hxWaitObject0}
			hxAwait(t, op.Done(), "Done")
			if s2 := op.Snapshot(); s2.Result.Source != windowsSocketResultFenceQuery || !s2.Result.OK {
				t.Fatalf("waiter did not run to a normal fence: %+v", s2)
			}
		} else if !hxClosed(op.Done()) || !s.Unpinned {
			t.Fatalf("%s: native0 must be released and Done on return: %+v", tc.name, s)
		}
		hxCounters(t, rig, windowsSocketCounters{1, 1, 1, 1})
	}
}

// ---- T11: callback refusal / not run ----

func TestWindowsHelperT11RefusedAndNilCallback(t *testing.T) {
	// Closed refusal: nil op, ORIGINAL error value preserved, zero counters.
	rig := newHxRig()
	refuse := &net.OpError{Op: "raw-write", Err: net.ErrClosed}
	raw := &hxRaw{mode: "refuse", refuseErr: refuse}
	op, err := rig.rt.Submit(raw, hxParts(8))
	if op != nil || err != error(refuse) || !errors.Is(err, net.ErrClosed) {
		t.Fatalf("refusal: op=%v err=%v (want nil op and the identical raw-write error)", op != nil, err)
	}
	hxCounters(t, rig, windowsSocketCounters{})
	if rig.api.creates != 0 || rig.api.sends != 0 || rig.pins.pinCalls != 0 || rig.rt.LiveCount() != 0 {
		t.Fatalf("refusal allocated: creates=%d sends=%d pins=%d", rig.api.creates, rig.api.sends, rig.pins.pinCalls)
	}
	// Callback not run and nil error: internal, non-ErrClosed.
	rig = newHxRig()
	op, err = rig.rt.Submit(&hxRaw{mode: "nocb"}, hxParts(8))
	if op != nil || err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, errWindowsSocketWriteDomain) {
		t.Fatalf("nil callback: op=%v err=%v", op != nil, err)
	}
	hxCounters(t, rig, windowsSocketCounters{})
	if rig.api.creates != 0 || rig.rt.LiveCount() != 0 {
		t.Fatal("nil-callback path created resources")
	}
}

// ---- T12: uint64 domain validator without allocating 4GiB ----

func TestWindowsHelperT12DomainValidatorUint64(t *testing.T) {
	const m = uint64(math.MaxUint32)
	bad := map[string][]uint64{
		"nil":            nil,
		"empty":          {},
		"zero part":      {1, 0},
		"single over":    {m + 1},
		"sum over":       {m/2 + 1, m/2 + 1},
		"sum one over":   {m, 1},
		"uint64 wrap":    {1 << 63, 1 << 63},
		"max64 plus one": {math.MaxUint64, 2},
		"huge single":    {1 << 40},
	}
	for name, lens := range bad {
		if _, err := windowsSocketWriteDomain(lens); !errors.Is(err, errWindowsSocketWriteDomain) || errors.Is(err, net.ErrClosed) {
			t.Fatalf("%s: err=%v want errWindowsSocketWriteDomain", name, err)
		}
	}
	good := map[string][]uint64{
		"one":        {1},
		"max single": {m},
		"max split":  {m - 5, 3, 2},
	}
	for name, lens := range good {
		got, err := windowsSocketWriteDomain(lens)
		var want uint64
		for _, l := range lens {
			want += l
		}
		if err != nil || got != want {
			t.Fatalf("%s: total=%d err=%v want %d", name, got, err, want)
		}
	}
	// End to end: rejected before ANY resolve, RawConn, or resource work.
	rig := newHxRig()
	rig.api.resolveErr = errors.New("must not be reached")
	raw := hxOK(t, "")
	for _, p := range [][][]byte{nil, {}, {nil}, {{}}, {[]byte{1}, {}}} {
		op, err := rig.rt.Submit(raw, p)
		if op != nil || !errors.Is(err, errWindowsSocketWriteDomain) {
			t.Fatalf("parts %v: op=%v err=%v", p, op != nil, err)
		}
	}
	if rig.api.resolves != 0 || raw.writeCalls != 0 || rig.api.creates != 0 {
		t.Fatalf("domain rejection reached resolve=%d write=%d create=%d", rig.api.resolves, raw.writeCalls, rig.api.creates)
	}
	hxCounters(t, rig, windowsSocketCounters{})
}

// ---- T13: pre-resource init failure ----

func TestWindowsHelperT13ResolveFailureZeroCounters(t *testing.T) {
	rig := newHxRig()
	rig.api.resolveErr = errors.New("proc resolution failed")
	raw := hxOK(t, "")
	op, err := rig.rt.Submit(raw, hxParts(8))
	if op != nil || err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, errWindowsSocketWriteDomain) {
		t.Fatalf("op=%v err=%v", op != nil, err)
	}
	if raw.writeCalls != 0 || rig.api.creates != 0 || rig.pins.pinCalls != 0 || rig.rt.LiveCount() != 0 {
		t.Fatal("pre-resource failure touched RawConn or created resources")
	}
	hxCounters(t, rig, windowsSocketCounters{})
}

// ---- T14: helper copies the outer descriptor slice (mutated inside post_send_returned) ----

func TestWindowsHelperT14OuterSliceCopied(t *testing.T) {
	rig := newHxRig()
	rig.pending()
	a := bytes.Repeat([]byte{0xa1}, 4096)
	b := bytes.Repeat([]byte{0xb2}, 8192)
	sum := sha256.Sum256(append(append([]byte{}, a...), b...))
	pa, pb := reflect.ValueOf(a).Pointer(), reflect.ValueOf(b).Pointer()
	outer := [][]byte{a, b}
	var mid windowsSocketWriteSnapshot
	var midLive int
	rig.setHook(func(op *windowsSocketWrite, step string) {
		if step == "post_send_returned" { // before the waiter is launched
			outer[0], outer[1] = nil, []byte("mutated")
			outer = outer[:1]
			mid = op.Snapshot()
			midLive = rig.rt.LiveCount()
		}
	})
	op, err := rig.rt.Submit(hxOK(t, ""), outer)
	if err != nil || op == nil {
		t.Fatalf("submit: op=%v err=%v", op != nil, err)
	}
	// Helper-owned descriptor state, observed through the helper/fake API, not GC:
	if mid.Parts != 2 || mid.Bytes != 4096+8192 || midLive != 1 {
		t.Fatalf("helper descriptor changed by outer mutation: %+v live=%d", mid, midLive)
	}
	rig.api.waitCh <- hxWait{status: hxWaitObject0}
	hxAwait(t, op.Done(), "Done")
	s := op.Snapshot()
	if s.Parts != 2 || s.Bytes != 4096+8192 {
		t.Fatalf("snapshot after completion: %+v", s)
	}
	if got := rig.api.sendBufLens; len(got) != 1 || len(got[0]) != 2 || got[0][0] != 4096 || got[0][1] != 8192 {
		t.Fatalf("WSABUF lengths %v", got)
	}
	rig.pins.mu.Lock()
	defer rig.pins.mu.Unlock()
	if len(rig.pins.made) != 1 || rig.pins.made[0].unpins != 1 || len(rig.pins.made[0].pinned) != 4 {
		t.Fatalf("pin accounting wrong: made=%d", len(rig.pins.made))
	}
	// Pins taken for the ORIGINAL part backers (before the mutation), not the mutated ones.
	got := rig.pins.made[0].pinned[2:]
	if got[0] != pa || got[1] != pb {
		t.Fatalf("pinned part backers %v want original %v", got, []uintptr{pa, pb})
	}
	if sha256.Sum256(append(append([]byte{}, a...), b...)) != sum {
		t.Fatal("payload changed")
	}
}

// Payload bytes are neither copied nor modified by the helper.
func TestWindowsHelperPayloadUnmodified(t *testing.T) {
	rig := newHxRig()
	parts := hxParts(100, 200)
	rig.api.result = windowsSocketNativeResult{OK: true, N: 300}
	before := sha256.Sum256(bytes.Join(parts, nil))
	op, err := rig.rt.Submit(hxOK(t, ""), parts)
	if err != nil || op == nil {
		t.Fatal(err)
	}
	if after := sha256.Sum256(bytes.Join(parts, nil)); after != before {
		t.Fatal("helper modified payload bytes")
	}
}

// ---- partial setup failure: exact truthful rollback ----

func TestWindowsHelperPartialSetupRollback(t *testing.T) {
	t.Run("CreateEvent fails: nothing created", func(t *testing.T) {
		rig := newHxRig()
		rig.api.createErr = errors.New("CreateEventW failed")
		op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
		if op != nil || err == nil {
			t.Fatalf("op=%v err=%v want nil op and error", op != nil, err)
		}
		hxCounters(t, rig, windowsSocketCounters{})
	})
	// Pin call k fails (1-based). Pin calls: OVERLAPPED, WSABUF array, each part.
	for _, tc := range []struct {
		failAt     int
		wantUnpins int64
	}{{1, 0}, {2, 1}, {3, 1}, {4, 1}} {
		rig := newHxRig()
		rig.pins.failAt = tc.failAt
		raw := &hxRaw{mode: "run"}
		op, err := rig.rt.Submit(raw, hxParts(8, 8))
		if op == nil || err == nil {
			t.Fatalf("failAt %d: op=%v err=%v want diagnostic op and error", tc.failAt, op != nil, err)
		}
		s := op.Snapshot()
		if s.Post != windowsSocketPostNotPosted || s.WSASendCalls != 0 || len(s.Faults) == 0 || !hxClosed(op.Done()) || hxClosed(op.FenceFailed()) || !s.EventClosed {
			t.Fatalf("failAt %d: snapshot %+v", tc.failAt, s)
		}
		if s.Unpinned != (tc.wantUnpins == 1) {
			t.Fatalf("failAt %d: Unpinned=%v want %v", tc.failAt, s.Unpinned, tc.wantUnpins == 1)
		}
		if rig.api.sends != 0 || rig.api.waits != 0 || rig.rt.LiveCount() != 0 {
			t.Fatalf("failAt %d: sends=%d waits=%d live=%d", tc.failAt, rig.api.sends, rig.api.waits, rig.rt.LiveCount())
		}
		if len(raw.cbRets) != 1 || !raw.cbRets[0] || raw.writeCalls != 1 {
			t.Fatalf("failAt %d: callback must run once and return true: %v writes=%d", tc.failAt, raw.cbRets, raw.writeCalls)
		}
		hxCounters(t, rig, windowsSocketCounters{EventsCreated: 1, EventsClosed: 1, UnpinOps: tc.wantUnpins, OpsPosted: 0})
		if tc.failAt != 1 {
			if !(0 < s.UnpinSeq && s.UnpinSeq < s.EventCloseSeq && s.EventCloseSeq < s.DoneSeq && s.FenceSeq == 0) {
				t.Fatalf("failAt %d: rollback order %+v", tc.failAt, s)
			}
		} else if !(s.UnpinSeq == 0 && 0 < s.EventCloseSeq && s.EventCloseSeq < s.DoneSeq && s.FenceSeq == 0) {
			t.Fatalf("failAt 1: rollback order %+v", s)
		}
	}
}

// ---- fence-query Control refusal / fault AFTER a real (synthetic) fence ----
// The wait returned WAIT_OBJECT_0, so native retirement is proven even though the
// result query could not run. The op must still unpin, close the event and close
// Done; the failure is only the Result source, plus a Fault when it is a real fault.

func TestWindowsHelperFenceQueryControlRefusedOrFaulted(t *testing.T) {
	boom := errors.New("injected control fault")
	for _, tc := range []struct {
		name      string
		hook      func(n int, f func(uintptr)) error // does NOT run f: query callback never executes
		wantSrc   windowsSocketResultSource
		wantFault bool
	}{
		{"ErrClosed", func(int, func(uintptr)) error { return &net.OpError{Op: "raw-control", Err: net.ErrClosed} }, windowsSocketResultControlClosed, false},
		{"other error", func(int, func(uintptr)) error { return boom }, windowsSocketResultControlFault, true},
		{"callback not run, nil error", func(int, func(uintptr)) error { return nil }, windowsSocketResultControlFault, true},
	} {
		rig := newHxRig()
		rig.pending()
		raw := hxOK(t, "")
		raw.controlHook = tc.hook // the waiter's fence query is the only Control call (no Cancel here)
		op, err := rig.rt.Submit(raw, hxParts(32))
		if err != nil {
			t.Fatal(err)
		}
		rig.api.waitCh <- hxWait{status: hxWaitObject0}
		hxAwait(t, op.Done(), tc.name+" Done")
		s := op.Snapshot()
		if hxClosed(op.FenceFailed()) || hxClosed(op.ReleaseFailed()) {
			t.Fatalf("%s: wrong terminal outcome", tc.name)
		}
		if s.Result.Source != tc.wantSrc || s.Post != windowsSocketPostNative997 || s.Result.OK {
			t.Fatalf("%s: result/post %+v", tc.name, s)
		}
		if tc.wantFault != (len(s.Faults) > 0) {
			t.Fatalf("%s: faults=%v want fault=%v", tc.name, s.Faults, tc.wantFault)
		}
		if tc.wantSrc == windowsSocketResultControlFault && s.Result.ControlErr == "" {
			t.Fatalf("%s: ControlErr empty", tc.name)
		}
		if rig.api.results != 0 {
			t.Fatalf("%s: native Result ran although Control never executed the callback", tc.name)
		}
		hxOrdered(t, s, true) // the fence really happened before the failed query
		if !s.Unpinned || !s.EventClosed || rig.pins.unpins() != 1 || rig.api.closes != 1 || rig.rt.LiveCount() != 0 {
			t.Fatalf("%s: not released after real fence: %+v", tc.name, s)
		}
		hxCounters(t, rig, windowsSocketCounters{1, 1, 1, 1})
	}
}

// ---- CloseEvent error after a real fence and Unpin: ReleaseFailed (private decision) ----
// Contract (RELEASE-FAILURE-DECISION.md): Done, FenceFailed and ReleaseFailed are
// mutually exclusive terminals. Native retirement is PROVEN, so Unpin is safe and the
// submitting thread need not stay locked; the event handle is retained by an owner in
// the registry; EventClosed stays false, EventsClosed counter stays 0, no EventCloseSeq
// or DoneSeq, no automatic re-close, no process exit.

func TestWindowsHelperCloseEventErrorReleaseFailed(t *testing.T) {
	for _, pend := range []bool{false, true} {
		rig := newHxRig()
		rig.api.closeErr = errors.New("injected CloseHandle failure")
		rig.api.result = windowsSocketNativeResult{OK: true, N: 8}
		if pend {
			rig.pending()
		}
		op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
		if op == nil {
			t.Fatalf("pend=%v: nil op (err=%v)", pend, err)
		}
		// Synchronous cleanup failure is returned; asynchronous cleanup remains
		// channel-reported, never a race-dependent constructor error.
		if !pend && !errors.Is(err, rig.api.closeErr) {
			t.Fatalf("native0 cleanup error=%v want wrapped CloseEvent error", err)
		}
		if pend && err != nil {
			t.Fatalf("native997 submission unexpectedly returned async cleanup error: %v", err)
		}
		if pend {
			rig.api.waitCh <- hxWait{status: hxWaitObject0}
		}
		hxAwait(t, op.ReleaseFailed(), "ReleaseFailed")
		if hxClosed(op.Done()) || hxClosed(op.FenceFailed()) {
			t.Fatalf("pend=%v: terminals not exclusive", pend)
		}
		s := op.Snapshot()
		if !s.Unpinned || s.UnpinSeq == 0 || s.FenceSeq == 0 || s.FenceSeq >= s.UnpinSeq {
			t.Fatalf("pend=%v: fence+unpin must precede the failed close: %+v", pend, s)
		}
		if s.EventClosed || s.EventCloseSeq != 0 || s.DoneSeq != 0 {
			t.Fatalf("pend=%v: close failure reported as success (EventClosed/EventCloseSeq/DoneSeq): %+v", pend, s)
		}
		if s.ReleaseErr == "" || len(s.Faults) == 0 || s.Result.Source == windowsSocketResultNone || !s.Result.OK {
			t.Fatalf("pend=%v: ReleaseErr/Faults/result missing: %+v", pend, s)
		}
		if rig.api.closes != 1 {
			t.Fatalf("pend=%v: CloseEvent attempted %d times (no automatic retry allowed)", pend, rig.api.closes)
		}
		if rig.pins.unpins() != 1 {
			t.Fatalf("pend=%v: unpins=%d want 1", pend, rig.pins.unpins())
		}
		owners := rig.rt.LiveOps()
		if rig.rt.LiveCount() != 1 || len(owners) != 1 || owners[0] != op {
			t.Fatalf("pend=%v: original event owner not retained in registry: live=%d owners=%v", pend, rig.rt.LiveCount(), owners)
		}
		hxCounters(t, rig, windowsSocketCounters{EventsCreated: 1, EventsClosed: 0, UnpinOps: 1, OpsPosted: 1})
		// No further native use: Cancel after release failure is already_done.
		before := rig.api.cancels
		if got := op.Cancel(); got != "already_done" || rig.api.cancels != before {
			t.Fatalf("pend=%v: Cancel after ReleaseFailed = %q (native cancels %d->%d)", pend, got, before, rig.api.cancels)
		}
		if rig.api.closes != 1 || hxClosed(op.Done()) {
			t.Fatalf("pend=%v: Cancel re-attempted close or completed the op", pend)
		}
		// Quarantined fake event owner is deliberately left (synthetic handle, OPEN-1).
	}
}

// ---- raw-value classification now owned by the core (reachable per instance) ----

func TestWindowsHelperCoreCreateEventClassification(t *testing.T) {
	for _, tc := range []struct {
		name    string
		callErr error
		want    string
		wantEn  syscall.Errno
	}{
		{"no errno", nil, "windows socket write: CreateEvent: CreateEventW failed without errno", 0},
		{"non-errno error", errors.New("opaque"), "windows socket write: CreateEvent: CreateEventW failed without errno", 0},
		{"errno", syscall.Errno(5), "", 5},
	} {
		rig := newHxRig()
		rig.api.createErr = tc.callErr
		if tc.callErr == nil {
			rig.api.createErr = errors.New("placeholder")
		}
		if tc.name == "no errno" {
			rig.api.createErr = syscall.Errno(0)
		}
		if tc.wantEn != 0 {
			rig.api.createErr = tc.wantEn
		}
		op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
		if op != nil || err == nil {
			t.Fatalf("%s: op=%v err=%v", tc.name, op != nil, err)
		}
		var en syscall.Errno
		if tc.wantEn != 0 {
			if !errors.As(err, &en) || en != tc.wantEn {
				t.Fatalf("%s: err=%v want wrapped errno %d", tc.name, err, tc.wantEn)
			}
		} else if err.Error() != tc.want {
			t.Fatalf("%s: err=%q want %q", tc.name, err.Error(), tc.want)
		}
		hxCounters(t, rig, windowsSocketCounters{})
	}
}

func TestWindowsHelperCoreResolveNamesProc(t *testing.T) {
	rig := newHxRig()
	rig.api.resolveName = "CreateEventW"
	rig.api.resolveErr = syscall.Errno(127)
	_, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
	var en syscall.Errno
	if err == nil || !errors.As(err, &en) || en != 127 || err.Error() != "windows socket write: resolve: resolve CreateEventW: "+syscall.Errno(127).Error() {
		t.Fatalf("resolve err=%v", err)
	}
}

func TestWindowsHelperCorePinPanicFormatting(t *testing.T) {
	rig := newHxRig()
	rig.pins.failAt, rig.pins.failPanic = 2, true
	op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
	if op == nil || err == nil || err.Error() != "windows socket write: pin: runtime.Pinner.Pin panic: fake pin panic" {
		t.Fatalf("op=%v err=%v", op != nil, err)
	}
	s := op.Snapshot()
	if len(s.Faults) != 1 || s.Faults[0] != err.Error() || s.Post != windowsSocketPostNotPosted {
		t.Fatalf("snapshot %+v", s)
	}
}

// Pin target order and tag are core-visible: adapter targets first (OVERLAPPED,
// WSABUF[0]), then one target per payload part; the core tags the event low bit.
func TestWindowsHelperCorePinTargetsAndTag(t *testing.T) {
	rig := newHxRig()
	rig.api.result = windowsSocketNativeResult{OK: true, N: 8}
	parts := hxParts(5, 7)
	op, err := rig.rt.Submit(hxOK(t, ""), parts)
	if err != nil || op == nil {
		t.Fatalf("submit: %v", err)
	}
	if len(rig.api.postTagged) != 1 || rig.api.postTagged[0]&1 != 1 || rig.api.postTagged[0] != uintptr(0x1000+4)|1 {
		t.Fatalf("tagged event %#v", rig.api.postTagged)
	}
	closed := rig.api.closedHandles
	if len(closed) != 1 || closed[0] != 0x1000+4 {
		t.Fatalf("CloseEvent must receive the ORIGINAL untagged handle: %v", closed)
	}
	pinned := rig.pins.made[0].pinned
	if len(pinned) != 4 || pinned[2] != reflect.ValueOf(parts[0]).Pointer() || pinned[3] != reflect.ValueOf(parts[1]).Pointer() {
		t.Fatalf("pin order wrong: %v", pinned)
	}
}

func TestWindowsHelperCoreDefaultFactoryUsed(t *testing.T) {
	rt := newWindowsSocketWriteRuntime(windowsSocketWriteDeps{})
	if rt.deps.API == nil || rt.deps.NewPinner == nil || rt.deps.NewPinner() == nil {
		t.Fatal("default factories not installed")
	}
}

// ---- B1: malformed NewPost output is a certain no-I/O setup fault ----

func TestWindowsHelperBadNewPostShapes(t *testing.T) {
	var nilPost *hxPost
	var nilByte *byte
	b1, b2, b3 := new(byte), new(byte), new(byte)
	good := &hxPost{}
	for _, tc := range []struct {
		name string
		post any
		pins []any
	}{
		{"nil post", nil, []any{b1, b2}},
		{"typed nil post", nilPost, []any{b1, b2}},
		{"scalar post", 7, []any{b1, b2}},
		{"zero pins", good, nil},
		{"one pin", good, []any{b1}},
		{"three pins", good, []any{b1, b2, b3}},
		{"nil pin 0", good, []any{nil, b2}},
		{"nil pin 1", good, []any{b1, nil}},
		{"typed nil pin", good, []any{nilByte, b2}},
		{"scalar pin", good, []any{b1, 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newHxRig()
			rig.pending() // a pending Send would be a real-I/O hazard if reached
			rig.api.badNewPost = func(uintptr, [][]byte) (any, []any) { return tc.post, tc.pins }
			raw := &hxRaw{mode: "run"}
			op, err := rig.rt.Submit(raw, hxParts(8, 8))
			if op == nil || err == nil {
				t.Fatalf("op=%v err=%v want diagnostic op and error", op != nil, err)
			}
			s := op.Snapshot()
			if rig.api.sends != 0 || s.WSASendCalls != 0 || s.Post != windowsSocketPostNotPosted || s.PinCalls != 0 {
				t.Fatalf("Send/Pin reached: sends=%d snap=%+v", rig.api.sends, s)
			}
			if len(s.Faults) != 1 || s.Faults[0] != err.Error() || !strings.Contains(err.Error(), "windows socket write: invalid native post: ") {
				t.Fatalf("fault/err wrong: faults=%v err=%v", s.Faults, err)
			}
			if !hxClosed(op.Done()) || hxClosed(op.FenceFailed()) || hxClosed(op.ReleaseFailed()) || !s.EventClosed || s.Unpinned || !s.Retired || s.FenceProven {
				t.Fatalf("not an exact no-IO rollback: %+v", s)
			}
			if rig.api.waits != 0 || rig.api.results != 0 || rig.api.cancels != 0 || rig.rt.LiveCount() != 0 {
				t.Fatalf("waits=%d results=%d cancels=%d live=%d", rig.api.waits, rig.api.results, rig.api.cancels, rig.rt.LiveCount())
			}
			rig.pins.mu.Lock()
			made, pinCalls := len(rig.pins.made), rig.pins.pinCalls
			rig.pins.mu.Unlock()
			if made != 0 || pinCalls != 0 || rig.pins.unpins() != 0 {
				t.Fatalf("pinner leaked or used: made=%d pins=%d unpins=%d", made, pinCalls, rig.pins.unpins())
			}
			if len(raw.cbRets) != 1 || !raw.cbRets[0] {
				t.Fatalf("callback must run once and return true: %v", raw.cbRets)
			}
			if got := op.Cancel(); got != "already_done" || rig.api.cancels != 0 {
				t.Fatalf("Cancel after rollback = %q", got)
			}
			hxCounters(t, rig, windowsSocketCounters{EventsCreated: 1, EventsClosed: 1, UnpinOps: 0, OpsPosted: 0})
		})
	}
}

// The same post value flows to Send, the post-query/fence-query Result and
// CancelIoEx; Send derives its lengths from the post, not from NewPost capture.
func TestWindowsHelperPostIdentityAndSendLengths(t *testing.T) {
	for _, pend := range []bool{false, true} {
		rig := newHxRig()
		rig.api.result = windowsSocketNativeResult{OK: true, N: 12}
		if pend {
			rig.pending()
		}
		op, err := rig.rt.Submit(hxOK(t, ""), hxParts(5, 7))
		if err != nil || op == nil {
			t.Fatalf("pend=%v submit: %v", pend, err)
		}
		if pend {
			if got := op.Cancel(); got != "cancel_issued" {
				t.Fatalf("Cancel = %q", got)
			}
			rig.api.waitCh <- hxWait{status: hxWaitObject0}
		}
		hxAwait(t, op.Done(), "Done")
		rig.api.mu.Lock()
		if len(rig.api.madePosts) != 1 || rig.api.sentPost == nil || !rig.api.madePosts[rig.api.sentPost] ||
			len(rig.api.sendN) != 1 || rig.api.sendN[0] != 2 ||
			len(rig.api.sendBufLens[0]) != 2 || rig.api.sendBufLens[0][0] != 5 || rig.api.sendBufLens[0][1] != 7 ||
			rig.api.results != 1 || (pend && rig.api.cancels != 1) {
			t.Fatalf("pend=%v: posts=%d sent=%v n=%v lens=%v results=%d cancels=%d", pend, len(rig.api.madePosts), rig.api.sentPost != nil, rig.api.sendN, rig.api.sendBufLens, rig.api.results, rig.api.cancels)
		}
		rig.api.mu.Unlock()
		hxCounters(t, rig, windowsSocketCounters{1, 1, 1, 1}) // also fails on any identity violation
	}
}

// ---- fix2: numeric post enum and typed domain sentinel keep their public text ----

func TestWindowsHelperPostEnumRenderedNames(t *testing.T) {
	for _, tc := range []struct {
		p    windowsSocketPost
		want string
	}{
		{windowsSocketPostNotPosted, "not_posted"},
		{windowsSocketPostNative0, "native0"},
		{windowsSocketPostNative997, "native997"},
		{windowsSocketPostError, "native_error"},
		{windowsSocketPostUnclassified, "native_unclassified"},
		{windowsSocketPost(200), "windowsSocketPost(200)"},
	} {
		if got := tc.p.String(); got != tc.want {
			t.Fatalf("String(%d)=%q want %q", uint8(tc.p), got, tc.want)
		}
		if got := fmt.Sprintf("%s|%q|%v", tc.p, tc.p, tc.p); got != tc.want+"|"+strconv.Quote(tc.want)+"|"+tc.want {
			t.Fatalf("formatted %d = %q", uint8(tc.p), got)
		}
	}
	var zero windowsSocketPost
	if zero != windowsSocketPostNotPosted {
		t.Fatal("zero value must be not_posted")
	}
}

// A real native_error post renders its public name through the snapshot.
func TestWindowsHelperPostEnumNativeErrorThroughSnapshot(t *testing.T) {
	rig := newHxRig()
	rig.api.sendErr = syscall.Errno(10054)
	op, _ := rig.rt.Submit(hxOK(t, ""), hxParts(8))
	if op == nil {
		t.Fatal("nil op")
	}
	if got := op.Snapshot().Post.String(); got != "native_error" {
		t.Fatalf("Post renders %q want native_error", got)
	}
}

func TestWindowsHelperDomainSentinelTypedIdentity(t *testing.T) {
	const text = "windows socket write: invalid part list"
	if errWindowsSocketWriteDomain.Error() != text {
		t.Fatalf("sentinel text %q", errWindowsSocketWriteDomain.Error())
	}
	cases := map[string][]uint64{"no parts": nil, "empty part": {1, 0}, "single over": {1 << 33}, "total over": {1 << 31, 1 << 31, 1 << 31}}
	for name, lens := range cases {
		_, err := windowsSocketWriteDomain(lens)
		if !errors.Is(err, errWindowsSocketWriteDomain) || !strings.HasPrefix(err.Error(), text+": ") {
			t.Fatalf("%s: err=%v", name, err)
		}
		var typed *windowsSocketDomainError
		if !errors.As(err, &typed) || error(typed) != errWindowsSocketWriteDomain {
			t.Fatalf("%s: not the single sentinel pointer", name)
		}
	}
	if errors.Is(errors.New(text), errWindowsSocketWriteDomain) || errors.Is(&windowsSocketDomainError{}, errWindowsSocketWriteDomain) {
		t.Fatal("identity must be the one var pointer, not text or type equality")
	}
}
