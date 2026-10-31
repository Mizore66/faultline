//go:build linux

// Command globalopen is a TEST-ONLY ptrace injector for the ordinary first-open
// fault rows of difftest/writerfault_live_test.go. It is not a production
// command, ships in no release and has no flags a user is meant to use. Its
// evidence is labeled GLOBALOPEN and is never stock strace output.
//
// Why it exists: stock strace counts "the first call" per tracee and per
// syscall, so it cannot express "fail exactly ONE open in the whole process
// tree, whichever thread or child makes it". This helper fails exactly one
// call of the ordinary open group (open, openat, openat2) with a configured
// errno: the first matching syscall ENTRY in one global order across every
// tracee (threads and child processes). One tracer, locked to one OS thread,
// handles every stop serially.
//
//	globalopen -errno ENOSPC|EIO|EACCES|ENOENT -path /abs/template -log FILE -- cmd args...
//
// The literal "<PID>" in -path is replaced by the pid of the ROOT tracee (the
// process the tracer fork+execs), right after ForkExec. That is the pid of the
// traced command itself; there is no strace -D pid-preservation claim.
//
// Matching never depends on the case, the errno, an ordinal, a thread or a
// timing. A call whose path cannot be shown to be, or not to be, the target is
// not silently skipped: it is logged REFUSED/UNRESOLVED and the HEALTH line
// reports it. HEALTH is separate from the traced command's own exit status.
//
// Restrictions (explicit, unproven or unsupported):
//   - group-stop (SIGSTOP/SIGTSTP/SIGTTIN/SIGTTOU after a tracee's first stop)
//     is a hard error: without PTRACE_SEIZE it cannot be told from a
//     signal-delivery-stop;
//   - a NON-LEADER thread's execve is reconciled without a phantom (the former
//     generation is retired from the actual PTRACE_GETEVENTMSG) but it is an
//     UNSUPPORTED case: HEALTH is always false after one. A failed execve after
//     the point of no return (no exec event) can still end in the loud ECHILD
//     fatal; no group_end_retired inference exists;
//   - an exec event met while draining is reconciled the same way, but cleanup
//     is never claimed universal;
//   - 32-bit (compat) syscall ABI is a hard error;
//   - aliases other than symlinked directory components are not detected (see
//     resolve in open_linux.go); final-component symlinks are refused;
//   - nothing here claims PID-namespace support: the PTRACE_GETEVENTMSG value
//     and wait4 tids are compared as plain numbers and cross-checked against the
//     held group, never trusted alone;
//   - evidence is not strace output and strace -P quirks are not copied.
//
// Cleanup: stable per-group pidfds only (pidfd_linux.go); no numeric signal,
// no ESRCH-as-death inference, no fabricated wait.
//
// The init() LockOSThread below is REQUIRED tracer thread affinity: every
// ptrace request must come from the tracer's own OS thread. It is distinct
// from the forbidden thread/timing alignment of FaultLine or of a tracee,
// which this helper never performs.

package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	ptraceGetSyscallInfo = 0x420e

	optTraceSysgood = 0x1
	optTraceFork    = 0x2
	optTraceVfork   = 0x4
	optTraceClone   = 0x8
	optTraceExec    = 0x10
	optExitKill     = 0x100000

	evFork  = 1
	evVfork = 2
	evClone = 3
	evExec  = 4

	sigSyscall = syscall.SIGTRAP | 0x80

	infoEntry = 1
	infoExit  = 2

	exitTool = 70
	maxPath  = 4096
)

func init() {
	// Every ptrace request must come from the tracer's OS thread, and the
	// thread that forks the tracee is its tracer.
	runtime.LockOSThread()
}

type pending struct {
	seq    int
	inject bool
}

type health struct {
	candidates, passed, refused, unresolved int
	injectStarted, injectDelivered          int
	injectLost                              int
	unmatched, unreadable                   int
	orphanEntries, unexpectedEvt            int
	lostStops                               int
	lostBeforeInjection, lostAfterInjection int
	lostPending                             int
	contradictions                          int
	eventLost, optionsLost                  int
	groupUnknown, groupConflict, pidfdFail  int
	closeFail                               int
	reconciled, resumeLost                  int
	peekLost, howUnknown, suppressLost      int
	signalFail                              int
}

type tracer struct {
	errno    syscall.Errno
	errName  string
	template string
	want     string
	log      *os.File
	logBad   bool

	l      *ledger
	groups map[int]*group

	rootPid    int
	rootStatus int
	rootSig    bool

	seq          int
	injected     bool // the single global token is consumed (suppress succeeded)
	injDelivered bool // the paired errno rewrite completed and the marker was written
	h            health
}

