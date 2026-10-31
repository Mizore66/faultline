//go:build linux && globalopen_hooks

package main

// Lifecycle regression probes for the globalopen helper (test-only). Build tag
// globalopen_hooks selects the instrumentation in hooks_regression.go; the
// default helper contains none of it.
//
// Each test runs the helper's REAL run() in this test process (so its real
// loop, onStop, syscallStop, exit and drain execute), against a re-exec of this
// same test binary as the traced program (environment LR_MODE selects the
// behavior). Only the ORDER of real kernel wait statuses is scripted, through
// rawWaitHook; there is no sleep, load, yield or timing alignment anywhere.
// Thread locking appears only in the traced fixture programs below (never in
// FaultLine). A test that cannot set up its race reports "INVALID PROBE" and is
// neither RED nor GREEN. Safety rules: killHook is a tripwire (the helper sends
// no signal by number), group pidfd signals are recorded.

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

const (
	envMode    = "LR_MODE"
	envTarget  = "LR_TARGET"
	envVariant = "LR_VARIANT"

	atFDCWDRaw = ^uintptr(99) // -100 as a register value
)

type exitPanic int

func TestMain(m *testing.M) {
	switch os.Getenv(envMode) {
	case "b1":
		childB1()
	case "esrch":
		childEsrch()
	case "paired":
		childPaired(os.Getenv(envTarget))
	case "resid":
		childResid(os.Getenv(envTarget), os.Getenv(envVariant))
	case "nlexec":
		childNlexec()
	case "logfail":
		childLogfail()
	case "flags":
		childFlags(os.Getenv(envTarget))
	default:
		os.Exit(m.Run())
	}
	os.Exit(0)
}

// ---- traced fixture programs (run in the re-exec'd test binary) ----

func setCommName(name string) {
	b := append([]byte(name), 0)
	syscall.RawSyscall(syscall.SYS_PRCTL, 15 /* PR_SET_NAME */, uintptr(unsafe.Pointer(&b[0])), 0)
}

func rawGetpid() { syscall.RawSyscall(syscall.SYS_GETPID, 0, 0, 0) }

// sigsuspendForever blocks the calling thread in the kernel (an empty mask,
// 8-byte kernel sigset), and does not look like a Go deadlock.
func sigsuspendForever() {
	var mask [8]byte
	for {
		syscall.Syscall(syscall.SYS_RT_SIGSUSPEND, uintptr(unsafe.Pointer(&mask[0])), 8, 0)
	}
}

// childB1: one child process that execs and exits on its own.
func childB1() {
	path, err := exec.LookPath("true")
	if err != nil {
		os.Exit(97)
	}
	if err := exec.Command(path).Run(); err != nil {
		os.Exit(98)
	}
	os.Exit(0)
}

// childEsrch: thread A stops at getpid and is held by the tracer; thread B is
// parked at its own getpid until A is held, then calls exit_group(7).
func childEsrch() {
	go func() {
		runtime.LockOSThread() // fixture only
		setCommName("esrchA")
		rawGetpid()
		sigsuspendForever()
	}()
	go func() {
		runtime.LockOSThread() // fixture only
		setCommName("esrchB")
		rawGetpid()
		syscall.RawSyscall(syscall.SYS_EXIT_GROUP, 7, 0, 0)
	}()
	select {}
}

