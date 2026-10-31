//go:build linux && globalopen_hooks

package main

// TEST-ONLY REGRESSION INSTRUMENTATION. Compiled only with
// -tags globalopen_hooks, for the lifecycle regression tests in this
// directory; never part of the default helper.
//
//   - behavior-under-test drivers: rawWaitHook scripts the ORDER in which
//     already-real kernel wait statuses reach the helper's loop (never
//     inventing one); afterInfoHook and beforeSuppressHook are points where
//     a test makes a real group exit happen at an exact place;
//     afterLogOpenHook / beforeLogCloseHook reach the log descriptor so a test
//     can make a write or the final Close fail through the OS;
//   - SAFETY instrumentation: killHook records any numeric kill (the helper
//     makes none, so a call is a defect); signalGroupHook records each
//     pidfd group signal; signalErrHook lets a test make the group signal FAIL
//     without sending anything; exitHookFn turns os.Exit into a panic.

import (
	"os"
	"syscall"
)

var (
	rawWaitHook        func(ws *syscall.WaitStatus) (int, error)
	killHook           func(tid int)
	exitHookFn         func(code int)
	signalGroupHook    func(fd, tgid int)
	signalErrHook      func(fd, tgid int) error
	afterInfoHook      func(tid int, info syscallInfo)
	beforeSuppressHook func(tid int)
	beforeLogCloseHook func(f *os.File)
	afterLogOpenHook   func(f *os.File)

	// reapedTids records every tid whose Exited/Signaled status realWait4
	// returned. Per test run; used only by the safety guard in the tests.
	reapedTids = map[int]bool{}
)

// realWait4 is the real wait4(-1, __WALL) and the single place that records
// consumed exit statuses.
func realWait4(ws *syscall.WaitStatus) (int, error) {
	tid, err := syscall.Wait4(-1, ws, syscall.WALL, nil)
	if err == nil && (ws.Exited() || ws.Signaled()) {
		reapedTids[tid] = true
	}
	return tid, err
}

func rawWait4(ws *syscall.WaitStatus) (int, error) {
	if rawWaitHook != nil {
		return rawWaitHook(ws)
	}
	return realWait4(ws)
}

// killTid exists only so the tests can install killHook as a tripwire. The
// helper never calls it (no signal is ever sent by number).
func killTid(tid int) {
	if killHook != nil {
		killHook(tid)
	}
}

func signalGroup(fd, tgid int) error {
	if signalGroupHook != nil {
		signalGroupHook(fd, tgid)
	}
	if signalErrHook != nil {
		if err := signalErrHook(fd, tgid); err != nil {
			return err // nothing is sent
		}
	}
	return pidfdSendSignal(fd, syscall.SIGKILL)
}

func afterInfo(tid int, info syscallInfo) {
	if afterInfoHook != nil {
		afterInfoHook(tid, info)
	}
}

func beforeSuppress(tid int) {
	if beforeSuppressHook != nil {
		beforeSuppressHook(tid)
	}
}

func afterLogOpen(f *os.File) {
	if afterLogOpenHook != nil {
		afterLogOpenHook(f)
	}
}

func beforeLogClose(f *os.File) {
	if beforeLogCloseHook != nil {
		beforeLogCloseHook(f)
	}
}

func exitHook(code int) {
	if exitHookFn != nil {
		exitHookFn(code)
	}
	os.Exit(code)
}