type syscallInfo struct {
	op      uint8
	arch    uint32
	nr      uint64
	args    [6]uint64
	rval    int64
	isError bool
}

func main() { os.Exit(run()) }

func parseErrno(s string) (syscall.Errno, bool) {
	switch s {
	case "ENOSPC":
		return syscall.ENOSPC, true
	case "EIO":
		return syscall.EIO, true
	case "EACCES":
		return syscall.EACCES, true
	case "ENOENT":
		return syscall.ENOENT, true
	}
	return 0, false
}

func esrch(err error) bool { return errors.Is(err, syscall.ESRCH) }

func run() int {
	errName := flag.String("errno", "", "ENOSPC | EIO | EACCES | ENOENT")
	tmpl := flag.String("path", "", "absolute target path; literal <PID> is the root tracee pid")
	logPath := flag.String("log", "", "log file (retained on every exit path)")
	flag.Parse()
	args := flag.Args()
	e, ok := parseErrno(*errName)
	if !ok || !filepath.IsAbs(*tmpl) || *logPath == "" || len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: globalopen -errno E -path /abs[<PID>] -log FILE -- cmd args...")
		return exitTool
	}
	logf, err := os.Create(*logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "globalopen:", err)
		return exitTool
	}
	afterLogOpen(logf) // test seam: no-op unless built with globalopen_hooks
	tr := &tracer{errno: e, errName: *errName, template: *tmpl, log: logf, l: newLedger(), groups: map[int]*group{}}

	exe, err := exec.LookPath(args[0])
	if err != nil {
		tr.fatal("lookpath: %v", err)
	}
	tr.logf("GLOBALOPEN HEADER arch=%s errno=%s template=%s cmd=%s cleanup=pidfd (not stock strace)\n", archName, tr.errName, q(tr.template), q(strings.Join(args, " ")))
	if err := probePidfd(); err != nil {
		tr.fatal("the pidfd facility is required (Linux 5.3+ pidfd_open, 5.1+ pidfd_send_signal) and is unavailable: %v", err)
	}

	rootFd := -1
	pid, err := syscall.ForkExec(exe, args, &syscall.ProcAttr{
		Env:   os.Environ(),
		Files: []uintptr{0, 1, 2},
		Sys:   &syscall.SysProcAttr{Ptrace: true, PidFD: &rootFd},
	})
	if err != nil {
		tr.fatal("forkexec: %v", err)
	}
	tr.rootPid = pid
	rt := tr.l.addRoot(pid)
	if rootFd >= 0 {
		g := &group{tgid: pid, fd: rootFd, members: 1}
		tr.groups[pid] = g
		rt.group, rt.tgid = g, pid
	}
	tr.want = strings.ReplaceAll(tr.template, "<PID>", strconv.Itoa(pid))
	tr.logf("GLOBALOPEN TARGET root_pid=%d path=%s\n", pid, q(tr.want))
	if rootFd < 0 {
		tr.fatal("ForkExec returned no group pidfd for root %d (CLONE_PIDFD unavailable)", pid)
	}

	first, ws, err := tr.wait()
	if err == nil && first != pid {
		tr.fatal("first wait returned pid %d, not the root %d", first, pid)
	}
	if err == nil && (ws.Exited() || ws.Signaled()) {
		tr.reap(pid, ws)
	}
	if err != nil || !ws.Stopped() || ws.StopSignal() != syscall.SIGTRAP {
		tr.fatal("initial exec stop not seen: err=%v status=%#x", err, uint32(ws))
	}
	opts := optTraceSysgood | optTraceFork | optTraceVfork | optTraceClone | optTraceExec | optExitKill
	if err := syscall.PtraceSetOptions(pid, opts); err != nil {
		if !esrch(err) {
			tr.fatal("PTRACE_SETOPTIONS %#x: %v", opts, err)
		}
		tr.h.optionsLost++
		tr.loseStop(pid, rt, "PTRACE_SETOPTIONS")
	} else {
		tr.logf("GLOBALOPEN EVENT kind=start tid=%d gen=%d\n", pid, rt.gen)
		tr.resume(pid, rt, 0)
	}

	for tr.l.outstanding() > 0 {
		tid, ws, err := tr.wait()
		if err != nil {
			tr.fatal("wait4 failed with %d live and %d announced-unobserved tracee(s) outstanding: %v (no signal is sent by number; EXITKILL ends them)", len(tr.l.live), len(tr.l.placeholders), err)
		}
		switch {
		case ws.Exited() || ws.Signaled():
			tr.reap(tid, ws)
		case ws.Stopped():
			tr.handleStop(tid, ws)
		default:
			tr.fatal("unexpected wait status %#x for tid %d", uint32(ws), tid)
		}
	}

	tr.closeAllGroups()
	ok2 := tr.healthy()
	h := tr.h
	tr.logf("GLOBALOPEN HEALTH ok=%t spawned=%d reaped=%d candidates=%d injected=%d injected_started=%d injection_lost=%d passed=%d refused=%d unresolved=%d unmatched=%d unreadable=%d orphan_entries=%d unexpected_events=%d lost_stops=%d lost_before_injection=%d lost_after_injection=%d lost_pending=%d contradictions=%d event_lost=%d options_lost=%d group_unknown=%d group_conflict=%d pidfd_fail=%d close_fail=%d event_conflicts=%d ambiguous=%d unannounced=%d reconciled=%d resume_lost=%d peek_lost=%d how_unknown=%d suppress_lost=%d signal_fail=%d exec_retired=%d exec_refused=%d\n",
		ok2, tr.l.spawned, tr.l.reaped, h.candidates, h.injectDelivered, h.injectStarted, h.injectLost, h.passed, h.refused, h.unresolved, h.unmatched, h.unreadable, h.orphanEntries, h.unexpectedEvt,
		h.lostStops, h.lostBeforeInjection, h.lostAfterInjection, h.lostPending, h.contradictions, h.eventLost, h.optionsLost, h.groupUnknown, h.groupConflict, h.pidfdFail, h.closeFail, tr.l.conflicts, tr.l.ambiguous, tr.l.unannounced(), h.reconciled, h.resumeLost, h.peekLost, h.howUnknown, h.suppressLost, h.signalFail, tr.l.execRetired, tr.l.execRefused)
	tr.logf("GLOBALOPEN SUMMARY root_status=%d root_signaled=%t\n", tr.rootStatus, tr.rootSig)
	beforeLogClose(tr.log) // test seam: no-op unless built with globalopen_hooks
	if cerr := tr.log.Close(); cerr != nil {
		fmt.Fprintln(os.Stderr, "globalopen: FATAL: closing the log:", cerr)
		return exitTool
	}
	if tr.logBad {
		return exitTool
	}
	return tr.rootStatus
}

