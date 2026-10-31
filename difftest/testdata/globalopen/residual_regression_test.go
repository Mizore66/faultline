//go:build linux && globalopen_hooks && globalopen_residual

package main

// RESIDUAL REGRESSIONS (path-pointer, open_how, suppress and drain-signal
// failures). Compiled only with -tags 'globalopen_hooks globalopen_residual'.
// Each carries the assertion that made it a meaningful RED before its fix and
// must not be weakened. No numeric signal is ever sent (the drain control
// makes the pidfd signal FAIL through signalErrHook without sending anything).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// childResid: thread X makes ONE open-group call on the target in the way the
// variant says; for peek/supp, thread K waits at getpid and then
// exit_group(9). The `how` and `idle` variants have no killer thread.
//
//	peek / supp  openat(target) with a valid path (the script kills K at the
//	             helper's seam: after GET_SYSCALL_INFO for peek, before the
//	             suppression for supp)
//	how          openat2(target, how=unmapped pointer, size 24): a real bad
//	             pointer; no death involved, no K (see the early return)
//	idle         the root blocks forever in rt_sigsuspend; nothing exits it
func childResid(target, variant string) {
	switch variant {
	case "how":
		// X makes the one bad-pointer openat2 and ends the process itself, so
		// nothing can hang.
		runtime.LockOSThread() // fixture only
		p, _ := syscall.BytePtrFromString(target)
		syscall.Syscall6(437, atFDCWDRaw, uintptr(unsafe.Pointer(p)), 0x10 /* unmapped */, 24, 0, 0)
		syscall.RawSyscall(syscall.SYS_EXIT_GROUP, 9, 0, 0)
		return
	case "idle":
		runtime.LockOSThread() // fixture only
		sigsuspendForever()
		return
	}
	go func() {
		runtime.LockOSThread() // fixture only
		setCommName("injX")
		p, _ := syscall.BytePtrFromString(target)
		fd, _, e := syscall.Syscall6(syscall.SYS_OPENAT, atFDCWDRaw, uintptr(unsafe.Pointer(p)), uintptr(syscall.O_RDONLY|syscall.O_CLOEXEC), 0, 0, 0)
		if e == 0 {
			syscall.Close(int(fd))
		}
		sigsuspendForever()
	}()
	go func() {
		runtime.LockOSThread() // fixture only
		setCommName("injK")
		rawGetpid()
		syscall.RawSyscall(syscall.SYS_EXIT_GROUP, 9, 0, 0)
	}()
	select {}
}

func residualTarget(t *testing.T) string {
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return target
}

// Path pointer read fails with ESRCH (the tracee dies between the helper's
// GET_SYSCALL_INFO and its readCString). Not a bad pointer: must be unhealthy.
func TestResidualPathPeekESRCH(t *testing.T) {
	target := residualTarget(t)
	s := &afterScript{}
	after := func(tid int, info syscallInfo) {
		if s.fired || s.parked == nil || readComm(tid) != "injX" || info.op != infoEntry || info.nr != sysOpenat {
			return
		}
		s.kill(tid, "x-entry-info-read")
	}
	r := runHelperWith(t, "resid", target, "peek", s.wait, after)
	residualComplete(t, s, r, "x-entry-info-read")
	h := recordOf(r.log, "HEALTH")
	if h == nil || recordOf(r.log, "SUMMARY") == nil {
		t.Fatalf("no HEALTH/SUMMARY (fatal=%q):\n%s", hasFatal(r.log), r.log)
	}
	if h["ok"] != "false" {
		t.Errorf("HEALTH ok=%q: an ESRCH path peek must not be benign unreadable: %v", h["ok"], h)
	}
	if n := strings.Count(r.log, "(INJECTED)"); n != 0 {
		t.Errorf("(INJECTED) %d times; the lost candidate delivered nothing", n)
	}
	if h["injected_started"] != "0" {
		t.Errorf("the token must not be consumed by an undecided candidate: %v", h)
	}
	checkConsistency(t, r)
}

// openat2 whose open_how pointer is genuinely bad while the path is valid and
// matches: the evidence about flags/mode is undecidable, so HEALTH is false.
// (No claim here that the peek error proves the kernel's EFAULT.)
func TestResidualHowPeekFault(t *testing.T) {
	target := residualTarget(t)
	r := runHelperWith(t, "resid", target, "how", nil, nil)
	if f := hasFatal(r.log); f != "" || r.exited {
		t.Fatalf("fatal path (hook exit=%t): %s\n%s", r.exited, f, r.log)
	}
	h := recordOf(r.log, "HEALTH")
	if h == nil || recordOf(r.log, "SUMMARY") == nil {
		t.Fatalf("no HEALTH/SUMMARY:\n%s", r.log)
	}
	if !strings.Contains(r.log, "how_ok=false") {
		t.Fatalf("INVALID PROBE: no openat2 candidate with how_ok=false was logged:\n%s", r.log)
	}
	if h["ok"] != "false" {
		t.Errorf("HEALTH ok=%q: an unreadable openat2 how struct on a matching path is undecidable evidence: %v", h["ok"], h)
	}
	if h["injected"] != "0" {
		t.Errorf("unknown how flags must never be accepted as known injection evidence (injected=%s): %v", h["injected"], h)
	}
	checkConsistency(t, r)
}

