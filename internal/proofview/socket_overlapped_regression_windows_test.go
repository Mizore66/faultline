//go:build windows

package proofview

// Private, unexecuted regression tests for the extraction's static safety findings.
// All handles and pin operations are synthetic; no native syscall is executed.
import (
	"errors"
	"syscall"
	"testing"
)

type hxPanicAfterWrite struct{ base *hxRaw }

func (r *hxPanicAfterWrite) Read(f func(uintptr) bool) error { return r.base.Read(f) }
func (r *hxPanicAfterWrite) Control(f func(uintptr)) error   { return r.base.Control(f) }
func (r *hxPanicAfterWrite) Write(f func(uintptr) bool) error {
	if err := r.base.Write(f); err != nil {
		return err
	}
	panic("wrapper panic after pending waiter launch")
}

func TestWindowsHelperQuarantineCannotReleaseLater(t *testing.T) {
	rig := newHxRig()
	rig.pending()
	exited := make(chan struct{})
	rig.setHook(func(_ *windowsSocketWrite, step string) {
		if step == "waiter_exit" {
			close(exited)
		}
	})
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = rig.rt.Submit(&hxPanicAfterWrite{base: hxOK(t, "")}, hxParts(8))
	}()
	if recovered == nil {
		t.Fatal("wrapper panic was swallowed")
	}
	ops := rig.rt.LiveOps()
	if len(ops) != 1 {
		t.Fatalf("quarantine registry length=%d want1", len(ops))
	}
	op := ops[0]
	hxAwait(t, op.FenceFailed(), "quarantine")
	rig.api.waitCh <- hxWait{status: hxWaitObject0}
	hxAwait(t, exited, "quarantined waiter exit")
	s := op.Snapshot()
	if !hxClosed(op.FenceFailed()) || hxClosed(op.Done()) || hxClosed(op.ReleaseFailed()) || s.Unpinned || s.EventClosed {
		t.Fatalf("quarantine advanced to release: %+v", s)
	}
	owners := rig.rt.LiveOps()
	if len(owners) != 1 || owners[0] != op || rig.pins.unpins() != 0 || rig.rt.Counters().EventsClosed != 0 {
		t.Fatalf("quarantined native ownership was dropped: %+v", rig.rt.Counters())
	}
}

func TestWindowsHelperUnknownSendErrorRetainsOwnership(t *testing.T) {
	rig := newHxRig()
	unknown := errors.New("unclassified send boundary error")
	rig.api.sendErr = unknown
	op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
	if op == nil || !errors.Is(err, unknown) {
		t.Fatalf("op=%v error=%v want original error", op, err)
	}
	hxAwait(t, op.FenceFailed(), "unknown post quarantine")
	s := op.Snapshot()
	if s.PostErrno != 0 || s.Unpinned || s.EventClosed || hxClosed(op.Done()) || hxClosed(op.ReleaseFailed()) {
		t.Fatalf("unknown error fabricated errno/no-I/O release: %+v", s)
	}
	if len(s.Faults) == 0 || rig.rt.LiveCount() != 1 {
		t.Fatal("unclassified ownership not faulted and retained")
	}
	hxCounters(t, rig, windowsSocketCounters{EventsCreated: 1, OpsPosted: 1})
}

func TestWindowsHelperPinnerConstructionPanicRetainsCreatedEvent(t *testing.T) {
	rig := newHxRig()
	rt := newWindowsSocketWriteRuntime(windowsSocketWriteDeps{
		API:       rig.api,
		NewPinner: func() windowsSocketPinner { panic("pinner construction") },
	})
	var recovered any
	func() { defer func() { recovered = recover() }(); _, _ = rt.Submit(hxOK(t, ""), hxParts(8)) }()
	if recovered == nil {
		t.Fatal("setup panic swallowed")
	}
	if rt.Counters().EventsCreated != 1 {
		t.Fatal("created event was not recorded")
	}
	// Either safe no-post rollback closed the event or an explicit owner remains.
	// Never accept an untracked live handle. No fabricated posted operation.
	c := rt.Counters()
	if c.OpsPosted != 0 || (c.EventsClosed == 0 && rt.LiveCount() != 1) {
		t.Fatalf("created event lost on construction panic: counters=%+v owners=%d", c, rt.LiveCount())
	}
}

