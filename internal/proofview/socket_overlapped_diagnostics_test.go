//go:build linux || darwin || windows

package proofview

// NATIVE-DIAGNOSTIC producers for the portable overlapped-socket core
// (socket_overlapped.go, fix2 base). Portable fake-backend diagnostics, not Winsock proof.
//
// Every child drives the ACTUAL portable core through the existing per-instance
// hx fakes (hxRig / hxNative / hxPins / hxRaw from socket_overlapped_helper_test.go)
// and builds ONE observed string purely from what the core returned or recorded:
// a returned error, Snapshot.Faults / ReleaseErr / TerminalReason / EventPollErr /
// Result.Err / Result.ControlErr, a Cancel return value, a recovered panic value,
// or a rendered Post status. The table's `want` is independently hand-written (or
// built from the native Errno.Error() for native errno text) and is compared BEFORE
// the single NATIVE-DIAGNOSTIC log line is emitted. The log line carries only the
// observed string, never `want` and never an AST-extracted literal.
//
// Same-package controlled fault injection (documented per child; no Go coverage
// counters are altered, and no expected literal is logged):
//   - sockDiagNewOp mirrors the op literal built inside Submit, used ONLY by the
//     "more-than-one-wsasend" child, whose guard (sendCalls > 1) is unreachable
//     through Submit because the cbCalls guard stops a second callback first. The
//     child synthetically presets sendCalls=1 without any prior Send, then calls
//     submitInCallback directly. One actual fake Send produces a logical count of 2.
//   - "lease-unavailable-after-publication" clears op.postReturned from pre_query
//     after publication to exercise the acquireLease guard under the fence query.
//   - "release-double-reentrant" re-enters op.release from the pre_unpin observer
//     (no field manipulation).
//   - "release-done-refused" calls op.cleanupPanic from the pre_done observer, the
//     same classifier a concurrent cleanup panic would use, so release finds a
//     terminal already owned.
//   - "release-terminal-owned" and "release-not-retired" call op.release directly on
//     a real quarantined / still-pending op to observe the refusal error value that
//     the worker path otherwise discards.
// No native resources: handles and pins are synthetic hx fakes; no sleeps, no
// retries; every wait is a bounded hxAwait (failure guard only).

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
)

// sockDiagAPI layers two overridable behaviors (PollEvent, WaitEvent) over the
// existing hxNative fake; everything else is the unchanged fake.
type sockDiagAPI struct {
	*hxNative
	poll func() (uint32, error)
	wait func() (uint32, error)
}

func (a *sockDiagAPI) PollEvent(ev windowsSocketHandle) (uint32, error) {
	if a.poll != nil {
		return a.poll()
	}
	return a.hxNative.PollEvent(ev)
}

func (a *sockDiagAPI) WaitEvent(ev windowsSocketHandle) (uint32, error) {
	if a.wait != nil {
		return a.wait()
	}
	return a.hxNative.WaitEvent(ev)
}

// newSockDiagRig is newHxRig with the runtime rebuilt over a sockDiagAPI that
// wraps the rig's own hxNative (same pins, same observer hook).
func newSockDiagCheckedRig(t *testing.T) *hxRig {
	t.Helper()
	r := newHxRig()
	t.Cleanup(func() {
		r.api.mu.Lock()
		defer r.api.mu.Unlock()
		if len(r.api.badPost) != 0 {
			t.Errorf("native post identity violations: %v", r.api.badPost)
		}
	})
	return r
}

func newSockDiagRig(t *testing.T, poll, wait func() (uint32, error)) *hxRig {
	r := newSockDiagCheckedRig(t)
	r.rt = newWindowsSocketWriteRuntime(windowsSocketWriteDeps{
		API:       &sockDiagAPI{hxNative: r.api, poll: poll, wait: wait},
		NewPinner: r.pins.New,
		Observe: func(op *windowsSocketWrite, step string) {
			if h := r.hook.Load(); h != nil {
				(*h)(op, step)
			}
		},
	})
	return r
}

// sockDiagTwiceRaw runs the Write callback twice (a misbehaving RawConn).
type sockDiagTwiceRaw struct{ hxRaw }

func (r *sockDiagTwiceRaw) Write(f func(uintptr) bool) error {
	f(0x7001)
	f(0x7001)
	return nil
}

// sockDiagPanicRaw runs the callback once, then panics out of Write.
type sockDiagPanicRaw struct{ hxRaw }