// The tracee dies at the injection candidate's ENTRY stop between the helper's
// GET_SYSCALL_INFO and its suppressSyscall: the known target's call was not
// suppressed, the token was never consumed, but the row is unhealthy.
func TestResidualSuppressESRCH(t *testing.T) {
	target := residualTarget(t)
	s := &afterScript{}
	residBefore = func(tid int) {
		if s.fired || s.parked == nil || readComm(tid) != "injX" {
			return
		}
		s.kill(tid, "x-before-suppress")
	}
	defer func() { residBefore = nil }()
	r := runHelperWith(t, "resid", target, "supp", s.wait, nil)
	residualComplete(t, s, r, "x-before-suppress")
	if f := hasFatal(r.log); f != "" || r.exited {
		t.Fatalf("the helper took its fatal path on ESRCH at suppress (hook exit=%t): %s\n%s", r.exited, f, r.log)
	}
	h := recordOf(r.log, "HEALTH")
	if h == nil || recordOf(r.log, "SUMMARY") == nil {
		t.Fatalf("no HEALTH/SUMMARY:\n%s", r.log)
	}
	if h["ok"] != "false" {
		t.Errorf("HEALTH ok=%q: a suppression failure for a known target must be unhealthy even if the token was not consumed: %v", h["ok"], h)
	}
	if n := strings.Count(r.log, "(INJECTED)"); n != 0 {
		t.Errorf("(INJECTED) %d times", n)
	}
	checkConsistency(t, r)
}

// A non-ESRCH group-signal failure in drain. The root idles forever in the
// kernel (nothing can end it), the main loop is made to fail its wait once
// (ECHILD) so fatal runs drain, and signalErrHook makes the pidfd signal fail
// with EPERM WITHOUT sending anything. The group stays owned but unsignalled.
//
// Binding expectation: drain must NOT wait for it (an unbounded wait for a
// process it never signalled would hang while the tracer lives). It must leave
// it to EXITKILL and say so loudly: a FATAL record, no SUMMARY, and no wait
// call after the failed signal. The probe is bounded by the hook, which
// returns ECHILD for any wait issued after the failed signal and records it;
// the real blocking wait4 is therefore never entered. The idle root ends when
// the helper's locked thread ends (EXITKILL). No signal of any kind is sent.
func TestResidualDrainSignalFailureDoesNotBlock(t *testing.T) {
	target := residualTarget(t)
	var armed, signalFailed, blockedWait bool
	residSignalErr = func(fd, tgid int) error {
		signalFailed = true
		return syscall.EPERM
	}
	defer func() { residSignalErr = nil }()
	wait := func(ws *syscall.WaitStatus) (int, error) {
		switch {
		case signalFailed:
			// A wait issued after the pidfd signal failed: this is the
			// unbounded wait for an unsignalled owned process. Record it and
			// refuse to enter the kernel wait.
			blockedWait = true
			return -1, syscall.ECHILD
		case armed:
			armed = false
			return -1, syscall.ECHILD // makes the main loop fatal() -> drain()
		}
		return realWait4(ws)
	}
	after := func(tid int, info syscallInfo) { armed = true }
	r := runHelperWith(t, "resid", target, "idle", wait, after)
	if !signalFailed {
		t.Fatalf("INVALID PROBE: drain never attempted the group signal; log:\n%s", r.log)
	}
	if blockedWait {
		t.Errorf("drain issued a wait after a non-ESRCH group-signal failure: it would block indefinitely on an owned, unsignalled process")
	}
	if hasFatal(r.log) == "" {
		t.Errorf("the incomplete cleanup must stay loud: no FATAL record in the log:\n%s", r.log)
	}
	if recordOf(r.log, "SUMMARY") != nil {
		t.Errorf("a run whose cleanup was incomplete must not produce a SUMMARY")
	}
	if len(r.killAttempts) != 0 {
		t.Errorf("OWNERSHIP VIOLATION: numeric kill attempted: %v", r.killAttempts)
	}
}

func residualComplete(t *testing.T, s *afterScript, r helperRun, label string) {
	t.Helper()
	for _, w := range []string{"park ", label, "killer-exit-pending-observed-by-waitid-nowait"} {
		found := false
		for _, e := range s.events {
			if strings.HasPrefix(e, w) {
				found = true
			}
		}
		if !found {
			t.Fatalf("INVALID PROBE: missing script event %q; events %q\nlog:\n%s", w, s.events, r.log)
		}
	}
}