// healthy: mechanical evidence integrity only (not "an injection happened").
func (tr *tracer) healthy() bool {
	h := tr.h
	return !tr.logBad &&
		tr.l.reaped+tr.l.execRetired == tr.l.spawned && tr.l.outstanding() == 0 &&
		tr.l.execRetired == 0 && tr.l.execRefused == 0 && // non-leader exec is an UNSUPPORTED case: never healthy
		h.orphanEntries == 0 && h.refused == 0 && h.unresolved == 0 && h.unexpectedEvt == 0 &&
		tr.l.conflicts == 0 && tr.l.ambiguous == 0 &&
		h.lostBeforeInjection == 0 && h.lostPending == 0 && h.injectLost == 0 &&
		h.contradictions == 0 && h.eventLost == 0 && h.optionsLost == 0 &&
		h.groupUnknown == 0 && h.groupConflict == 0 && h.pidfdFail == 0 && h.closeFail == 0 &&
		h.unreadable == 0 && h.peekLost == 0 && h.howUnknown == 0 && h.suppressLost == 0 && h.signalFail == 0 &&
		h.candidates == h.passed+h.refused+h.unresolved+h.injectStarted+h.suppressLost &&
		h.injectStarted == h.injectDelivered+h.injectLost
}

// reap handles a real terminal wait status (the only way a generation ends).
func (tr *tracer) reap(tid int, ws syscall.WaitStatus) {
	t, known := tr.l.onExit(tid)
	if t.group != nil {
		t.group.members--
		if t.group.members <= 0 {
			tr.closeGroup(t.group)
		}
	}
	if t.pend != nil {
		tr.h.orphanEntries++
		if t.pend.inject {
			tr.h.injectLost++
			tr.logf("GLOBALOPEN EVENT kind=injection-lost tid=%d seq=%d (the tracee ended inside the injected call; no errno was delivered)\n", tid, t.pend.seq)
		}
		tr.logf("GLOBALOPEN EVENT kind=orphan-entry tid=%d seq=%d (tracee ended inside the call)\n", tid, t.pend.seq)
		t.pend = nil
	}
	if t.lost {
		tr.h.reconciled++
		tr.logf("GLOBALOPEN EVENT kind=esrch-reconciled tid=%d gen=%d (terminal wait status followed the abandoned stop)\n", tid, t.gen)
	}
	status := 0
	if ws.Exited() {
		status = ws.ExitStatus()
	} else {
		status = 128 + int(ws.Signal())
	}
	kind := "exit"
	if !known {
		kind = "exit-unannounced"
	}
	tr.logf("GLOBALOPEN EVENT kind=%s tid=%d gen=%d status=%d signaled=%t\n", kind, tid, t.gen, status, ws.Signaled())
	if tid == tr.rootPid {
		tr.rootStatus, tr.rootSig = status, ws.Signaled()
	}
}

