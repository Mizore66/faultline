//go:build linux && globalopen_hooks && log_controls

package main

// EVIDENCE-WRITE, LOG-CLOSE and EXACT FLAGS/MODE controls. Tag set
// 'globalopen_hooks log_controls'. They need NO change to helper logic: the
// failures are made by the OS on real descriptors (no fake success evidence).
// Two empty test seams (afterLogOpen, beforeLogClose) exist only to reach the
// log's descriptor; the default build is unchanged.
//
// Expectations are written against the helper's logic for behavior that exists
// in the code. A FAIL here is a real finding about log-failure cleanup or
// evidence fidelity.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// childLogfail: thread "logfX" makes a getpid (the test's trigger point), then
// one openat on the target (a matching candidate, so the helper must LOG an
// ENTRY, which is the write that fails), then blocks forever. The traced
// process is a live, owned, unreaped tracee throughout.
func childLogfail() {
	target := os.Getenv(envTarget)
	go func() {
		runtime.LockOSThread() // fixture only
		setCommName("logfX")
		rawGetpid()
		p, _ := syscall.BytePtrFromString(target)
		fd, _, e := syscall.Syscall6(syscall.SYS_OPENAT, atFDCWDRaw, uintptr(unsafe.Pointer(p)), uintptr(syscall.O_RDONLY|syscall.O_CLOEXEC), 0, 0, 0)
		if e == 0 {
			syscall.Close(int(fd))
		}
		sigsuspendForever()
	}()
	select {}
}

// childFlags: two real calls with known flags/mode on the target. The first is
// the injected one (openat, O_WRONLY|O_CREAT|O_TRUNC|O_CLOEXEC, mode 0640); the
// second passes (openat2 with a real open_how: O_RDONLY|O_CLOEXEC, mode 0).
func childFlags(target string) {
	runtime.LockOSThread() // fixture only
	p, _ := syscall.BytePtrFromString(target)
	fd, _, e := syscall.Syscall6(syscall.SYS_OPENAT, atFDCWDRaw, uintptr(unsafe.Pointer(p)), uintptr(syscall.O_WRONLY|syscall.O_CREAT|syscall.O_TRUNC|syscall.O_CLOEXEC), 0o640, 0, 0)
	if e == 0 {
		syscall.Close(int(fd))
	}
	how := [3]uint64{uint64(syscall.O_RDONLY | syscall.O_CLOEXEC), 0, 0}
	fd, _, e = syscall.Syscall6(437, atFDCWDRaw, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&how)), 24, 0, 0)
	if e == 0 {
		syscall.Close(int(fd))
	}
	syscall.RawSyscall(syscall.SYS_EXIT_GROUP, 0, 0, 0)
}

// captureStderr swaps os.Stderr for a pipe for the duration of fn (the helper
// reads the variable at each write) and returns what was written.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string)
	go func() {
		b := make([]byte, 0, 4096)
		buf := make([]byte, 4096)
		for {
			n, e := r.Read(buf)
			b = append(b, buf[:n]...)
			if e != nil {
				break
			}
		}
		done <- string(b)
	}()
	func() {
		defer func() { os.Stderr = old }()
		fn()
	}()
	w.Close()
	out := <-done
	r.Close()
	return out
}

// Mid-run evidence-write failure with LIVE, OWNED tracees. At the traced
// thread's getpid stop the test redirects the helper's REAL log descriptor onto
// /dev/full (dup3 over the same fd number), so the very next evidence write
// fails with the OS's ENOSPC. The next write is the ENTRY of the thread's
// matching openat, so a tracee is stopped inside the call and owned.
//
// Binding expectation: the helper takes its log-failure path (exit hook code
// 70), prints a loud stderr message naming the failed write, cleans up through
// the held group pidfd ONLY (no numeric kill), and leaves NO owned tracee
// unreaped (the traced root's exit status is consumed by the helper's drain);
// no SUMMARY was produced (the redirected descriptor could not receive one).
func TestLogControlMidRunWriteFailureWithOwnedCleanup(t *testing.T) {
	devfull, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("/dev/full is unavailable: %v (the control needs a real failing descriptor and must not be skipped)", err)
	}
	defer devfull.Close()
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logFile *os.File
	afterLogOpenHook = func(f *os.File) { logFile = f }
	redirected := false
	after := func(tid int, info syscallInfo) {
		if redirected || logFile == nil || readComm(tid) != "logfX" || info.op != infoEntry || info.nr != uint64(syscall.SYS_GETPID) {
			return
		}
		redirected = true
		if err := rawDup3(int(devfull.Fd()), int(logFile.Fd())); err != nil {
			t.Errorf("INVALID PROBE: dup3 over the log descriptor failed: %v", err)
		}
	}
	var r helperRun
	stderr := captureStderr(t, func() {
		r = runHelperWith(t, "logfail", target, "", nil, after)
	})
	if !redirected {
		t.Fatalf("INVALID PROBE: the log descriptor was never redirected; log:\n%s", r.log)
	}
	if !r.exited || r.exitCode != 70 {
		t.Errorf("the helper did not take its log-failure exit (exited=%t code=%d, run returned %d)", r.exited, r.exitCode, r.code)
	}
	if !strings.Contains(stderr, "log write failed") {
		t.Errorf("no loud stderr message about the failed log write; stderr:\n%s", stderr)
	}
	if len(r.killAttempts) != 0 {
		t.Errorf("OWNERSHIP VIOLATION: numeric kill attempted during the failure cleanup: %v", r.killAttempts)
	}
	if len(r.groupSignals) == 0 {
		t.Errorf("no group pidfd signal was sent: the live owned process was not cleaned up through its held handle")
	}
	root := rootPidOf(r.log)
	if root == 0 {
		t.Fatalf("INVALID PROBE: no TARGET root_pid in the pre-failure log:\n%s", r.log)
	}
	if !reapedTids[root] {
		t.Errorf("the traced root %d was not reaped by the failure cleanup (an owned tracee was left to EXITKILL)", root)
	}
	if recordOf(r.log, "SUMMARY") != nil {
		t.Errorf("a SUMMARY exists although the evidence write failed")
	}
}

