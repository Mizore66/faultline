//go:build linux && arm64

package main

// linux/arm64 syscall numbers and register access for the open group.

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	archName   = "arm64"
	wantArch   = uint32(0xc00000b7) // AUDIT_ARCH_AARCH64
	hasOpen    = false              // arm64 has no open(2)
	sysOpen    = 0                  // unused: hasOpen is false
	sysOpenat  = 56
	sysOpenat2 = 437

	ptraceGetRegset = 0x4204
	ptraceSetRegset = 0x4205
	ntPrstatus      = 0x1
	ntArmSystemCall = 0x404
)

type iovec struct {
	base unsafe.Pointer
	n    uintptr
}

func regset(req uintptr, tid int, nt uintptr, p unsafe.Pointer, size uintptr) error {
	iov := iovec{base: p, n: size}
	_, _, e := syscall.Syscall6(syscall.SYS_PTRACE, req, uintptr(tid), nt, uintptr(unsafe.Pointer(&iov)), 0, 0)
	if e != 0 {
		return e
	}
	return nil
}

// suppressedSyscallNr is the replacement syscall number written at a syscall
// ENTRY to cancel the call. It is deliberately NOT -1: on affected kernels
// (the upstream v6.11 reference) exactly NO_SYSCALL (-1) can make the kernel
// skip the call and leave x0 alone (syscall_trace_enter jumps to trace_exit),
// so the paired exit can show the input register (an openat dirfd such as -100
// or 4) instead of an errno and the raw-ENOSYS proof would fail. That behavior
// is kernel dependent: a local Linux 7.2.7 run with -1 still produced ENOSYS.
// An out-of-range number other than -1 takes the normal dispatch path, where
// invoke_syscall compares the number as UNSIGNED against __NR_syscalls and
// sends an out-of-range one to do_ni_syscall, which sets -ENOSYS in x0; that
// does not depend on the NO_SYSCALL shortcut. -2 is 0xFFFFFFFE unsigned, far
// beyond the table and distinct from the sentinel. x0 is never seeded: the
// -ENOSYS at the paired exit must be the kernel's own.
const suppressedSyscallNr = int32(-2)

func suppressSyscall(tid int) error {
	nr := suppressedSyscallNr
	if err := regset(ptraceSetRegset, tid, ntArmSystemCall, unsafe.Pointer(&nr), 4); err != nil {
		return fmt.Errorf("PTRACE_SETREGSET NT_ARM_SYSTEM_CALL=%d: %w", nr, err)
	}
	return nil
}

func setReturnErrno(tid int, e syscall.Errno) error {
	var regs [34]uint64 // user_pt_regs: x0-x30, sp, pc, pstate
	if err := regset(ptraceGetRegset, tid, ntPrstatus, unsafe.Pointer(&regs[0]), uintptr(len(regs)*8)); err != nil {
		return fmt.Errorf("PTRACE_GETREGSET NT_PRSTATUS: %w", err)
	}
	regs[0] = uint64(-int64(e))
	if err := regset(ptraceSetRegset, tid, ntPrstatus, unsafe.Pointer(&regs[0]), uintptr(len(regs)*8)); err != nil {
		return fmt.Errorf("PTRACE_SETREGSET NT_PRSTATUS x0: %w", err)
	}
	return nil
}