// handleStop records a real stop. A first observation acquires (leader) or
// looks up (non-leader) the group handle BEFORE the tracee is ever resumed.
func (tr *tracer) handleStop(tid int, ws syscall.WaitStatus) {
	t, kind := tr.l.onStop(tid)
	switch kind {
	case stopAdopted:
		tr.logf("GLOBALOPEN EVENT kind=adopt tid=%d gen=%d (stop seen before its parent's event)\n", tid, t.gen)
	case stopPromoted:
		tr.logf("GLOBALOPEN EVENT kind=promote tid=%d gen=%d (stop of a tid its parent's event had announced)\n", tid, t.gen)
	}
	if kind != stopKnown {
		tr.attachGroup(tid, t)
	}
	if t.lost {
		tr.h.contradictions++
		tr.logf("GLOBALOPEN EVENT kind=esrch-contradicted tid=%d gen=%d (a stop arrived after the stop was assumed abandoned)\n", tid, t.gen)
		t.lost = false
	}
	tr.onStop(tid, t, ws)
}

// attachGroup follows pidfd_linux.go rules 2 and 3.
func (tr *tracer) attachGroup(tid int, t *tracee) {
	fd, err := pidfdOpen(tid)
	switch {
	case err == nil:
		if old := tr.groups[tid]; old != nil {
			tr.h.groupConflict++
			tr.logf("GLOBALOPEN EVENT kind=group-conflict tid=%d (a group handle for this number is already held)\n", tid)
			if cerr := syscall.Close(fd); cerr != nil {
				tr.h.closeFail++
			}
			return
		}
		g := &group{tgid: tid, fd: fd, members: 1}
		tr.groups[tid] = g
		t.group, t.tgid = g, tid
		tr.logf("GLOBALOPEN EVENT kind=group-open tgid=%d gen=%d\n", tid, t.gen)
	case needsGroupLookup(err):
		tgid, terr := readTgid(tid)
		switch {
		case terr != nil:
			tr.h.groupUnknown++
			tr.logf("GLOBALOPEN EVENT kind=group-unknown tid=%d reason=%s (pidfd_open ENOENT and the Tgid lookup failed; ownership ambiguous, EXITKILL only)\n", tid, q(terr.Error()))
		case tgid == tid:
			tr.h.pidfdFail++
			tr.logf("GLOBALOPEN EVENT kind=pidfd-open-failed tid=%d tgid=%d reason=%s\n", tid, tgid, q("pidfd_open ENOENT for a thread whose Tgid equals its own tid"))
		default:
			g := tr.groups[tgid]
			if g == nil || g.members <= 0 {
				tr.h.groupUnknown++
				tr.logf("GLOBALOPEN EVENT kind=group-unknown tid=%d tgid=%d reason=%s\n", tid, tgid, q("no held handle with live observed members names this thread's group"))
				return
			}
			g.members++
			t.group, t.tgid = g, tgid
			tr.logf("GLOBALOPEN EVENT kind=group-member tid=%d tgid=%d gen=%d\n", tid, tgid, t.gen)
		}
	default:
		tr.h.pidfdFail++
		tr.logf("GLOBALOPEN EVENT kind=pidfd-open-failed tid=%d err=%s\n", tid, q(err.Error()))
	}
}

func (tr *tracer) closeGroup(g *group) {
	if _, held := tr.groups[g.tgid]; held && tr.groups[g.tgid] == g {
		delete(tr.groups, g.tgid)
	}
	if err := syscall.Close(g.fd); err != nil {
		tr.h.closeFail++
		fmt.Fprintf(os.Stderr, "globalopen: closing the pidfd of group %d: %v\n", g.tgid, err)
	}
}

func (tr *tracer) closeAllGroups() {
	for _, g := range tr.groups {
		tr.closeGroup(g)
	}
}