func TestWindowsHelperCancelBeforePublicationUsesNoNativePointer(t *testing.T) {
	rig := newHxRig()
	rig.api.result = windowsSocketNativeResult{OK: true, N: 8}
	// The constructor hook views its own runtime; no global dependency is replaced.
	var actual *windowsSocketWriteRuntime
	actual = newWindowsSocketWriteRuntime(windowsSocketWriteDeps{
		API: rig.api,
		NewPinner: func() windowsSocketPinner {
			owners := actual.LiveOps()
			if len(owners) != 1 {
				t.Fatal("event owner missing before pinner construction")
			}
			if got := owners[0].Cancel(); got != "already_done" {
				t.Fatalf("pre-post Cancel=%q", got)
			}
			rig.api.mu.Lock()
			calls := rig.api.cancels
			rig.api.mu.Unlock()
			if calls != 0 {
				t.Fatalf("pre-post Cancel invoked native cancellation %d times", calls)
			}
			return rig.pins.New()
		},
	})
	op, err := actual.Submit(hxOK(t, ""), hxParts(8))
	if err != nil {
		t.Fatal(err)
	}
	hxAwait(t, op.Done(), "normal completion after pre-post cancel")
}

func TestWindowsHelperCleanupPanicReportsProvenRetirement(t *testing.T) {
	for _, step := range []string{"pre_unpin", "pre_close_event", "pre_done"} {
		t.Run(step, func(t *testing.T) {
			rig := newHxRig()
			rig.api.result = windowsSocketNativeResult{OK: true, N: 8}
			rig.setHook(func(_ *windowsSocketWrite, s string) {
				if s == step {
					panic("injected cleanup observer")
				}
			})
			var recovered any
			func() { defer func() { recovered = recover() }(); _, _ = rig.rt.Submit(hxOK(t, ""), hxParts(8)) }()
			if recovered == nil {
				t.Fatal("synchronous cleanup panic swallowed")
			}
			owners := rig.rt.LiveOps()
			if len(owners) != 1 {
				t.Fatalf("retained ownership=%d", len(owners))
			}
			op := owners[0]
			hxAwait(t, op.ReleaseFailed(), "cleanup interruption")
			s := op.Snapshot()
			if !s.Retired || !s.FenceProven || hxClosed(op.FenceFailed()) || hxClosed(op.Done()) || s.ReleaseErr == "" {
				t.Fatalf("proven fence mislabeled or cleanup reported successful: %+v", s)
			}
			if s.Unpinned != (step != "pre_unpin") || s.EventClosed != (step == "pre_done") {
				t.Fatalf("cleanup phase flags wrong at %s: %+v", step, s)
			}
		})
	}
}

func TestWindowsHelperCancelBeforeSendReturnsIsNoop(t *testing.T) {
	rig := newHxRig()
	rig.api.result = windowsSocketNativeResult{OK: true, N: 8}
	observed := false
	rig.setHook(func(op *windowsSocketWrite, step string) {
		if step != "registered" {
			return
		}
		observed = true
		if got := op.Cancel(); got != "already_done" {
			t.Fatalf("pre-publication cancel result=%q want no-op", got)
		}
		rig.api.mu.Lock()
		calls := rig.api.cancels
		rig.api.mu.Unlock()
		if calls != 0 {
			t.Fatalf("pre-send Cancel called native API %d times", calls)
		}
	})
	op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
	if err != nil {
		t.Fatal(err)
	}
	hxAwait(t, op.Done(), "normal completion")
	if !observed {
		t.Fatal("pre-send observer did not execute")
	}
}

func TestWindowsHelperFalseResultWithoutErrnoIsFault(t *testing.T) {
	rig := newHxRig()
	rig.api.result = windowsSocketNativeResult{} // FALSE with no trustworthy errno
	op, err := rig.rt.Submit(hxOK(t, ""), hxParts(8))
	if op == nil || err == nil {
		t.Fatalf("invalid query was not returned as error: op=%v err=%v", op, err)
	}
	hxAwait(t, op.Done(), "physical fence permits safe cleanup")
	s := op.Snapshot()
	if s.Result.OK || s.Result.Err == "" || s.Result.Errno != syscall.Errno(0) || len(s.Faults) == 0 {
		t.Fatalf("missing errno fabricated or ignored: %+v", s)
	}
}
