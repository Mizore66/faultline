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

// runtimeAction is the Go runtime's handler for each signal SetDefault
// replaced, kept so that Intercept can give it back: the runtime installs
// its handler once, and os/signal does not install it again, so a signal
// set to SIG_DFL here would end fl even after signal.Notify.
var runtimeAction [65]sigaction

// SetDefault sets sig's disposition to SIG_DFL in the kernel, behind the Go
// runtime's back.
func SetDefault(sig syscall.Signal) {
	var act, old sigaction // SIG_DFL
	syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&act)), uintptr(unsafe.Pointer(&old)), 8, 0, 0)
	if sig > 0 && int(sig) < len(runtimeAction) && old.handler > 1 && runtimeAction[sig].handler == 0 {
		runtimeAction[sig] = old
	}
}

// restoreRuntimeHandler gives sig back to the Go runtime's handler, so that
// os/signal delivers it (Intercept).
func restoreRuntimeHandler(sig syscall.Signal) {
	if sig > 0 && int(sig) < len(runtimeAction) && runtimeAction[sig].handler > 1 {
		act := runtimeAction[sig]
		syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&act)), 0, 8, 0, 0)
	}
}

// SetIgnored sets sig's disposition to SIG_IGN in the kernel, which also
// discards it if it is pending.
func SetIgnored(sig syscall.Signal) { setIgnore(sig) }

// setIgnore sets sig's disposition to SIG_IGN in the kernel.
func setIgnore(sig syscall.Signal) {
	act := sigaction{handler: 1} // SIG_IGN
	syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&act)), 0, 8, 0, 0)
}

// runtimeSignal is a signal the Go runtime needs while fl dies: faults in
// Go code, preemption (URG), and the C library's 32 to 34.
func runtimeSignal(sig syscall.Signal) bool {
	switch sig {
	case syscall.SIGSEGV, syscall.SIGBUS, syscall.SIGFPE, syscall.SIGILL, syscall.SIGURG, 32, 33, 34:
		return true
	}
	return false
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
	// Node dies of the first signal; one that arrives while fl is dying of
	// it must not take its place.
	for other := syscall.Signal(1); other <= 64; other++ {
		if other != sig && other != syscall.SIGKILL && other != syscall.SIGSTOP && !runtimeSignal(other) {
			setIgnore(other)
		}
	}
	SetDefault(sig)
	set := uint64(1) << (sig - 1)
	syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 1 /* SIG_UNBLOCK */, uintptr(unsafe.Pointer(&set)), 0, 8, 0, 0)
	syscall.Tgkill(syscall.Getpid(), syscall.Gettid(), sig)
	os.Exit(128 + int(sig))
}

// RecordIgnored is only needed where Die resets dispositions through
// os/signal.
func RecordIgnored() {}