func (tr *tracer) onStop(tid int, t *tracee, ws syscall.WaitStatus) {
	sig := ws.StopSignal()
	switch {
	case sig == sigSyscall:
		if tr.syscallStop(tid, t) {
			tr.resume(tid, t, 0)
		}
	case sig == syscall.SIGTRAP && ws.TrapCause() > 0:
		switch cause := ws.TrapCause(); cause {
		case evFork, evVfork, evClone:
			msg, err := syscall.PtraceGetEventMsg(tid)
			if err != nil {
				if esrch(err) {
					tr.h.eventLost++
					tr.loseStop(tid, t, "PTRACE_GETEVENTMSG")
					return
				}
				tr.fatal("PTRACE_GETEVENTMSG tid=%d: %v", tid, err)
			}
			child := int(msg)
			out := tr.l.onEvent(child)
			tr.logf("GLOBALOPEN EVENT kind=%s parent=%d child=%d outcome=%s\n", map[int]string{evFork: "fork", evVfork: "vfork", evClone: "clone"}[cause], tid, child, out)
		case evExec:
			if !tr.execEvent(tid, t, false) {
				return // the event read failed ESRCH: the stop was abandoned (loseStop)
			}
		default:
			tr.h.unexpectedEvt++
			tr.logf("GLOBALOPEN EVENT kind=unexpected-ptrace-event cause=%d tid=%d\n", cause, tid)
		}
		tr.resume(tid, t, 0)
	case sig == syscall.SIGSTOP && !t.seenStop:
		t.seenStop = true
		tr.resume(tid, t, 0)
	case sig == syscall.SIGSTOP || sig == syscall.SIGTSTP || sig == syscall.SIGTTIN || sig == syscall.SIGTTOU:
		tr.fatal("tid %d: stop signal %d is a possible group-stop, unsupported without PTRACE_SEIZE", tid, int(sig))
	default:
		t.seenStop = true
		tr.resume(tid, t, int(sig))
	}
}

// execEvent handles a PTRACE_EVENT_EXEC stop reported under tid X (t is the
// generation the ledger holds under X). The ACTUAL PTRACE_GETEVENTMSG value is
// the exec thread's former tid F.
//
//	F == X : an ordinary (leader) exec; logged as before, nothing retires.
//	F != X : a NON-LEADER exec. The kernel gave the thread the old leader's tid
//	         and delivers no terminal wait status for F, so the generation under
//	         F is retired through ledger.retireExec (no onExit, no reaped, no
//	         terminal EVENT line, no fabricated wait). The generation under X
//	         stays as the single survivor and keeps the held group handle. The
//	         case stays UNSUPPORTED: HEALTH is false whenever it happens.
//
// The survivor's pending call belonged to the dead old-leader task: it is an
// orphan (and injection_lost if it was the injected call): never delivered, no
// marker, the token stays consumed. A pending call on F contradicts "F was in
// execve" and is accounted the same way plus a contradiction. A refused
// reconciliation (unknown tid, group or member mismatch) changes nothing and is
// counted (exec_refused, unhealthy); the run then continues and a later wait
// failure stays loud.
//
// Returns false when the stop was abandoned because the event read failed with
// ESRCH (not death: the stop is kept lost, the terminal wait decides). When
// draining, a non-ESRCH read error is not fatal (the drain is already ending
// the run); it is only logged.
func (tr *tracer) execEvent(x int, t *tracee, draining bool) bool {
	msg, err := syscall.PtraceGetEventMsg(x)
	if err != nil {
		if esrch(err) {
			tr.h.eventLost++
			tr.loseStop(x, t, "PTRACE_GETEVENTMSG(exec)")
			return false
		}
		if !draining {
			tr.fatal("PTRACE_GETEVENTMSG(exec) tid=%d: %v", x, err)
		}
		tr.logf("GLOBALOPEN EVENT kind=exec tid=%d eventmsg=unreadable err=%s\n", x, q(err.Error()))
		return true
	}
	f := int(msg)
	if f == x {
		tr.logf("GLOBALOPEN EVENT kind=exec tid=%d\n", x)
		return true
	}
	former, out := tr.l.retireExec(f, x)
	if out != execOK {
		tr.logf("GLOBALOPEN EVENT kind=exec-refused former=%d new=%d eventmsg=%d reason=%s\n", f, x, uint(msg), q("the event's former tid could not be cross-checked against the ledger and its held group; nothing changed"))
		return true
	}
	// The old leader's task died with its pending call (if any).
	tr.orphanPending(x, t, "the old leader ended inside the call when another thread exec'd")
	if former.pend != nil {
		tr.h.contradictions++
		tr.orphanPending(f, former, "the exec thread had a pending call although it was inside execve")
	}
	tr.logf("GLOBALOPEN EVENT kind=exec-retired former=%d new=%d gen=%d eventmsg=%d\n", f, x, former.gen, uint(msg))
	tr.logf("GLOBALOPEN EVENT kind=exec tid=%d former=%d (non-leader exec: unsupported, HEALTH false)\n", x, f)
	return true
}