// Final log Close failure. Just before the helper's last Close the test closes
// the underlying descriptor behind the os.File, so the helper's Close fails
// with the OS's EBADF. The run is otherwise complete and the evidence (HEALTH,
// SUMMARY) is already written.
//
// Binding expectation: the helper must NOT report success: it returns 70 (the
// tool-failure code), not the traced process's own status (0), and says so on
// stderr. The log content itself is complete (HEALTH and SUMMARY present),
// which is why an integration must treat exit 70 as a helper failure.
func TestLogControlFinalCloseFailure(t *testing.T) {
	target := filepath.Join(t.TempDir(), "never-opened")
	beforeLogCloseHook = func(f *os.File) {
		if err := syscall.Close(int(f.Fd())); err != nil {
			t.Errorf("INVALID PROBE: could not close the log descriptor behind the file: %v", err)
		}
	}
	var r helperRun
	stderr := captureStderr(t, func() {
		r = runHelperWith(t, "b1", target, "", nil, nil)
	})
	if r.exited {
		t.Fatalf("the helper took a fatal path before the final close (code %d); log:\n%s", r.exitCode, r.log)
	}
	if recordOf(r.log, "HEALTH") == nil || recordOf(r.log, "SUMMARY") == nil {
		t.Fatalf("INVALID PROBE: HEALTH/SUMMARY were not written before the close:\n%s", r.log)
	}
	if r.code != 70 {
		t.Errorf("run returned %d after a failed final log Close; want 70 (a close failure must not look like the traced status)", r.code)
	}
	if !strings.Contains(stderr, "closing the log") {
		t.Errorf("no loud stderr message about the failed log close; stderr:\n%s", stderr)
	}
	if len(r.killAttempts) != 0 {
		t.Errorf("OWNERSHIP VIOLATION: numeric kill attempted: %v", r.killAttempts)
	}
}

// Exact flags/mode in the evidence. The helper logs the decoded flags (hex and
// symbolic) and the mode for each candidate; this control pins the exact text
// for one openat and one openat2, with real values issued by the traced program.
// Values are those of both Linux targets (O_CLOEXEC 0x80000, O_CREAT 0x40,
// O_TRUNC 0x200, O_WRONLY 0x1).
func TestFlagsModeEvidenceIsExact(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runHelperWith(t, "flags", target, "", nil, nil)
	if f := hasFatal(r.log); f != "" || r.exited {
		t.Fatalf("fatal path (hook exit=%t): %s\n%s", r.exited, f, r.log)
	}
	var entries []string
	for _, line := range strings.Split(r.log, "\n") {
		if strings.HasPrefix(line, "GLOBALOPEN ENTRY ") {
			entries = append(entries, line)
		}
	}
	if len(entries) != 2 {
		t.Fatalf("%d ENTRY records, want 2:\n%s", len(entries), r.log)
	}
	for i, want := range [][]string{
		{"syscall=openat ", "flags=0x80241:O_WRONLY|O_CREAT|O_TRUNC|O_CLOEXEC ", "mode=0640 ", "decision=inject"},
		{"syscall=openat2 ", "flags=0x80000:O_RDONLY|O_CLOEXEC ", "mode=00 ", "how_size=24 how_ok=true ", "decision=pass"},
	} {
		for _, frag := range want {
			if !strings.Contains(entries[i], frag) {
				t.Errorf("ENTRY %d lacks %q:\n%s", i+1, frag, entries[i])
			}
		}
	}
	h := recordOf(r.log, "HEALTH")
	if h == nil || h["ok"] != "true" || h["injected"] != "1" {
		t.Errorf("HEALTH %v, want ok=true injected=1 for a clean run", h)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("the target should still exist untouched by the suppressed O_TRUNC open: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "x" {
		t.Errorf("the target's content changed to %q: the injected O_TRUNC open ran", b)
	}
	checkConsistency(t, r)
}

// rawDup3 is dup3(oldfd, newfd, 0) through the raw syscall number, which the Go
// syscall package defines on both linux/arm64 and linux/amd64 (syscall.Dup3
// itself exists only on arm64).
func rawDup3(oldfd, newfd int) error {
	if _, _, e := syscall.Syscall(syscall.SYS_DUP3, uintptr(oldfd), uintptr(newfd), 0); e != 0 {
		return e
	}
	return nil
}

func rootPidOf(log string) int {
	m := recordOf(log, "TARGET")
	if m == nil {
		return 0
	}
	n := 0
	for _, c := range m["root_pid"] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
