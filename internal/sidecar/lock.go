package sidecar

import (
	"io/fs"
	"math"
	"os"
	"time"

	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/nodefs"
)

// codex-sidecar.ts:201-205.
const (
	lockWaitMs  = 2_000
	lockRetryMs = 25
	// Codex gives the configured command 30 seconds. A holder older than
	// this is no longer credible as a live hook process.
	staleLockMs = 45_000
)

// sidecarLockPath ports codex-sidecar.ts:450.
func sidecarLockPath(ledgerPath string) string { return jsexc.Concat(ledgerPath, ".sidecar.lock") }

// jsRound is Math.round (V8's Float64Round): floor, plus one when the
// fraction is at least 0.5, so half rounds toward +Infinity and a value in
// [-0.5, 0) is -0.
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || x == math.Trunc(x) {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	if r == 0 && x < 0 {
		return math.Copysign(0, -1)
	}
	return r
}

// sidecarLockHealth ports codex-sidecar.ts:454: Undefined when there is no
// lock, {state: "INVALID"} for a symlink or non-file, else the state and
// age (one Date.now() read).
func sidecarLockHealth(ledgerPath string) jsjson.Value {
	info, err := nodefs.Lstat(sidecarLockPath(ledgerPath))
	if err != nil {
		if filesystemErrorCode(err) == "ENOENT" {
			return jsjson.Value{}
		}
		jsexc.Throw(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return jsjson.MustObject("state", jsjson.MakeString("INVALID"))
	}
	age := math.Max(0, jsRound(float64(clock.NowMillis())-nodefs.MtimeMs(info)))
	state := "ACTIVE"
	if age > staleLockMs {
		state = "STALE"
	}
	return jsjson.MustObject("state", jsjson.MakeString(state), "ageMilliseconds", jsjson.MakeNumber(age))
}

// waitForSidecarLock ports codex-sidecar.ts:469 (Atomics.wait).
func waitForSidecarLock(milliseconds int64) {
	time.Sleep(time.Duration(milliseconds) * time.Millisecond)
}

// clearStaleSidecarLock ports codex-sidecar.ts:474.
func clearStaleSidecarLock(lockPath string) bool {
	info, err := nodefs.Lstat(lockPath)
	if err != nil {
		if filesystemErrorCode(err) == "ENOENT" {
			return true
		}
		jsexc.Throw(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		fail("FaultLine sidecar lock must be a regular file, not a symlink or directory.")
	}
	if float64(clock.NowMillis())-nodefs.MtimeMs(info) <= staleLockMs {
		return false
	}
	if err := nodefs.Unlink(lockPath); err != nil {
		if filesystemErrorCode(err) == "ENOENT" {
			return true
		}
		fail(jsexc.Concat("FaultLine could not recover a stale sidecar lock: ", jsexc.Message(err))) // coverage gate: race only (the stale lock was lstat'ed in a writable recordings directory just before)
	}
	return true
}

// withSidecarLock ports codex-sidecar.ts:496: it serializes sidecar
// transactions with bounded stale-lock recovery. In `finally` the
// descriptor is closed (a close error replaces the result) and the lock
// unlinked (errors ignored).
func withSidecarLock(ledgerPath string, action func() Result) Result {
	assertSafeDirectory(nodefs.Dirname(ledgerPath), "FaultLine sidecar recordings")
	lockPath := sidecarLockPath(ledgerPath)
	var descriptor *os.File
	deadline := clock.NowMillis() + lockWaitMs
	for descriptor == nil {
		f, err := nodefs.OpenExclusive(lockPath, 0o600)
		if err == nil {
			descriptor = f
			break
		}
		if filesystemErrorCode(err) != "EEXIST" {
			jsexc.Throw(err)
		}
		if clearStaleSidecarLock(lockPath) {
			continue
		}
		if clock.NowMillis() >= deadline {
			fail("Another FaultLine sidecar event is still recording for this Codex session; inspect fl codex sidecar status after the current turn.")
		}
		waitForSidecarLock(min(lockRetryMs, max(1, deadline-clock.NowMillis())))
	}
	var result Result
	actionErr := jsexc.Try(func() { result = action() })
	if closeErr := nodefs.Close(descriptor); closeErr != nil {
		jsexc.Throw(closeErr)
	}
	_ = nodefs.Unlink(lockPath) // A durable ledger update remains valid if lock cleanup is interrupted.
	if actionErr != nil {
		jsexc.Throw(actionErr)
	}
	return result
}