// orphanPending accounts a pending candidate whose tracee task is gone: an
// orphan entry, and a lost injection (never delivered, no marker, token not
// reused) when it was the injected call. It clears the pending state.
func (tr *tracer) orphanPending(tid int, t *tracee, why string) {
	p := t.pend
	if p == nil {
		return
	}
	tr.h.orphanEntries++
	if p.inject {
		tr.h.injectLost++
		tr.logf("GLOBALOPEN EVENT kind=injection-lost tid=%d seq=%d (%s; no errno was delivered)\n", tid, p.seq, why)
	}
	tr.logf("GLOBALOPEN EVENT kind=orphan-entry tid=%d seq=%d (%s)\n", tid, p.seq, why)
	t.pend = nil
}

// syscallStop returns false when the stop was abandoned (do not resume it).
func (tr *tracer) syscallStop(tid int, t *tracee) bool {
	info, err := getSyscallInfo(tid)
	if err != nil {
		if esrch(err) {
			tr.loseStop(tid, t, "PTRACE_GET_SYSCALL_INFO")
			return false
		}
		tr.fatal("PTRACE_GET_SYSCALL_INFO tid=%d: %v (entry/exit are never guessed)", tid, err)
	}
	afterInfo(tid, info) // test seam: no-op unless built with globalopen_hooks
	switch info.op {
	case infoEntry:
		if t.pend != nil {
			tr.fatal("tid %d: syscall entry while seq=%d is still pending", tid, t.pend.seq)
		}
		if info.arch != wantArch {
			tr.fatal("tid %d: syscall ABI arch %#x, expected %#x (compat ABI unsupported)", tid, info.arch, wantArch)
		}
		return tr.entry(tid, t, info)
	case infoExit:
		return tr.exit(tid, t, info)
	default:
		tr.fatal("tid %d: syscall stop with PTRACE_GET_SYSCALL_INFO op=%d", tid, info.op)
	}
	return true
}

func (tr *tracer) loseStop(tid int, t *tracee, op string) {
	t.lost = true
	tr.h.lostStops++
	switch {
	case t.pend != nil:
		tr.h.lostPending++
	case tr.injDelivered:
		tr.h.lostAfterInjection++
	default:
		tr.h.lostBeforeInjection++
	}
	tr.logf("GLOBALOPEN EVENT kind=esrch tid=%d gen=%d op=%s pending=%t after_injection=%t (stop abandoned, no direction guessed)\n", tid, t.gen, op, t.pend != nil, tr.injDelivered)
}