func (r *sockDiagPanicRaw) Write(f func(uintptr) bool) error {
	f(0x7001)
	panic("wrapper panic after pending post")
}

// sockDiagNewOp mirrors Submit's op literal in socket_overlapped.go; keep these
// fields in sync when Submit changes. This test-only mirror can otherwise drift.
func sockDiagNewOp(rt *windowsSocketWriteRuntime, raw *hxRaw, parts [][]byte) *windowsSocketWrite {
	w := &windowsSocketWrite{
		rt: rt, raw: raw,
		done: make(chan struct{}), fenceFailed: make(chan struct{}), releaseFailed: make(chan struct{}),
		parts:  append([][]byte(nil), parts...),
		post:   windowsSocketPostNotPosted,
		nParts: len(parts), bytes: hxTotal(parts),
	}
	w.cond = sync.NewCond(&w.mu)
	return w
}

func sockDiagJoin(parts ...string) string { return strings.Join(parts, " | ") }

func sockDiagText(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error from the core, got nil")
	}
	return err.Error()
}

func sockDiagFaultCount(t *testing.T, s windowsSocketWriteSnapshot, n int) {
	t.Helper()
	if len(s.Faults) != n {
		t.Fatalf("faults %q, want exactly %d", s.Faults, n)
	}
}

// sockDiagFence delivers a real (synthetic) fence to a pending op and waits for Done.
func sockDiagFence(t *testing.T, rig *hxRig, op *windowsSocketWrite) {
	t.Helper()
	rig.api.waitCh <- hxWait{status: hxWaitObject0}
	hxAwait(t, op.Done(), "Done after fence")
}

func sockDiagOK() windowsSocketNativeResult {
	return windowsSocketNativeResult{OK: true, N: 8}
}

// sockDiagRecover runs f and returns the recovered panic value (nil if none).
func sockDiagRecover(f func()) (rec any) {
	defer func() { rec = recover() }()
	f()
	return nil
}

type sockDiagCase struct {
	name  string
	sites string // semantic core position(s) this child must reach (see PRODUCERS.md)
	want  string // independently hand-written; compared before emit
	run   func(t *testing.T) string
}

