//go:build linux && arm64

package main

// Real ARM64 ptrace regression for the syscall SUPPRESSION number
// (suppressSyscall in regs_linux_arm64.go). No fake ptrace response: a real
// tracee is held at a real syscall ENTRY by this test acting as tracer, the
// helper's OWN suppressSyscall runs against it, and the kernel's own state and
// result are inspected. It is part of both the default and the hook-tagged test
// sets (no build tag beyond the file name); the child entry point is an init()
// so it works with or without the hook TestMain.
//
// What it pins. On affected arm64 kernels (the upstream v6.11 reference) a
// ptrace-set syscall number of exactly -1 (NO_SYSCALL) CAN mean "skip the
// syscall and leave x0 alone" (syscall_trace_enter jumps to trace_exit), so the
// paired syscall-exit can then show the input register x0 (an openat dirfd
// such as -100 or 4) instead of an errno. This is kernel dependent: a local
// Linux 7.2.7 run with -1 still yielded ENOSYS, which is why the test reads the
// syscall-number register back instead of relying on the observed return. The
// helper's proof that a call was really suppressed is an ACTUAL kernel -ENOSYS
// at the paired exit, and an out-of-range number other than -1 reaches it
// through normal dispatch (invoke_syscall sends those to do_ni_syscall) without
// depending on the NO_SYSCALL shortcut. So at the held ENTRY, after
// suppressSyscall, the replacement number must be a real, non-sentinel,
// out-of-range number; x0 must be left alone (an errno is never seeded into x0
// and then called proof); and resuming to the paired EXIT must yield raw
// -ENOSYS. The selected open's filesystem effect (here O_TRUNC of an existing
// file) must not have happened.
//
// The tracer thread is locked to one OS thread: required ptrace ownership, not
// alignment of the tracee. No sleeps, stress, CPU or yield control. A setup
// that cannot be established fails loudly as INVALID PROBE; there is no skip.

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

const suppressChildEnv = "GLOBALOPEN_SUPPRESS_CHILD_TARGET"

// Child mode: open the target O_WRONLY|O_TRUNC with a raw openat on the main
// OS thread (the main goroutine is locked to it during init) and exit with the
// errno the kernel returned (0 when the open succeeded). Runs before any
// TestMain, so it does not depend on the hook test set's TestMain.
func init() {
	target := os.Getenv(suppressChildEnv)
	if target == "" {
		return
	}
	p, err := syscall.BytePtrFromString(target)
	if err != nil {
		os.Exit(97)
	}
	cwd := -100
	fd, _, e := syscall.Syscall6(syscall.SYS_OPENAT, uintptr(cwd), uintptr(unsafe.Pointer(p)), uintptr(syscall.O_WRONLY|syscall.O_TRUNC|syscall.O_CLOEXEC), 0, 0, 0)
	if e == 0 {
		syscall.Close(int(fd))
		os.Exit(0)
	}
	os.Exit(int(e))
}

func suppressRegs(tid int) ([34]uint64, error) {
	var regs [34]uint64 // user_pt_regs: x0-x30, sp, pc, pstate
	err := regset(ptraceGetRegset, tid, ntPrstatus, unsafe.Pointer(&regs[0]), uintptr(len(regs)*8))
	return regs, err
}

func suppressSyscallNo(tid int) (int32, error) {
	var nr int32
	err := regset(ptraceGetRegset, tid, ntArmSystemCall, unsafe.Pointer(&nr), 4)
	return nr, err
}