// entry returns false when the stop was abandoned (do not resume it).
func (tr *tracer) entry(tid int, t *tracee, info syscallInfo) bool {
	call, isOpen := decodeOpen(tid, info)
	if !isOpen {
		return true
	}
	if call.howErr != nil && esrch(call.howErr) {
		tr.h.peekLost++
		tr.loseStop(tid, t, "open_how peek")
		return false
	}
	buf, terminated, perr := readCString(tid, call.pathPtr, maxPath+1)
	if perr != nil {
		if esrch(perr) {
			tr.h.peekLost++
			tr.loseStop(tid, t, "path peek")
			return false
		}
		tr.h.unreadable++
		return true
	}
	raw := string(buf)
	var res resolution
	if !terminated {
		res = resolution{kind: resUnresolved, reason: "path longer than 4096 bytes"}
	} else {
		res = tr.resolve(tid, call.dirfd, raw)
	}
	if res.kind == resNoMatch {
		tr.h.unmatched++
		return true
	}
	if res.kind == resMatch && call.isHow && !call.howOK {
		tr.h.howUnknown++
		why := "open_how struct unreadable or shorter than 16 bytes (flags/mode unknown)"
		if call.howErr != nil {
			why = "open_how peek failed: " + call.howErr.Error()
		}
		res = resolution{kind: resUnresolved, reason: why}
	}
	tr.seq++
	tr.h.candidates++
	flags := "unknown"
	if !call.isHow || call.howOK {
		flags = fmt.Sprintf("0x%x:%s", call.flags, flagsSym(call.flags))
	}
	common := fmt.Sprintf("seq=%d tid=%d syscall=%s dirfd=%d raw=%s flags=%s mode=0%o", tr.seq, tid, call.name, call.dirfd, q(raw), flags, call.mode)
	if call.isHow {
		common += fmt.Sprintf(" how_size=%d how_ok=%t resolve=0x%x", call.howSize, call.howOK, call.resolve)
	}
	switch res.kind {
	case resRefused, resUnresolved:
		name := "refused"
		if res.kind == resRefused {
			tr.h.refused++
		} else {
			name = "unresolved"
			tr.h.unresolved++
		}
		tr.logf("GLOBALOPEN ENTRY %s decision=%s reason=%s\n", common, name, q(res.reason))
		t.pend = &pending{seq: tr.seq}
		return true
	}
	inject := !tr.injected
	if inject {
		beforeSuppress(tid) // test seam: no-op unless built with globalopen_hooks
		if err := suppressSyscall(tid); err != nil {
			if esrch(err) {
				tr.h.suppressLost++
				tr.logf("GLOBALOPEN ENTRY %s resolved=%s decision=suppress-lost reason=%s\n", common, q(res.resolved), q(err.Error()))
				tr.loseStop(tid, t, "suppressSyscall")
				return false
			}
			tr.fatal("suppress syscall tid=%d: %v", tid, err)
		}
		tr.injected = true
		tr.h.injectStarted++
	} else {
		tr.h.passed++
	}
	decision := "pass"
	if inject {
		decision = "inject"
	}
	t.pend = &pending{seq: tr.seq, inject: inject}
	tr.logf("GLOBALOPEN ENTRY %s resolved=%s decision=%s\n", common, q(res.resolved), decision)
	return true
}

// exit returns false when the stop was abandoned (do not resume it).
func (tr *tracer) exit(tid int, t *tracee, info syscallInfo) bool {
	p := t.pend
	if p == nil {
		return true
	}
	t.pend = nil
	if p.inject {
		if !info.isError || info.rval != -int64(syscall.ENOSYS) {
			tr.fatal("seq=%d tid=%d: suppression not proven, want raw -ENOSYS at the paired exit, got ret=%d is_error=%t", p.seq, tid, info.rval, info.isError)
		}
		if err := setReturnErrno(tid, tr.errno); err != nil {
			if esrch(err) {
				tr.h.injectLost++
				tr.logf("GLOBALOPEN EXIT seq=%d tid=%d raw=%d raw_errno=ENOSYS ret=undelivered errno=none injected=false lost=true\n", p.seq, tid, info.rval)
				tr.loseStop(tid, t, "setReturnErrno")
				return false
			}
			tr.fatal("set return tid=%d: %v", tid, err)
		}
		tr.injDelivered = true
		tr.h.injectDelivered++
		// The only (INJECTED) marker in the log.
		tr.logf("GLOBALOPEN EXIT seq=%d tid=%d raw=%d raw_errno=ENOSYS ret=%d errno=%s injected=true (INJECTED)\n", p.seq, tid, info.rval, -int64(tr.errno), tr.errName)
		return true
	}
	name := "OK"
	if info.isError {
		name = errnoName(-info.rval)
	}
	tr.logf("GLOBALOPEN EXIT seq=%d tid=%d ret=%d errno=%s injected=false\n", p.seq, tid, info.rval, name)
	return true
}

func (tr *tracer) resume(tid int, t *tracee, sig int) {
	if err := syscall.PtraceSyscall(tid, sig); err != nil {
		if esrch(err) {
			t.lost = true
			tr.h.resumeLost++
			tr.logf("GLOBALOPEN EVENT kind=resume-esrch tid=%d gen=%d pending=%t (stop already handled; terminal wait expected)\n", tid, t.gen, t.pend != nil)
			return
		}
		tr.fatal("PTRACE_SYSCALL tid=%d sig=%d: %v", tid, sig, err)
	}
}

func (tr *tracer) wait() (int, syscall.WaitStatus, error) {
	for {
		var ws syscall.WaitStatus
		tid, err := rawWait4(&ws)
		if err == syscall.EINTR {
			continue
		}
		return tid, ws, err
	}
}

// logf writes one whole line straight to the file. A failed or short write
// is never ignored: the evidence is incomplete, so the run is a fatal error.
func (tr *tracer) logf(format string, a ...any) {
	if tr.logBad {
		return
	}
	line := fmt.Sprintf(format, a...)
	if n, err := tr.log.WriteString(line); err != nil || n != len(line) {
		tr.logBad = true
		fmt.Fprintf(os.Stderr, "globalopen: FATAL: log write failed (n=%d/%d): %v\n", n, len(line), err)
		tr.drain()
		exitHook(exitTool)
	}
}