// childPaired: thread X opens the target (the helper injects, so X reaches
// the paired syscall-exit stop); thread K is parked at getpid until X's exit
// stop is held, then calls exit_group(9).
func childPaired(target string) {
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

// ---- scripted wait ordering ----

type stash struct {
	tid int
	ws  syscall.WaitStatus
}

func readComm(tid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(tid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// scriptConsumed records every tid whose exit status the SCRIPT itself consumed
// (an exact-tid wait4 that returned an exit instead of a stop, which can
// happen if the status changes between the WNOWAIT peek and the wait4). Any
// entry makes the probe INVALID: the script must never take the helper's exit.
var scriptConsumed = map[int]bool{}

// driveToDeath resumes a stopped tracee and consumes only its STOP statuses
// (with wait4 on that exact tid) until its exit is observed PENDING through
// waitid(WNOWAIT), then returns without having consumed it, so the helper's
// own loop later reaps it. The WNOWAIT observation is a peek, not a guarantee:
// if the later wait4 does return an exit, it is recorded in scriptConsumed and
// reapedTids and the probe is INVALID. Used by the scripts, not by the helper.
func driveToDeath(tid int) error {
	if err := syscall.PtraceSyscall(tid, 0); err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	for {
		var si [128]byte
		const wexited, wstopped, wnowait, wall = 4, 2, 0x1000000, 0x40000000
		_, _, e := syscall.Syscall6(syscall.SYS_WAITID, 1 /* P_PID */, uintptr(tid), uintptr(unsafe.Pointer(&si[0])), uintptr(wexited|wstopped|wnowait|wall), 0, 0)
		if e == syscall.EINTR {
			continue
		}
		if e != 0 {
			return fmt.Errorf("waitid: %w", e)
		}
		if code := int32(binary.NativeEndian.Uint32(si[8:])); code >= 1 && code <= 3 {
			return nil // CLD_EXITED/KILLED/DUMPED, left unconsumed
		}
		var ws syscall.WaitStatus
		if _, err := syscall.Wait4(tid, &ws, syscall.WALL, nil); err != nil {
			return fmt.Errorf("wait4 stop: %w", err)
		}
		if !ws.Stopped() {
			scriptConsumed[tid] = true
			reapedTids[tid] = true
			return fmt.Errorf("the script's exact-tid wait4 consumed exit status %#x of tid %d (INVALID PROBE)", uint32(ws), tid)
		}
		sig := 0
		if sc := ws.StopSignal(); sc != sigSyscall && sc != syscall.SIGTRAP {
			sig = int(sc)
		}
		if err := syscall.PtraceSyscall(tid, sig); err != nil && err != syscall.ESRCH {
			return fmt.Errorf("resume: %w", err)
		}
	}
}

// holdScript models the real race window faithfully: the helper receives the
// held thread's stop from wait4 and issues its first request IMMEDIATELY,
// after another thread of the same process has already exited the group.
type holdScript struct {
	holdComm, killComm string
	holdMatch          func(syscallInfo) bool
	held, parked       *stash
	fired              bool
	events             []string
}

func (s *holdScript) wait(ws *syscall.WaitStatus) (int, error) {
	for {
		tid, err := realWait4(ws)
		if err != nil {
			return tid, err
		}
		st := *ws
		if !s.fired && st.Stopped() && st.StopSignal() == sigSyscall {
			stashed := false
			switch comm := readComm(tid); {
			case comm == s.holdComm && s.held == nil:
				if info, e := getSyscallInfo(tid); e == nil && s.holdMatch(info) {
					s.held, stashed = &stash{tid, st}, true
					s.events = append(s.events, fmt.Sprintf("hold tid=%d comm=%s", tid, comm))
				}
			case comm == s.killComm && s.parked == nil:
				if info, e := getSyscallInfo(tid); e == nil && info.op == infoEntry && info.nr == uint64(syscall.SYS_GETPID) {
					s.parked, stashed = &stash{tid, st}, true
					s.events = append(s.events, fmt.Sprintf("park tid=%d comm=%s", tid, comm))
				}
			}
			if s.held != nil && s.parked != nil {
				s.fired = true
				s.events = append(s.events, "kill-sequence-begin")
				if err := driveToDeath(s.parked.tid); err != nil {
					s.events = append(s.events, "kill-sequence-error: "+err.Error())
				} else {
					s.events = append(s.events, fmt.Sprintf("killer-exit-pending-observed-by-waitid-nowait tid=%d", s.parked.tid))
				}
				s.events = append(s.events, fmt.Sprintf("deliver-held tid=%d", s.held.tid))
				*ws = s.held.ws
				return s.held.tid, nil
			}
			if stashed {
				continue
			}
		}
		return tid, nil
	}
}

func (s *holdScript) complete() bool {
	for _, w := range []string{"hold ", "park ", "kill-sequence-begin", "killer-exit-pending-observed-by-waitid-nowait", "deliver-held"} {
		found := false
		for _, e := range s.events {
			if strings.HasPrefix(e, w) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// b1Script withholds the first fork/vfork event of the traced program until
// its child's exit status has been handed to (and processed by) the helper's
// loop, so the child is reaped BEFORE the parent's event is handled. The real
// kernel's newest-first stop scan can produce that order; here it is forced.
type b1Script struct {
	held           *stash
	childPid       int
	childDelivered bool
	done           bool
	events         []string
}

func (s *b1Script) wait(ws *syscall.WaitStatus) (int, error) {
	if s.held != nil && s.childDelivered && !s.done {
		s.done = true
		s.events = append(s.events, fmt.Sprintf("deliver-held-event parent=%d child=%d", s.held.tid, s.childPid))
		*ws = s.held.ws
		return s.held.tid, nil
	}
	for {
		tid, err := realWait4(ws)
		if err != nil {
			return tid, err
		}
		st := *ws
		if s.held == nil && !s.done && st.Stopped() && st.StopSignal() == syscall.SIGTRAP {
			if c := st.TrapCause(); c == evFork || c == evVfork {
				msg, e := syscall.PtraceGetEventMsg(tid)
				if e != nil {
					return tid, e
				}
				s.held, s.childPid = &stash{tid, st}, int(msg)
				s.events = append(s.events, fmt.Sprintf("hold-event parent=%d child=%d", tid, s.childPid))
				continue
			}
		}
		if s.held != nil && !s.childDelivered && tid == s.childPid && (st.Exited() || st.Signaled()) {
			s.childDelivered = true
			s.events = append(s.events, fmt.Sprintf("child-exit-delivered tid=%d", tid))
		}
		return tid, nil
	}
}

func (s *b1Script) complete() bool {
	want := []string{"hold-event", "child-exit-delivered", "deliver-held-event"}
	i := 0
	for _, e := range s.events {
		if i < len(want) && strings.HasPrefix(e, want[i]) {
			i++
		}
	}
	return i == len(want)
}

// afterScript parks the killer thread K (at its getpid entry) and gates X's
// openat entry stop until K is parked; then, at a helper seam, K exits the
// group (the helper's NEXT request on X sees a tracee that is no longer in a
// ptrace stop).
type afterScript struct {
	parked, gated *stash
	fired         bool
	events        []string
}

func (s *afterScript) wait(ws *syscall.WaitStatus) (int, error) {
	if s.parked != nil && s.gated != nil && s.gated.tid != 0 {
		g := s.gated
		s.gated = &stash{} // delivered; never again
		s.events = append(s.events, fmt.Sprintf("deliver-gated-entry tid=%d", g.tid))
		*ws = g.ws
		return g.tid, nil
	}
	for {
		tid, err := realWait4(ws)
		if err != nil {
			return tid, err
		}
		st := *ws
		if st.Stopped() && st.StopSignal() == sigSyscall {
			switch readComm(tid) {
			case "injK":
				if s.parked == nil {
					if info, e := getSyscallInfo(tid); e == nil && info.op == infoEntry && info.nr == uint64(syscall.SYS_GETPID) {
						s.parked = &stash{tid, st}
						s.events = append(s.events, fmt.Sprintf("park tid=%d", tid))
						if s.gated != nil && s.gated.tid != 0 {
							g := s.gated
							s.gated = &stash{}
							s.events = append(s.events, fmt.Sprintf("deliver-gated-entry tid=%d", g.tid))
							*ws = g.ws
							return g.tid, nil
						}
						continue
					}
				}
			case "injX":
				if s.parked == nil && s.gated == nil {
					if info, e := getSyscallInfo(tid); e == nil && info.op == infoEntry && (info.nr == sysOpenat || info.nr == sysOpenat2) {
						s.gated = &stash{tid, st}
						s.events = append(s.events, fmt.Sprintf("gate-entry tid=%d", tid))
						continue
					}
				}
			}
		}
		return tid, nil
	}
}

func (s *afterScript) after(tid int, info syscallInfo) {
	if s.fired || s.parked == nil || readComm(tid) != "injX" || info.op != infoExit || !info.isError || info.rval != -int64(syscall.ENOSYS) {
		return
	}
	s.kill(tid, "x-exit-info-read")
}

// kill drives the parked killer thread to death at the current helper point.
func (s *afterScript) kill(tid int, label string) {
	s.fired = true
	s.events = append(s.events, fmt.Sprintf("%s tid=%d", label, tid))
	if err := driveToDeath(s.parked.tid); err != nil {
		s.events = append(s.events, "kill-sequence-error: "+err.Error())
		return
	}
	s.events = append(s.events, fmt.Sprintf("killer-exit-pending-observed-by-waitid-nowait tid=%d", s.parked.tid))
}

func (s *afterScript) complete() bool {
	for _, w := range []string{"park ", "x-exit-info-read", "killer-exit-pending-observed-by-waitid-nowait"} {
		found := false
		for _, e := range s.events {
			if strings.HasPrefix(e, w) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ---- running the helper ----

type helperRun struct {
	code           int
	exited         bool // the helper's exit path was taken (fatal or log failure)
	exitCode       int
	log            string
	killAttempts   []int // ANY numeric kill call: always a defect of the helper
	killViolations []int // tids the helper tried to signal after their exit status was consumed
	groupSignals   []int // pidfd group signals (identity-safe), recorded not blocked
}

func callRun() (r helperRun) {
	defer func() {
		if p := recover(); p != nil {
			e, ok := p.(exitPanic)
			if !ok {
				panic(p)
			}
			r.exited, r.exitCode = true, int(e)
		}
	}()
	r.code = run()
	return
}

// residBefore is set by a residual test (other file) for the suppress seam.
var residBefore func(tid int)

// residSignalErr is set by a residual test to make the group signal fail
// (nothing is sent).
var residSignalErr func(fd, tgid int) error

func runHelper(t *testing.T, mode, target string, wait func(*syscall.WaitStatus) (int, error), after ...func(int, syscallInfo)) helperRun {
	var afterFn func(int, syscallInfo)
	if len(after) > 0 {
		afterFn = after[0]
	}
	return runHelperWith(t, mode, target, "", wait, afterFn)
}

func runHelperWith(t *testing.T, mode, target, variant string, wait func(*syscall.WaitStatus) (int, error), after func(int, syscallInfo)) helperRun {
	t.Helper()
	// Intentionally never unlocked: if this goroutine ends locked, its thread
	// ends too, and PTRACE_O_EXITKILL kills any tracee the helper left behind.
	runtime.LockOSThread()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "trace")
	t.Setenv(envMode, mode)
	t.Setenv(envTarget, target)
	t.Setenv(envVariant, variant)
	t.Setenv("GOMAXPROCS", "4")

	oldArgs, oldFlags := os.Args, flag.CommandLine
	os.Args = []string{"globalopen", "-errno", "ENOSPC", "-path", target, "-log", logPath, "--", self}
	flag.CommandLine = flag.NewFlagSet("globalopen", flag.PanicOnError)

	reapedTids = map[int]bool{}
	scriptConsumed = map[int]bool{}
	var attempts, violations, groups []int
	rawWaitHook = wait
	afterInfoHook = after
	beforeSuppressHook = residBefore
	signalErrHook = residSignalErr
	// SAFETY INSTRUMENTATION: the helper must send NO signal by number.
	killHook = func(tid int) {
		attempts = append(attempts, tid)
		if reapedTids[tid] {
			violations = append(violations, tid)
		}
	}
	signalGroupHook = func(fd, tgid int) { groups = append(groups, tgid) }
	exitHookFn = func(code int) { panic(exitPanic(code)) }
	defer func() {
		os.Args, flag.CommandLine = oldArgs, oldFlags
		rawWaitHook, killHook, exitHookFn, afterInfoHook, beforeSuppressHook, signalGroupHook, signalErrHook = nil, nil, nil, nil, nil, nil, nil
		afterLogOpenHook, beforeLogCloseHook = nil, nil
	}()

	r := callRun()
	b, _ := os.ReadFile(logPath)
	r.log = string(b)
	r.killAttempts, r.killViolations, r.groupSignals = attempts, violations, groups
	return r
}

func recordOf(log, prefix string) map[string]string {
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "GLOBALOPEN "+prefix) {
			m := map[string]string{}
			for _, f := range strings.Fields(line) {
				if k, v, ok := strings.Cut(f, "="); ok {
					m[k] = v
				}
			}
			return m
		}
	}
	return nil
}

func hasFatal(log string) string {
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "GLOBALOPEN FATAL") {
			return line
		}
	}
	return ""
}

// checkConsistency: counters, ENTRY/EXIT records, the marker count, the
// terminal EVENT records and SUMMARY are derived from each other, never merely
// asserted next to each other.
func checkConsistency(t *testing.T, r helperRun) {
	t.Helper()
	h := recordOf(r.log, "HEALTH")
	if h == nil {
		return
	}
	atoi := func(k string) int {
		v, err := strconv.Atoi(h[k])
		if err != nil {
			t.Errorf("HEALTH %s=%q is not a number: %v", k, h[k], err)
		}
		return v
	}
	entries := strings.Count(r.log, "\nGLOBALOPEN ENTRY ") + btoi(strings.HasPrefix(r.log, "GLOBALOPEN ENTRY "))
	if got := atoi("candidates"); got != entries {
		t.Errorf("HEALTH candidates=%d but %d ENTRY records", got, entries)
	}
	if got, n := atoi("injected"), strings.Count(r.log, "(INJECTED)"); got != n {
		t.Errorf("HEALTH injected=%d but %d (INJECTED) markers", got, n)
	}
	if got := atoi("injected_started") - atoi("injected") - atoi("injection_lost"); got != 0 {
		t.Errorf("injected_started - injected - injection_lost = %d, want 0", got)
	}
	if rc := atoi("reaped"); rc != strings.Count(r.log, "EVENT kind=exit tid=")+strings.Count(r.log, "EVENT kind=exit-unannounced tid=") {
		t.Errorf("HEALTH reaped=%d disagrees with the exit EVENT records", rc)
	}
	if h["spawned"] != h["reaped"] {
		t.Errorf("spawned=%s reaped=%s", h["spawned"], h["reaped"])
	}
	if len(r.killAttempts) != 0 {
		t.Errorf("OWNERSHIP VIOLATION: the helper sent/attempted numeric kill(s) of tid(s) %v (reaped: %v)", r.killAttempts, r.killViolations)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- the four binding regressions ----

// B1: no phantom, no fatal, spawned == reaped, no numeric signal.
func TestRegressionB1LateParentEventAfterChildReaped(t *testing.T) {
	s := &b1Script{}
	target := filepath.Join(t.TempDir(), "never-opened")
	r := runHelper(t, "b1", target, s.wait)
	if !s.complete() {
		t.Fatalf("INVALID PROBE: the event ordering was not forced; script events %q\nlog:\n%s", s.events, r.log)
	}
	if len(r.killViolations) != 0 {
		t.Errorf("OWNERSHIP VIOLATION (B1): the helper tried to signal tid(s) %v whose exit status it had already consumed (script events %q)", r.killViolations, s.events)
	}
	if f := hasFatal(r.log); f != "" || r.exited {
		t.Errorf("the helper took its fatal/exit path (hook exit=%t code=%d): %s", r.exited, r.exitCode, f)
	}
	h, sum := recordOf(r.log, "HEALTH"), recordOf(r.log, "SUMMARY")
	if h == nil || sum == nil {
		t.Fatalf("no HEALTH/SUMMARY in the log:\n%s", r.log)
	}
	if h["ok"] != "true" || h["spawned"] != h["reaped"] {
		t.Errorf("HEALTH not clean: %v", h)
	}
	if h["pidfd_fail"] != "0" || h["group_unknown"] != "0" {
		t.Errorf("pidfd_fail=%s group_unknown=%s, want 0 and 0: every non-leader must be accepted only by a consistent Tgid lookup: %v", h["pidfd_fail"], h["group_unknown"], h)
	}
	if sum["root_status"] != "0" || sum["root_signaled"] != "false" {
		t.Errorf("SUMMARY %v, want root_status=0 root_signaled=false", sum)
	}
	if !strings.Contains(r.log, "outcome=matched-reaped") {
		t.Errorf("the late parent event was not reconciled against the reaped child (no outcome=matched-reaped record)")
	}
	checkConsistency(t, r)
}

// ESRCH on a REPORTED syscall stop. HEALTH must be ok=false: the helper never
// read the op of the abandoned stop, so an unknown lost stop before any
// proven injection cannot be called a known non-open stop. Reconciled by the
// terminal wait status; no fatal; every tracee accounted.
const wantEsrchProbeHealthy = false

func TestRegressionESRCHStopAfterNonleaderExitGroup(t *testing.T) {
	s := &holdScript{
		holdComm: "esrchA", killComm: "esrchB",
		holdMatch: func(i syscallInfo) bool { return i.op == infoEntry && i.nr == uint64(syscall.SYS_GETPID) },
	}
	r := runHelper(t, "esrch", filepath.Join(t.TempDir(), "never-opened"), s.wait)
	if !s.complete() {
		t.Fatalf("INVALID PROBE: the race was not set up; script events %q\nlog:\n%s", s.events, r.log)
	}
	if f := hasFatal(r.log); f != "" || r.exited {
		t.Errorf("the helper took its fatal path on a lost stop (hook exit=%t code=%d): %s", r.exited, r.exitCode, f)
	}
	h, sum := recordOf(r.log, "HEALTH"), recordOf(r.log, "SUMMARY")
	if h == nil || sum == nil {
		t.Fatalf("no HEALTH/SUMMARY in the log (script events %q):\n%s", s.events, r.log)
	}
	if sum["root_status"] != "7" || sum["root_signaled"] != "false" {
		t.Errorf("SUMMARY %v, want root_status=7 root_signaled=false", sum)
	}
	if h["spawned"] != h["reaped"] {
		t.Errorf("tracees not all reaped: %v", h)
	}
	if got := h["ok"] == "true"; got != wantEsrchProbeHealthy {
		t.Errorf("HEALTH ok=%q, want healthy=%t for an unknown lost stop before any injection: %v", h["ok"], wantEsrchProbeHealthy, h)
	}
	if h["lost_before_injection"] != "1" || h["reconciled"] != "1" {
		t.Errorf("lost_before_injection=%s reconciled=%s, want 1 and 1 (the lost stop must be reconciled by its terminal wait): %v", h["lost_before_injection"], h["reconciled"], h)
	}
	if len(r.killViolations) != 0 {
		t.Errorf("OWNERSHIP VIOLATION: signalled consumed tid(s) %v", r.killViolations)
	}
	checkConsistency(t, r)
}

// Injected call whose tracee dies AFTER the helper read the paired exit stop
// (GET_SYSCALL_INFO succeeded) and BEFORE setReturnErrno: no fatal, ok=false,
// zero markers (the errno never reached the writer), the token stays consumed.
func TestRegressionInjectedSetReturnESRCH(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &afterScript{}
	r := runHelper(t, "paired", target, s.wait, s.after)
	if !s.complete() {
		t.Fatalf("INVALID PROBE: the race was not set up; script events %q\nlog:\n%s", s.events, r.log)
	}
	if f := hasFatal(r.log); f != "" || r.exited {
		t.Errorf("the helper took its fatal path on ESRCH at setReturnErrno (hook exit=%t code=%d): %s", r.exited, r.exitCode, f)
	}
	h := recordOf(r.log, "HEALTH")
	if h == nil || recordOf(r.log, "SUMMARY") == nil {
		t.Fatalf("no HEALTH/SUMMARY in the log (script events %q):\n%s", s.events, r.log)
	}
	if h["ok"] != "false" {
		t.Errorf("HEALTH ok=%q, want false (injection lost before delivery): %v", h["ok"], h)
	}
	if n := strings.Count(r.log, "(INJECTED)"); n != 0 {
		t.Errorf("(INJECTED) occurs %d times; an undelivered injection must leave no marker", n)
	}
	if n := strings.Count(r.log, "decision=inject"); n != 1 {
		t.Errorf("%d ENTRY decision=inject records, want exactly 1 (token not reused)", n)
	}
	if h["injection_lost"] != "1" || h["injected"] != "0" || h["injected_started"] != "1" {
		t.Errorf("injection counters %v, want started=1 lost=1 delivered=0", h)
	}
	if len(r.killViolations) != 0 {
		t.Errorf("OWNERSHIP VIOLATION: signalled consumed tid(s) %v", r.killViolations)
	}
	checkConsistency(t, r)
}

// Missing injected paired exit: the stop is held and the group exits; the
// helper's first request on it is ESRCH.
func TestRegressionInjectedPairedExitLost(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &holdScript{
		holdComm: "injX", killComm: "injK",
		holdMatch: func(i syscallInfo) bool {
			return i.op == infoExit && i.isError && i.rval == -int64(syscall.ENOSYS)
		},
	}
	r := runHelper(t, "paired", target, s.wait)
	if !s.complete() {
		t.Fatalf("INVALID PROBE: the race was not set up; script events %q\nlog:\n%s", s.events, r.log)
	}
	if f := hasFatal(r.log); f != "" || r.exited {
		t.Errorf("the helper took its fatal path on a lost paired exit (hook exit=%t code=%d): %s", r.exited, r.exitCode, f)
	}
	h, sum := recordOf(r.log, "HEALTH"), recordOf(r.log, "SUMMARY")
	if h == nil || sum == nil {
		t.Fatalf("no HEALTH/SUMMARY in the log (script events %q):\n%s", s.events, r.log)
	}
	if h["ok"] != "false" {
		t.Errorf("HEALTH ok=%q, want false (injection lost before delivery): %v", h["ok"], h)
	}
	if n := strings.Count(r.log, "(INJECTED)"); n != 0 {
		t.Errorf("(INJECTED) occurs %d times; an undelivered injection must leave no marker", n)
	}
	if n := strings.Count(r.log, "decision=inject"); n != 1 {
		t.Errorf("%d ENTRY decision=inject records, want exactly 1 (no token reuse)", n)
	}
	if h["injection_lost"] != "1" || h["orphan_entries"] != "1" || h["lost_pending"] != "1" {
		t.Errorf("a lost pending injection must be counted as injection_lost, orphan_entries and lost_pending: %v", h)
	}
	if sum["root_status"] != "9" {
		t.Errorf("SUMMARY %v, want root_status=9", sum)
	}
	if len(r.killViolations) != 0 {
		t.Errorf("OWNERSHIP VIOLATION: signalled consumed tid(s) %v", r.killViolations)
	}
	checkConsistency(t, r)
}
