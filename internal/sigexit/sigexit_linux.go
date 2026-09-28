// Package sigexit ends the process by a signal's default action, which the
// Go runtime cannot do for signals it handles itself (SIGTRAP, SIGQUIT,
// SIGABRT, ...): restoring "default" there means Go's handler, which dumps
// goroutines and exits 2.
package sigexit

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// sigaction is the kernel's struct sigaction on amd64 and arm64.
type sigaction struct {
	handler  uintptr
	flags    uint64
	restorer uintptr
	mask     uint64
}

// SetDefault sets sig's disposition to SIG_DFL in the kernel, behind the Go
// runtime's back.
func SetDefault(sig syscall.Signal) {
	var act sigaction // SIG_DFL
	syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&act)), 0, 8, 0, 0)
}

// IsIgnored reports whether sig's kernel disposition is SIG_IGN.
func IsIgnored(sig syscall.Signal) bool {
	var old sigaction
	_, _, e := syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), 0, uintptr(unsafe.Pointer(&old)), 8, 0, 0)
	return e == 0 && old.handler == 1 // SIG_IGN
}

// Die kills the process with sig as the kernel's default action would, as
// when Node or V8 dies of it: the disposition is set to SIG_DFL and sig is
// unblocked on this thread, then sent to it. Should the signal not end the
// process, fl exits 128+sig.
func Die(sig syscall.Signal) {
	dying.Store(true)
	runtime.LockOSThread()
	SetDefault(sig)
	set := uint64(1) << (sig - 1)
	syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 1 /* SIG_UNBLOCK */, uintptr(unsafe.Pointer(&set)), 0, 8, 0, 0)
	syscall.Tgkill(syscall.Getpid(), syscall.Gettid(), sig)
	os.Exit(128 + int(sig))
}