func TestArm64SuppressionYieldsKernelENOSYS(t *testing.T) {
	// Intentionally never unlocked: the goroutine ends locked, so its thread
	// ends too and PTRACE_O_EXITKILL covers any tracee left behind.
	runtime.LockOSThread()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	pidfd := -1
	pid, err := syscall.ForkExec(self, []string{self}, &syscall.ProcAttr{
		Env:   append(os.Environ(), suppressChildEnv+"="+target),
		Files: []uintptr{0, 1, 2},
		Sys:   &syscall.SysProcAttr{Ptrace: true, PidFD: &pidfd},
	})
	if err != nil {
		t.Fatalf("INVALID PROBE: starting the traced child: %v", err)
	}
	reaped := false
	defer func() {
		if !reaped {
			// Cleanup uses the stable pidfd only (never a numeric signal) and
			// follows the helper's rule: no wait after a failed signal or an
			// unavailable descriptor, because that wait could block on a tracee
			// that was never signalled. PTRACE_O_EXITKILL then covers it when
			// this goroutine's locked thread ends.
			switch {
			case pidfd < 0:
				t.Errorf("cleanup: no pidfd for the tracee %d: not signalling and not waiting (left to PTRACE_O_EXITKILL)", pid)
			default:
				if serr := pidfdSendSignal(pidfd, syscall.SIGKILL); serr != nil && serr != syscall.ESRCH {
					t.Errorf("cleanup: pidfd_send_signal(SIGKILL) failed: %v: not waiting (left to PTRACE_O_EXITKILL)", serr)
				} else {
					var ws syscall.WaitStatus
					for {
						_, werr := syscall.Wait4(pid, &ws, syscall.WALL, nil)
						if werr == syscall.EINTR {
							continue
						}
						if werr != nil {
							t.Errorf("cleanup: wait4(%d) after the group signal: %v", pid, werr)
							break
						}
						if ws.Exited() || ws.Signaled() {
							break
						}
					}
				}
			}
		}
		if pidfd >= 0 {
			if cerr := syscall.Close(pidfd); cerr != nil {
				t.Errorf("cleanup: closing the pidfd: %v", cerr)
			}
		}
	}()

	wait := func() syscall.WaitStatus {
		for {
			var ws syscall.WaitStatus
			_, werr := syscall.Wait4(pid, &ws, syscall.WALL, nil)
			if werr == syscall.EINTR {
				continue
			}
			if werr != nil {
				t.Fatalf("INVALID PROBE: wait4(%d): %v", pid, werr)
			}
			if ws.Exited() || ws.Signaled() {
				reaped = true
			}
			return ws
		}
	}
	resume := func(sig int) {
		if err := syscall.PtraceSyscall(pid, sig); err != nil {
			t.Fatalf("INVALID PROBE: PTRACE_SYSCALL: %v", err)
		}
	}

	// The initial exec stop.
	if ws := wait(); !ws.Stopped() || ws.StopSignal() != syscall.SIGTRAP {
		t.Fatalf("INVALID PROBE: no initial exec stop (status %#x)", uint32(ws))
	}
	if err := syscall.PtraceSetOptions(pid, optTraceSysgood|optExitKill); err != nil {
		t.Fatalf("INVALID PROBE: PTRACE_SETOPTIONS: %v", err)
	}
	resume(0)

	// Run to the child's real openat of the target. Signal-delivery stops
	// (the Go runtime's own signals) are passed through unchanged.
	const maxStops = 200000
	stops := 0
	for held := false; !held; {
		ws := wait()
		if !ws.Stopped() {
			t.Fatalf("INVALID PROBE: the tracee ended (status %#x) before its openat of the target", uint32(ws))
		}
		if stops++; stops > maxStops {
			t.Fatalf("INVALID PROBE: %d stops without reaching the target openat", maxStops)
		}
		sig := ws.StopSignal()
		if sig != sigSyscall {
			if sig == syscall.SIGTRAP {
				resume(0)
			} else {
				resume(int(sig))
			}
			continue
		}
		info, ierr := getSyscallInfo(pid)
		if ierr != nil {
			t.Fatalf("INVALID PROBE: PTRACE_GET_SYSCALL_INFO: %v", ierr)
		}
		if info.op == infoEntry && info.arch == wantArch && info.nr == sysOpenat {
			if buf, term, rerr := readCString(pid, uintptr(info.args[1]), maxPath+1); rerr == nil && term && string(buf) == target {
				held = true
				continue
			}
		}
		resume(0)
	}

	// Held at the real ENTRY of the child's openat(target, O_WRONLY|O_TRUNC).
	before, err := suppressRegs(pid)
	if err != nil {
		t.Fatalf("INVALID PROBE: reading the entry registers: %v", err)
	}
	if nr, err := suppressSyscallNo(pid); err != nil || nr != sysOpenat {
		t.Fatalf("INVALID PROBE: syscall number regset at the held entry = %d (%v), want openat %d", nr, err, sysOpenat)
	}

	if err := suppressSyscall(pid); err != nil {
		t.Fatalf("INVALID PROBE: the helper's suppressSyscall failed: %v", err)
	}

	// RED (kernel-independent): the replacement number must not be the NO_SYSCALL
	// sentinel (-1), whose paired exit CAN preserve x0 instead of returning ENOSYS
	// on affected kernels (a local Linux 7.2.7 run still yielded ENOSYS, so the
	// register is read back rather than the return value), and must be an
	// out-of-range number the kernel dispatches to do_ni_syscall.
	nr, err := suppressSyscallNo(pid)
	if err != nil {
		t.Fatalf("INVALID PROBE: reading the post-suppress syscall number: %v", err)
	}
	if nr == -1 {
		t.Errorf("suppressSyscall left the syscall number at NO_SYSCALL (-1): the kernel then skips the call and preserves x0, so the paired exit does not carry an ENOSYS")
	}
	if uint32(nr) < 1024 { // arm64's __NR_syscalls is well below this
		t.Errorf("the post-suppress syscall number %d (unsigned %d) is inside the dispatch table or small enough to be valid: it must be an out-of-range number", nr, uint32(nr))
	}
	// x0 is left alone: an errno must never be seeded into it and called proof.
	after, err := suppressRegs(pid)
	if err != nil {
		t.Fatalf("INVALID PROBE: reading the post-suppress registers: %v", err)
	}
	if after[0] != before[0] {
		t.Errorf("x0 changed across suppression: %#x -> %#x (an errno must not be seeded)", before[0], after[0])
	}
	if after[0] == ^uint64(syscall.ENOSYS-1) {
		t.Errorf("x0 was seeded with -ENOSYS: that would fabricate the proof")
	}

	// Resume to the paired EXIT and inspect the kernel's real raw result.
	resume(0)
	for {
		ws := wait()
		if !ws.Stopped() {
			t.Fatalf("INVALID PROBE: the tracee ended (status %#x) before the paired exit", uint32(ws))
		}
		if stops++; stops > maxStops {
			t.Fatalf("INVALID PROBE: %d stops without reaching the paired exit", maxStops)
		}
		sig := ws.StopSignal()
		if sig != sigSyscall {
			if sig == syscall.SIGTRAP {
				resume(0)
			} else {
				resume(int(sig))
			}
			continue
		}
		info, ierr := getSyscallInfo(pid)
		if ierr != nil {
			t.Fatalf("INVALID PROBE: PTRACE_GET_SYSCALL_INFO at the exit: %v", ierr)
		}
		if info.op != infoExit {
			t.Fatalf("INVALID PROBE: the next syscall stop is not the paired exit (op %d)", info.op)
		}
		if !info.isError || info.rval != -int64(syscall.ENOSYS) {
			t.Errorf("paired EXIT raw result: rval=%d is_error=%t, want the kernel's -ENOSYS (%d) (an input register such as the openat dirfd means the call was skipped without an errno)", info.rval, info.isError, -int(syscall.ENOSYS))
		}
		break
	}

	// Let the child finish; it exits with the errno it observed.
	resume(0)
	status := -1
	for {
		ws := wait()
		if ws.Exited() {
			status = ws.ExitStatus()
			break
		}
		if ws.Signaled() {
			t.Fatalf("INVALID PROBE: the child was killed by signal %v", ws.Signal())
		}
		sig := ws.StopSignal()
		if sig == sigSyscall || sig == syscall.SIGTRAP {
			resume(0)
		} else {
			resume(int(sig))
		}
	}
	if status != int(syscall.ENOSYS) {
		t.Errorf("the child observed errno %d from the suppressed openat, want ENOSYS (%d)", status, int(syscall.ENOSYS))
	}

	// The selected open's filesystem effect must not have happened.
	if b, err := os.ReadFile(target); err != nil || string(b) != "keep" {
		t.Errorf("the suppressed O_TRUNC open modified the target: content %q (%v), want %q", b, err, "keep")
	}
}
