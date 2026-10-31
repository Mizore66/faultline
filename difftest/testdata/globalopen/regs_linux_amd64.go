//go:build linux && amd64

package main

// linux/amd64 syscall numbers and register access for the open group.

import (
	"fmt"
	"syscall"
)

const (
	archName   = "amd64"
	wantArch   = uint32(0xc000003e) // AUDIT_ARCH_X86_64
	hasOpen    = true
	sysOpen    = 2
	sysOpenat  = 257
	sysOpenat2 = 437
)

func suppressSyscall(tid int) error {
	var r syscall.PtraceRegs
	if err := syscall.PtraceGetRegs(tid, &r); err != nil {
		return fmt.Errorf("PTRACE_GETREGS: %w", err)
	}
	r.Orig_rax = ^uint64(0)
	if err := syscall.PtraceSetRegs(tid, &r); err != nil {
		return fmt.Errorf("PTRACE_SETREGS orig_rax=-1: %w", err)
	}
	return nil
}

func setReturnErrno(tid int, e syscall.Errno) error {
	var r syscall.PtraceRegs
	if err := syscall.PtraceGetRegs(tid, &r); err != nil {
		return fmt.Errorf("PTRACE_GETREGS: %w", err)
	}
	r.Rax = uint64(-int64(e))
	if err := syscall.PtraceSetRegs(tid, &r); err != nil {
		return fmt.Errorf("PTRACE_SETREGS rax: %w", err)
	}
	return nil
}