// fatal retains the message in the log (when the log works), cleans up through
// group pidfds only, and exits.
func (tr *tracer) fatal(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	tr.logf("GLOBALOPEN FATAL %s\n", strings.ReplaceAll(msg, "(INJECTED)", `(INJECTED\x29`))
	fmt.Fprintln(os.Stderr, "globalopen: FATAL:", msg)
	tr.drain()
	if err := tr.log.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "globalopen: closing the log:", err)
	}
	exitHook(exitTool)
}

// ownedLive counts live observed tracees that a signalled-or-signalable group
// handle can reach. A group whose signal FAILED (unsignalled) is excluded.
func (tr *tracer) ownedLive() int {
	n := 0
	for _, t := range tr.l.live {
		if t.group != nil && !t.group.unsignalled {
			n++
		}
	}
	return n
}

// signalOwned sends SIGKILL through a group pidfd once.
func (tr *tracer) signalOwned(g *group) {
	if g.signalled || g.unsignalled || g.members <= 0 {
		return
	}
	err := signalGroup(g.fd, g.tgid)
	switch {
	case err == nil || esrch(err):
		g.signalled = true
	default:
		g.unsignalled = true
		tr.h.signalFail++
		fmt.Fprintf(os.Stderr, "globalopen: pidfd_send_signal(group %d) failed: %v (the group is left to PTRACE_O_EXITKILL; drain will not wait for it)\n", g.tgid, err)
	}
}

// drain ends every genuinely owned group through its pidfd and reaps what it
// can, with no signal by number.
func (tr *tracer) drain() {
	for _, g := range tr.groups {
		tr.signalOwned(g)
	}
	for tr.ownedLive() > 0 {
		tid, ws, err := tr.wait()
		if err != nil {
			break
		}
		switch {
		case ws.Exited() || ws.Signaled():
			t, _ := tr.l.onExit(tid)
			if t.group != nil {
				t.group.members--
				if t.group.members <= 0 {
					tr.closeGroup(t.group)
				}
			}
		case ws.Stopped():
			t, kind := tr.l.onStop(tid)
			if kind != stopKnown {
				tr.attachGroup(tid, t)
				if t.group != nil && t.group.tgid == tid {
					tr.signalOwned(t.group)
				}
			}
			// An exec event stop met while draining goes through the SAME
			// reconciliation, so a retired former tid is not left as a phantom
			// that would keep ownedLive() > 0 (the group SIGKILL already sent
			// ends the survivor; it is deliberately not resumed).
			if ws.StopSignal() == syscall.SIGTRAP && ws.TrapCause() == evExec {
				tr.execEvent(tid, t, true)
			}
		}
	}
	tr.closeAllGroups()
}

func getSyscallInfo(tid int) (syscallInfo, error) {
	var buf [88]byte
	n, _, e := syscall.Syscall6(syscall.SYS_PTRACE, ptraceGetSyscallInfo, uintptr(tid), uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), 0, 0)
	if e != 0 {
		return syscallInfo{}, e
	}
	var si syscallInfo
	if n < 8 {
		return si, fmt.Errorf("short syscall info: %d bytes", n)
	}
	si.op = buf[0]
	si.arch = binary.NativeEndian.Uint32(buf[4:])
	switch si.op {
	case infoEntry:
		if n < 80 {
			return si, fmt.Errorf("short entry info: %d bytes", n)
		}
		si.nr = binary.NativeEndian.Uint64(buf[24:])
		for i := range si.args {
			si.args[i] = binary.NativeEndian.Uint64(buf[32+8*i:])
		}
	case infoExit:
		if n < 33 {
			return si, fmt.Errorf("short exit info: %d bytes", n)
		}
		si.rval = int64(binary.NativeEndian.Uint64(buf[24:]))
		si.isError = buf[32] != 0
	}
	return si, nil
}

func errnoName(n int64) string {
	switch syscall.Errno(n) {
	case syscall.ENOSPC:
		return "ENOSPC"
	case syscall.ENOENT:
		return "ENOENT"
	case syscall.EACCES:
		return "EACCES"
	case syscall.EIO:
		return "EIO"
	case syscall.ENOSYS:
		return "ENOSYS"
	case syscall.EEXIST:
		return "EEXIST"
	case syscall.EISDIR:
		return "EISDIR"
	case syscall.ELOOP:
		return "ELOOP"
	}
	return fmt.Sprintf("E%d", n)
}
