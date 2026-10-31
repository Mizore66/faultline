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

func suppressSyscall(tid int) error {
	nr := int32(-1)
	if err := regset(ptraceSetRegset, tid, ntArmSystemCall, unsafe.Pointer(&nr), 4); err != nil {
		return fmt.Errorf("PTRACE_SETREGSET NT_ARM_SYSTEM_CALL=-1: %w", err)
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