func sockDiagCases() []sockDiagCase {
	return []sockDiagCase{
		{
			name:  "domain-no-parts",
			sites: "Submit domain gate; windowsSocketDomainError.Error",
			want:  "windows socket write: invalid part list: no parts",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				op, err := rig.rt.Submit(hxOK(t, ""), nil)
				if op != nil || !errors.Is(err, errWindowsSocketWriteDomain) {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				return err.Error()
			},
		},
		{
			name:  "domain-empty-part",
			sites: "Submit domain gate; windowsSocketDomainError.Error",
			want:  "windows socket write: invalid part list: part 1 is empty",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(1, 0))
				if op != nil || !errors.Is(err, errWindowsSocketWriteDomain) {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				return err.Error()
			},
		},
		{
			name:  "domain-part-exceeds-dword",
			sites: "windowsSocketWriteDomain: part length exceeds DWORD",
			want:  "windows socket write: invalid part list: part 0 length 8589934592 exceeds DWORD",
			run: func(t *testing.T) string {
				_, err := windowsSocketWriteDomain([]uint64{1 << 33})
				if !errors.Is(err, errWindowsSocketWriteDomain) {
					t.Fatalf("err=%v", err)
				}
				return err.Error()
			},
		},
		{
			name:  "domain-total-exceeds-dword",
			sites: "windowsSocketWriteDomain: total exceeds DWORD",
			want:  "windows socket write: invalid part list: total exceeds DWORD at part 1",
			run: func(t *testing.T) string {
				_, err := windowsSocketWriteDomain([]uint64{math.MaxUint32, 1})
				if !errors.Is(err, errWindowsSocketWriteDomain) {
					t.Fatalf("err=%v", err)
				}
				return err.Error()
			},
		},
		{
			name:  "submit-nil-rawconn",
			sites: "Submit: nil RawConn guard",
			want:  "windows socket write: nil RawConn",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				op, err := rig.rt.Submit(nil, hxParts(8))
				if op != nil {
					t.Fatal("nil RawConn produced an op")
				}
				return sockDiagText(t, err)
			},
		},
		{
			name:  "submit-resolve-failure",
			sites: "Submit: resolve wrapper; windowsSocketResolveError",
			want:  "windows socket write: resolve: resolve WSASend: proc table unavailable",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.resolveName = "WSASend"
				rig.api.resolveErr = errors.New("proc table unavailable")
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op != nil {
					t.Fatal("resolve failure produced an op")
				}
				return sockDiagText(t, err)
			},
		},
		{
			name:  "createevent-without-errno",
			sites: "windowsSocketCreateEventError: no errno; submitInCallback CreateEvent wrapper",
			want:  "windows socket write: CreateEvent: CreateEventW failed without errno",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.createErr = errors.New("opaque create failure")
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op != nil {
					t.Fatal("CreateEvent failure produced an op")
				}
				return sockDiagText(t, err)
			},
		},
		{
			name:  "createevent-errno",
			sites: "windowsSocketCreateEventError: errno wrap; submitInCallback CreateEvent wrapper",
			want:  "windows socket write: CreateEvent: CreateEventW failed: " + syscall.Errno(5).Error(),
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.createErr = syscall.Errno(5)
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op != nil {
					t.Fatal("CreateEvent failure produced an op")
				}
				return sockDiagText(t, err)
			},
		},
		{
			name:  "newpost-nil-post",
			sites: "windowsSocketPostShapeError: nil post; submitInCallback invalid native post wrapper",
			want:  "windows socket write: invalid native post: NewPost returned a nil or non-pointer post",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.badNewPost = func(uintptr, [][]byte) (any, []any) { return nil, []any{new(byte), new(byte)} }
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op == nil {
					t.Fatal("expected a rolled-back diagnostic op")
				}
				return sockDiagText(t, err)
			},
		},
		{
			name:  "newpost-pin-count",
			sites: "windowsSocketPostShapeError: pin target count",
			want:  "windows socket write: invalid native post: NewPost returned 1 adapter pin targets, want exactly 2",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.badNewPost = func(uintptr, [][]byte) (any, []any) { return &hxPost{}, []any{new(byte)} }
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op == nil {
					t.Fatal("expected a rolled-back diagnostic op")
				}
				return sockDiagText(t, err)
			},
		},
		{
			name:  "newpost-pin-target-nil",
			sites: "windowsSocketPostShapeError: pin target nil or not a pointer",
			want:  "windows socket write: invalid native post: NewPost pin target 1 is nil or not a pointer",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.badNewPost = func(uintptr, [][]byte) (any, []any) { return &hxPost{}, []any{new(byte), nil} }
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op == nil {
					t.Fatal("expected a rolled-back diagnostic op")
				}
				return sockDiagText(t, err)
			},
		},
		{
			name:  "pin-panic-result",
			sites: "windowsSocketPinError: recovered pin panic; submitInCallback pin wrapper",
			want:  "windows socket write: pin: runtime.Pinner.Pin panic: fake pin panic",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pins.failAt, rig.pins.failPanic = 1, true
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op == nil {
					t.Fatal("expected a rolled-back diagnostic op")
				}
				return sockDiagText(t, err)
			},
		},
		{
			name:  "pin-error-result",
			sites: "submitInCallback pin wrapper (plain pin error)",
			want:  "windows socket write: pin: fake pin failure",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pins.failAt = 2
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op == nil {
					t.Fatal("expected a rolled-back diagnostic op")
				}
				return sockDiagText(t, err)
			},
		},
		{
			name:  "send-native-errno-post-status",
			sites: "submitInCallback WSASend errno wrapper; windowsSocketPost.String native_error",
			want:  "windows socket write: WSASend: " + syscall.Errno(10054).Error() + " | native_error",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.sendErr = syscall.Errno(10054)
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op == nil {
					t.Fatal("expected a diagnostic op")
				}
				return sockDiagJoin(sockDiagText(t, err), op.Snapshot().Post.String())
			},
		},
		{
			name:  "send-unclassified-quarantine",
			sites: "submitInCallback WSASend unclassified wrapper; quarantine reason; windowsSocketPost.String native_unclassified",
			want: "windows socket write: WSASend returned unclassified error: unclassified boundary | native_unclassified | " +
				"unclassified WSASend error; native ownership uncertain: unclassified boundary",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.sendErr = errors.New("unclassified boundary")
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op == nil {
					t.Fatal("expected a quarantined diagnostic op")
				}
				s := op.Snapshot()
				if s.Terminal != "fence_failed" {
					t.Fatalf("terminal %q", s.Terminal)
				}
				return sockDiagJoin(sockDiagText(t, err), s.Post.String(), s.TerminalReason)
			},
		},
		{
			name:  "post-status-unknown-value",
			sites: "windowsSocketPost.String default branch",
			want:  "windowsSocketPost(200)",
			run: func(t *testing.T) string {
				return windowsSocketPost(200).String()
			},
		},
		{
			name:  "submit-panic-rolled-back",
			sites: "Submit deferred recover: panic before post, rolled back",
			want:  "pinner factory exploded | panic before post, rolled back: pinner factory exploded",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				var rt *windowsSocketWriteRuntime
				var seen []*windowsSocketWrite
				rt = newWindowsSocketWriteRuntime(windowsSocketWriteDeps{
					API: rig.api,
					NewPinner: func() windowsSocketPinner {
						seen = rt.LiveOps() // the op is registered before NewPinner runs
						panic("pinner factory exploded")
					},
				})
				rec := sockDiagRecover(func() { rt.Submit(hxOK(t, ""), hxParts(8)) })
				if rec == nil || len(seen) != 1 {
					t.Fatalf("recovered=%v seen=%d", rec, len(seen))
				}
				s := seen[0].Snapshot()
				sockDiagFaultCount(t, s, 1)
				return sockDiagJoin(fmt.Sprint(rec), s.Faults[0])
			},
		},
		{
			name:  "submit-panic-after-post-quarantined",
			sites: "Submit deferred recover: panic during submit (native ownership possible)",
			want: "injected post-send panic | panic during submit (native ownership possible or release already claimed): " +
				"injected post-send panic",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pending()
				rig.setHook(func(_ *windowsSocketWrite, step string) {
					if step == "post_send_returned" {
						panic("injected post-send panic")
					}
				})
				rec := sockDiagRecover(func() { rig.rt.Submit(hxOK(t, ""), hxParts(8)) })
				ops := rig.rt.LiveOps()
				if rec == nil || len(ops) != 1 {
					t.Fatalf("recovered=%v live=%d", rec, len(ops))
				}
				s := ops[0].Snapshot()
				if s.Terminal != "fence_failed" {
					t.Fatalf("terminal %q", s.Terminal)
				}
				return sockDiagJoin(fmt.Sprint(rec), s.TerminalReason)
			},
		},
		{
			name:  "rawconn-callback-twice",
			sites: "Submit callback: duplicate RawConn callback fault",
			want:  "RawConn.Write callback invoked more than once; no second post",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.result = sockDiagOK()
				op, err := rig.rt.Submit(&sockDiagTwiceRaw{}, hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				hxAwait(t, op.Done(), "Done")
				if rig.api.sends != 1 {
					t.Fatalf("sends=%d, want exactly one post", rig.api.sends)
				}
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 1)
				return s.Faults[0]
			},
		},
		{
			name:  "rawconn-callback-not-run",
			sites: "Submit: Write returned without running the callback",
			want:  "windows socket write: RawConn.Write returned without running the callback",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				op, err := rig.rt.Submit(&hxRaw{mode: "nocb"}, hxParts(8))
				if op != nil {
					t.Fatal("callback-not-run produced an op")
				}
				return sockDiagText(t, err)
			},
		},
		{
			name:  "rawconn-outer-error-after-callback",
			sites: "Submit: RawConn.Write error after callback fault and raw write wrapper",
			want: "windows socket write: raw write: wrapper closed late | " +
				"RawConn.Write error after callback: wrapper closed late",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.result = sockDiagOK()
				op, err := rig.rt.Submit(&hxRaw{mode: "errafter", outerErr: errors.New("wrapper closed late")}, hxParts(8))
				if op == nil {
					t.Fatal("expected a diagnostic op")
				}
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 1)
				return sockDiagJoin(sockDiagText(t, err), s.Faults[0])
			},
		},
		{
			name:  "more-than-one-wsasend",
			sites: "submitInCallback send-count invariant (sendCalls > 1); SAME-PACKAGE injection",
			want:  "more than one WSASend",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.result = sockDiagOK()
				w := sockDiagNewOp(rig.rt, hxOK(t, ""), hxParts(8))
				// Synthetic internal count: NO prior Send occurred. Submit's cbCalls
				// guard makes a second send unreachable; exercise the invariant only.
				w.sendCalls = 1
				setupErr, postErr := w.submitInCallback(0x7001)
				if setupErr != nil || postErr != nil {
					t.Fatalf("setup=%v post=%v", setupErr, postErr)
				}
				if err := w.release(false); err != nil {
					t.Fatalf("cleanup release: %v", err)
				}
				s := w.Snapshot()
				sockDiagFaultCount(t, s, 1)
				rig.api.mu.Lock()
				actualSends := rig.api.sends
				rig.api.mu.Unlock()
				// Count 2 includes the synthetic preset, not a second native Send.
				if actualSends != 1 || s.WSASendCalls != 2 {
					t.Fatalf("actual sends=%d helper count=%d; want 1 and 2", actualSends, s.WSASendCalls)
				}
				return s.Faults[0]
			},
		},
		{
			name:  "post-query-false-without-errno",
			sites: "submitInCallback native0 result query FALSE without errno (post-query copy) and error wrapper",
			want: "windows socket write: WSAGetOverlappedResult returned FALSE without errno | " +
				"WSAGetOverlappedResult returned FALSE without errno",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.result = windowsSocketNativeResult{OK: false}
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op == nil {
					t.Fatal("expected a diagnostic op")
				}
				return sockDiagJoin(sockDiagText(t, err), op.Snapshot().Result.Err)
			},
		},
		{
			name:  "fence-query-false-without-errno",
			sites: "fenceQuery FALSE without errno (fence-query copy of the literal)",
			want:  "WSAGetOverlappedResult returned FALSE without errno",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pending()
				rig.api.result = windowsSocketNativeResult{OK: false}
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				sockDiagFence(t, rig, op)
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 1)
				if s.Faults[0] != s.Result.Err {
					t.Fatalf("fault %q differs from result error %q", s.Faults[0], s.Result.Err)
				}
				return s.Result.Err
			},
		},
		{
			name:  "poll-unexpected-status",
			sites: "Snapshot: unexpected poll status",
			want:  "unexpected poll status 7",
			run: func(t *testing.T) string {
				rig := newSockDiagRig(t, func() (uint32, error) { return 7, nil }, nil)
				rig.pending()
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				observed := op.Snapshot().EventPollErr
				sockDiagFence(t, rig, op)
				return observed
			},
		},
		{
			name:  "waiter-unfenced-wait-status",
			sites: "waitAndRelease: wait for completion event not fenced",
			want:  "wait for completion event not fenced: status=258 err=<nil>",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pending()
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				rig.api.waitCh <- hxWait{status: hxWaitTimeout}
				hxAwait(t, op.FenceFailed(), "FenceFailed")
				return op.Snapshot().TerminalReason
			},
		},
		{
			name:  "waiter-panic-quarantines",
			sites: "waitAndRelease deferred recover: panic in waiter",
			want:  "panic in waiter: injected wait panic",
			run: func(t *testing.T) string {
				rig := newSockDiagRig(t, nil, func() (uint32, error) { panic("injected wait panic") })
				rig.pending()
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				hxAwait(t, op.FenceFailed(), "FenceFailed")
				return op.Snapshot().TerminalReason
			},
		},
		{
			name: "waiter-exit-observer-panic",
			// Settled with synctest.Wait: the fault is appended by the waiter goroutine
			// after Done is closed, so there is no channel that orders it for free.
			sites: "waitAndRelease exit observer recover: panic in waiter_exit observer",
			want:  "panic in waiter_exit observer: injected exit observer panic",
			run: func(t *testing.T) string {
				var observed string
				synctest.Test(t, func(t *testing.T) {
					rig := newSockDiagCheckedRig(t)
					rig.pending()
					rig.api.result = sockDiagOK()
					rig.setHook(func(_ *windowsSocketWrite, step string) {
						if step == "waiter_exit" {
							panic("injected exit observer panic")
						}
					})
					op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
					if err != nil || op == nil {
						t.Fatalf("op=%v err=%v", op != nil, err)
					}
					sockDiagFence(t, rig, op)
					synctest.Wait() // every other goroutine in the bubble has exited
					s := op.Snapshot()
					sockDiagFaultCount(t, s, 1)
					observed = s.Faults[0]
				})
				return observed
			},
		},
		{
			name:  "fence-after-quarantine",
			sites: "Submit quarantine (panic during submit) then waitAndRelease: fence observed after quarantine",
			want: "panic during submit (native ownership possible or release already claimed): wrapper panic after pending post | " +
				"fence observed after quarantine; op stays quarantined",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pending()
				exited := make(chan struct{})
				rig.setHook(func(_ *windowsSocketWrite, step string) {
					if step == "waiter_exit" {
						close(exited)
					}
				})
				rec := sockDiagRecover(func() { rig.rt.Submit(&sockDiagPanicRaw{}, hxParts(8)) })
				ops := rig.rt.LiveOps()
				if rec == nil || len(ops) != 1 {
					t.Fatalf("recovered=%v live=%d", rec, len(ops))
				}
				op := ops[0]
				hxAwait(t, op.FenceFailed(), "FenceFailed")
				rig.api.waitCh <- hxWait{status: hxWaitObject0}
				hxAwait(t, exited, "waiter exit")
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 2)
				return sockDiagJoin(s.Faults[0], s.Faults[1])
			},
		},
		{
			name:  "lease-unavailable-after-publication",
			sites: "fenceQuery acquireLease refusal: fence query lease unavailable + ControlErr; SAME-PACKAGE injection",
			want:  "fence query lease unavailable | lease unavailable",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pending()
				rig.api.result = sockDiagOK()
				// Injected: clear postReturned AFTER publication, once the fence is
				// stamped at pre_query. This synthetic state tests acquireLease refusal.
				rig.setHook(func(op *windowsSocketWrite, step string) {
					if step == "pre_query" {
						op.with(func() { op.postReturned = false })
					}
				})
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				sockDiagFence(t, rig, op)
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 1)
				return sockDiagJoin(s.Faults[0], s.Result.ControlErr)
			},
		},
		{
			name:  "fence-query-control-did-not-run",
			sites: "fenceQuery: RawConn.Control did not run the callback; fence query control fault",
			want: "fence query control fault: RawConn.Control did not run the callback | " +
				"RawConn.Control did not run the callback",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pending()
				raw := hxOK(t, "")
				raw.controlHook = func(int, func(uintptr)) error { return nil }
				op, err := rig.rt.Submit(raw, hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				sockDiagFence(t, rig, op)
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 1)
				return sockDiagJoin(s.Faults[0], s.Result.ControlErr)
			},
		},
		{
			name:  "fence-query-control-error",
			sites: "fenceQuery: fence query control fault with a Control error",
			want:  "fence query control fault: control plane refused",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pending()
				raw := hxOK(t, "")
				raw.controlHook = func(int, func(uintptr)) error { return errors.New("control plane refused") }
				op, err := rig.rt.Submit(raw, hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				sockDiagFence(t, rig, op)
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 1)
				return s.Faults[0]
			},
		},
		{
			name:  "cancel-control-fault",
			sites: "Cancel: cancel control fault (Control refused)",
			want:  "fault_control | cancel control fault: ran=false err=control plane refused cancel",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pending()
				rig.api.result = sockDiagOK()
				raw := hxOK(t, "")
				raw.controlHook = func(n int, f func(uintptr)) error {
					if n == 1 { // the explicit Cancel; the waiter's fence query is call 2
						return errors.New("control plane refused cancel")
					}
					f(0x7001)
					return nil
				}
				op, err := rig.rt.Submit(raw, hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				got := op.Cancel()
				sockDiagFence(t, rig, op)
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 1)
				return sockDiagJoin(string(got), s.Faults[0])
			},
		},
		{
			name:  "cancel-native-errno-fault",
			sites: "Cancel: CancelIoEx error (errno other than not-found)",
			want:  "fault_cancel | CancelIoEx error: " + syscall.Errno(87).Error(),
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pending()
				rig.api.result = sockDiagOK()
				rig.api.cancelErr = syscall.Errno(87)
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				got := op.Cancel()
				sockDiagFence(t, rig, op)
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 1)
				return sockDiagJoin(string(got), s.Faults[0])
			},
		},
		{
			name:  "release-cleanup-panic-worker",
			sites: "release deferred recover -> cleanupPanic: cleanup panic in phase",
			want: "cleanup panic in phase 0 after retirement (unpinned=false eventClosed=false): injected release interruption | " +
				"cleanup panic in phase 0 after retirement (unpinned=false eventClosed=false): injected release interruption",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pending()
				rig.api.result = sockDiagOK()
				rig.setHook(func(_ *windowsSocketWrite, step string) {
					if step == "pre_unpin" {
						panic("injected release interruption")
					}
				})
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				rig.api.waitCh <- hxWait{status: hxWaitObject0}
				hxAwait(t, op.ReleaseFailed(), "ReleaseFailed")
				s := op.Snapshot()
				if s.Terminal != "release_failed" {
					t.Fatalf("terminal %q", s.Terminal)
				}
				return sockDiagJoin(s.ReleaseErr, s.TerminalReason)
			},
		},
		{
			name:  "release-terminal-owned",
			sites: "release refusal: terminal already owned (+ refuse error prefix); direct call on a real quarantined op",
			want:  "windows socket write: release refused: terminal already owned | release refused: terminal already owned",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pending()
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				rig.api.waitCh <- hxWait{status: hxWaitTimeout}
				hxAwait(t, op.FenceFailed(), "FenceFailed")
				rerr := op.release(false)
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 2)
				return sockDiagJoin(sockDiagText(t, rerr), s.Faults[1])
			},
		},
		{
			name:  "release-double-reentrant",
			sites: "release refusal: double release (re-entered from the pre_unpin observer, no field edit)",
			want:  "windows socket write: double release | double release",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.result = sockDiagOK()
				var reErr error
				fired := false
				rig.setHook(func(op *windowsSocketWrite, step string) {
					if step == "pre_unpin" && !fired {
						fired = true
						reErr = op.release(false)
					}
				})
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				hxAwait(t, op.Done(), "Done")
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 1)
				return sockDiagJoin(sockDiagText(t, reErr), s.Faults[0])
			},
		},
		{
			name:  "release-not-retired",
			sites: "release refusal: native retirement not established; direct call on a real pending op",
			want: "windows socket write: release refused: native retirement not established | " +
				"release refused: native retirement not established",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.pending()
				rig.api.result = sockDiagOK()
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				rerr := op.release(false)
				sockDiagFence(t, rig, op)
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 1)
				return sockDiagJoin(sockDiagText(t, rerr), s.Faults[0])
			},
		},
		{
			name:  "closeevent-failure-release-failed",
			sites: "release: CloseEvent failed (ReleaseErr, fault, TerminalReason) and CloseEvent wrapper",
			want: "windows socket write: CloseEvent: CloseHandle refused | CloseHandle refused | CloseEvent failed | " +
				"CloseEvent failed: CloseHandle refused",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.result = sockDiagOK()
				rig.api.closeErr = errors.New("CloseHandle refused")
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op == nil {
					t.Fatal("expected a diagnostic op")
				}
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 1)
				return sockDiagJoin(sockDiagText(t, err), s.ReleaseErr, s.TerminalReason, s.Faults[0])
			},
		},
		{
			name:  "release-done-refused",
			sites: "release pre_done: Done refused terminal owned (fault + error); SAME-PACKAGE cleanupPanic from observer",
			want: "windows socket write: Done refused: terminal already owned | " +
				"Done refused: terminal already owned",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.result = sockDiagOK()
				// Injected: a competing cleanup classification takes the terminal right
				// before Done is claimed (the guard under test is release's pre-Done check).
				rig.setHook(func(op *windowsSocketWrite, step string) {
					if step == "pre_done" {
						op.cleanupPanic("competing cleanup classification")
					}
				})
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if op == nil {
					t.Fatal("expected a diagnostic op")
				}
				s := op.Snapshot()
				sockDiagFaultCount(t, s, 2)
				return sockDiagJoin(sockDiagText(t, err), s.Faults[1])
			},
		},
		{
			name:  "release-complete-terminal-done",
			sites: "release: cleanup complete terminal reason",
			want:  "cleanup complete | done",
			run: func(t *testing.T) string {
				rig := newSockDiagCheckedRig(t)
				rig.api.result = sockDiagOK()
				op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
				if err != nil || op == nil {
					t.Fatalf("op=%v err=%v", op != nil, err)
				}
				hxAwait(t, op.Done(), "Done")
				s := op.Snapshot()
				return sockDiagJoin(s.TerminalReason, s.Terminal)
			},
		},
	}
}

func TestSocketOverlappedNativeDiagnostics(t *testing.T) {
	for _, c := range sockDiagCases() {
		t.Run(c.name, func(t *testing.T) {
			got := c.run(t)
			if got != c.want {
				t.Fatalf("observed %q does not match the independent expectation %q", got, c.want)
			}
			observed, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("NATIVE-DIAGNOSTIC %s", observed)
		})
	}
}
