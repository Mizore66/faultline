//go:build linux && globalopen_hooks && nonleader_exec_staged

package main

// Non-leader execve accounting regressions. Compiled only with -tags
// 'globalopen_hooks nonleader_exec_staged'. A non-leader exec is an
// UNSUPPORTED case that the helper reconciles without a phantom but reports as
// HEALTH false; these tests pin that behavior.
//
// When a NON-LEADER thread execs, the kernel gives the execing thread the old
// leader's tid and delivers NO terminal wait status for the execing thread's
// former tid. Requiring spawned == reaped would demand a terminal wait that
// never comes and must not be fabricated. The required relation is
//
//	spawned == reaped + exec_retired
//
// with the number of terminal `EVENT kind=exit` lines remaining exactly
// `reaped` (a retirement is NOT a terminal line), and an explicit retirement
// record carrying the former tid, the new tid, the retired generation and the
// ACTUAL PTRACE_GETEVENTMSG value:
//
//	GLOBALOPEN EVENT kind=exec-retired former=<F> new=<X> gen=<G> eventmsg=<M>
//
// The test reads the same GETEVENTMSG itself, at the exec event stop, as an
// independent expectation. HEALTH stays ok=false. No `group_end_retired`
// inference is tested or implied.
//
// Event control, no sleep/load/yield: the traced program locks its main
// goroutine to the main OS thread (the group leader), and a second goroutine,
// locked to another OS thread and named nlexN, calls execve("true"). The
// non-leadership of the exec thread is confirmed from /proc (Tgid != tid) or
// the probe is INVALID.

import (
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

// childNlexec: variant "" -> the leader only sleeps in the kernel; variant
// "pending" -> the leader first makes one openat on the target (the helper
// injects it, so the leader reaches its paired exit stop, which the script
// holds while the other thread execs).
func childNlexec() {
	variant, target := os.Getenv(envVariant), os.Getenv(envTarget)
	truePath, err := exec.LookPath("true")
	if err != nil {
		os.Exit(97)
	}
	go func() {
		runtime.LockOSThread() // fixture only
		setCommName("nlexN")
		rawGetpid()
		syscall.Exec(truePath, []string{truePath}, os.Environ()) // does not return on success
		os.Exit(98)
	}()
	runtime.LockOSThread() // fixture only: this goroutine stays on the main thread
	if variant == "pending" {
		setCommName("nlexL")
		p, _ := syscall.BytePtrFromString(target)
		fd, _, e := syscall.Syscall6(syscall.SYS_OPENAT, atFDCWDRaw, uintptr(unsafe.Pointer(p)), uintptr(syscall.O_RDONLY|syscall.O_CLOEXEC), 0, 0, 0)
		if e == 0 {
			syscall.Close(int(fd))
		}
	}
	sigsuspendForever()
}

type nlexecScript struct {
	variant string
	events  []string

	tid, tgid   int // the exec thread (former tid) and its group (the surviving tid)
	nonleader   bool
	sawExecStop bool

	eventMsg    uint // the ACTUAL PTRACE_GETEVENTMSG read at the exec stop (syscall returns uint)
	eventMsgErr error

	pidfd    int   // an independent group pidfd opened before the exec (test-owned)
	pidfdErr error // signal-0 result through it after the real exec (nil = still valid)
	pidfdSig bool

	leaderTid  int
	leaderHeld bool
	gated      *stash
}

func (s *nlexecScript) wait(ws *syscall.WaitStatus) (int, error) {
	if s.gated != nil && s.leaderHeld {
		g := s.gated
		s.gated = nil
		s.events = append(s.events, "release-gated-exec-thread tid="+strconv.Itoa(g.tid))
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
			case "nlexN":
				if s.tid == 0 {
					s.tid = tid
					if tgid, e := readTgid(tid); e == nil {
						s.tgid = tgid
						s.nonleader = tgid != tid
						// Independent pidfd of the group while its leader is an
						// unreaped tracee (test-owned; never used to signal).
						if fd, e := pidfdOpen(tgid); e == nil {
							s.pidfd = fd
						} else {
							s.pidfdErr = e
							s.pidfd = -1
						}
					}
					s.events = append(s.events, "exec-thread-seen tid="+strconv.Itoa(tid)+" tgid="+strconv.Itoa(s.tgid))
					if s.variant == "pending" && !s.leaderHeld {
						s.gated = &stash{tid, st}
						s.events = append(s.events, "gate-exec-thread tid="+strconv.Itoa(tid))
						continue
					}
				}
			case "nlexL":
				if s.variant == "pending" && !s.leaderHeld {
					if info, e := getSyscallInfo(tid); e == nil && info.op == infoExit && info.isError && info.rval == -int64(syscall.ENOSYS) {
						// The helper has injected the leader's openat and this is its
						// paired exit stop: hold it (never delivered; the exec kills it).
						s.leaderHeld, s.leaderTid = true, tid
						s.events = append(s.events, "hold-leader-paired-exit tid="+strconv.Itoa(tid))
						if s.gated != nil {
							g := s.gated
							s.gated = nil
							s.events = append(s.events, "release-gated-exec-thread tid="+strconv.Itoa(g.tid))
							*ws = g.ws
							return g.tid, nil
						}
						continue
					}
				}
			}
		}
		if st.Stopped() && st.StopSignal() == syscall.SIGTRAP && st.TrapCause() == evExec {
			s.sawExecStop = true
			msg, e := syscall.PtraceGetEventMsg(tid) // (uint, error)
			s.eventMsg, s.eventMsgErr = msg, e
			s.events = append(s.events, "exec-event-stop tid="+strconv.Itoa(tid)+" eventmsg="+strconv.FormatUint(uint64(msg), 10))
			if s.pidfd > 0 {
				s.pidfdErr = pidfdSendSignal(s.pidfd, 0) // signal 0: permission check only, nothing delivered
				s.pidfdSig = true
				s.events = append(s.events, "pidfd-signal0-after-exec err="+strconv.Quote(errString(s.pidfdErr)))
			}
		}
		return tid, nil
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// checkPidfdControl asserts the independent kernel-model control: a group
// pidfd opened by the TEST before the exec must still name the surviving
// process afterwards (signal 0 succeeds). It is helper-independent and is
// called BEFORE execAccounting, whose Fatalf on a failing accounting would
// otherwise stop the test before this control is asserted. It is skipped only
// when the probe is already INVALID (no non-leader shown), which execAccounting
// then reports.
func checkPidfdControl(t *testing.T, s *nlexecScript) {
	t.Helper()
	if !s.nonleader {
		return
	}
	if !s.pidfdSig {
		t.Errorf("INVALID PROBE: the pidfd signal-0 control did not run (pidfd err %v)", s.pidfdErr)
	} else if s.pidfdErr != nil {
		t.Errorf("a group pidfd opened before the non-leader exec failed signal 0 after it: %v (the stable-group-ownership model is wrong)", s.pidfdErr)
	}
}

// execAccounting asserts the ruled accounting and the retirement evidence.
func execAccounting(t *testing.T, s *nlexecScript, r helperRun) (h map[string]string) {
	t.Helper()
	t.Logf("script events: %q", s.events)
	t.Logf("full helper log (observation for the controller):\n%s", r.log)
	if !s.nonleader {
		t.Fatalf("INVALID PROBE: the exec thread was not shown to be a non-leader (tid=%d tgid=%d); events %q", s.tid, s.tgid, s.events)
	}
	if !s.sawExecStop || s.eventMsgErr != nil {
		t.Fatalf("INVALID PROBE: exec event stop %t, GETEVENTMSG err %v; events %q", s.sawExecStop, s.eventMsgErr, s.events)
	}
	if int(s.eventMsg) != s.tid {
		t.Fatalf("INVALID PROBE: the actual GETEVENTMSG %d is not the exec thread's former tid %d (the kernel model in the brief does not hold); events %q", s.eventMsg, s.tid, s.events)
	}
	if f := hasFatal(r.log); f != "" || r.exited {
		t.Errorf("the helper took its fatal path on a non-leader execve (hook exit=%t code=%d): %s", r.exited, r.exitCode, f)
	}
	h = recordOf(r.log, "HEALTH")
	sum := recordOf(r.log, "SUMMARY")
	if h == nil || sum == nil {
		t.Fatalf("no HEALTH/SUMMARY in the log; fatal=%q", hasFatal(r.log))
	}
	if h["ok"] != "false" {
		t.Errorf("HEALTH ok=%q after a non-leader execve; the unsupported case must stay unhealthy: %v", h["ok"], h)
	}
	spawned, e1 := strconv.Atoi(h["spawned"])
	reaped, e2 := strconv.Atoi(h["reaped"])
	retired, e3 := strconv.Atoi(h["exec_retired"])
	if e1 != nil || e2 != nil {
		t.Fatalf("HEALTH spawned/reaped not numbers: %v", h)
	}
	if e3 != nil || retired < 1 {
		t.Errorf("exec_retired=%q: a real non-leader exec must be accounted as a positive exec_retired: %v", h["exec_retired"], h)
	}
	if spawned != reaped+retired {
		t.Errorf("accounting: spawned %d != reaped %d + exec_retired %d", spawned, reaped, retired)
	}
	if terminal := strings.Count(r.log, "EVENT kind=exit tid=") + strings.Count(r.log, "EVENT kind=exit-unannounced tid="); terminal != reaped {
		t.Errorf("terminal exit EVENT lines %d != reaped %d: a retirement must never be logged as a terminal exit", terminal, reaped)
	}
	if strings.Contains(r.log, "EVENT kind=exit tid="+strconv.Itoa(s.tid)+" ") {
		t.Errorf("a terminal exit line was fabricated for the former tid %d", s.tid)
	}
	var rec map[string]string
	for _, line := range strings.Split(r.log, "\n") {
		if strings.HasPrefix(line, "GLOBALOPEN EVENT kind=exec-retired ") {
			if rec != nil {
				t.Errorf("more than one exec-retired record; the single exec must be retired once")
			}
			rec = map[string]string{}
			for _, f := range strings.Fields(line) {
				if k, v, ok := strings.Cut(f, "="); ok {
					rec[k] = v
				}
			}
		}
	}
	switch {
	case rec == nil:
		t.Errorf("no `kind=exec-retired` record: the retirement evidence is missing")
	default:
		if rec["former"] != strconv.Itoa(s.tid) {
			t.Errorf("retirement former=%q, want the exec thread's tid %d", rec["former"], s.tid)
		}
		if rec["new"] != strconv.Itoa(s.tgid) {
			t.Errorf("retirement new=%q, want the surviving tid %d", rec["new"], s.tgid)
		}
		if rec["eventmsg"] != strconv.FormatUint(uint64(s.eventMsg), 10) {
			t.Errorf("retirement eventmsg=%q, want the actual PTRACE_GETEVENTMSG %d", rec["eventmsg"], s.eventMsg)
		}
		if g, e := strconv.Atoi(rec["gen"]); e != nil || g <= 0 {
			t.Errorf("retirement gen=%q is not a positive generation", rec["gen"])
		}
	}
	if sum["root_status"] != "0" || sum["root_signaled"] != "false" {
		t.Errorf("SUMMARY %v, want the surviving process's real status root_status=0 root_signaled=false", sum)
	}
	if len(r.killAttempts) != 0 {
		t.Errorf("OWNERSHIP VIOLATION: numeric kill attempted: %v", r.killAttempts)
	}
	return h
}

func TestStagedNonleaderExecRetirementAccounting(t *testing.T) {
	s := &nlexecScript{variant: ""}
	defer func() {
		if s.pidfd > 0 {
			syscall.Close(s.pidfd)
		}
	}()
	r := runHelperWith(t, "nlexec", filepath.Join(t.TempDir(), "never-opened"), "", s.wait, nil)
	// The helper-independent control is asserted first: execAccounting's Fatalf
	// must not hide it.
	checkPidfdControl(t, s)
	execAccounting(t, s, r)
}

// A pending injected open on the OLD LEADER when the other thread execs. The
// leader's paired exit stop is held (never delivered: the exec kills it), so
// the helper's ledger keeps pend on the leader's generation. That pending call
// must be accounted as lost, not silently dropped and not reported as delivered.
func TestStagedNonleaderExecPendingInjectionOnOldLeader(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &nlexecScript{variant: "pending"}
	defer func() {
		if s.pidfd > 0 {
			syscall.Close(s.pidfd)
		}
	}()
	r := runHelperWith(t, "nlexec", target, "pending", s.wait, nil)
	if !s.leaderHeld {
		t.Fatalf("INVALID PROBE: the leader's injected paired exit was never held; events %q\nlog:\n%s", s.events, r.log)
	}
	checkPidfdControl(t, s)
	h := execAccounting(t, s, r)
	if n := strings.Count(r.log, "(INJECTED)"); n != 0 {
		t.Errorf("(INJECTED) occurs %d times: the injected call's errno never reached the writer", n)
	}
	if n := strings.Count(r.log, "decision=inject"); n != 1 {
		t.Errorf("%d ENTRY decision=inject records, want exactly 1 (token not reused)", n)
	}
	if h["injected_started"] != "1" || h["injected"] != "0" || h["injection_lost"] != "1" || h["orphan_entries"] != "1" {
		t.Errorf("the pending injection on the old leader must be counted lost (started=1 delivered=0 injection_lost=1 orphan_entries=1): %v", h)
	}
}
