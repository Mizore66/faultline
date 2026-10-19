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

// sigaction, MaxSignal and the sigprocmask constants are per
// architecture (sigarch_*_linux.go); Set is the kernel's sigset_t.

// runtimeAction is the Go runtime's handler for each signal SetDefault or
// SetIgnored replaced (whichever replaced it first), kept so that Intercept
// can give it back: the runtime installs its handler once, and os/signal
// does not install it again, so a signal set to SIG_DFL here would end fl
// even after signal.Notify.
var runtimeAction [MaxSignal + 1]sigaction

// SetDefault sets sig's disposition to SIG_DFL in the kernel, behind the Go
// runtime's back.
func SetDefault(sig syscall.Signal) { replaceAction(sig, sigaction{}) }

// replaceAction sets sig's disposition in the kernel and keeps the Go
// runtime's handler it replaces, the first time it replaces one.
func replaceAction(sig syscall.Signal, act sigaction) {
	var old sigaction
	syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&act)), uintptr(unsafe.Pointer(&old)), sigsetSize, 0, 0)
	if sig > 0 && int(sig) < len(runtimeAction) && old.handler > 1 && runtimeAction[sig].handler == 0 {
		runtimeAction[sig] = old
	}
}

// restoreRuntimeHandler gives sig back to the Go runtime's handler, so that
// os/signal delivers it (Intercept).
func restoreRuntimeHandler(sig syscall.Signal) {
	if sig > 0 && int(sig) < len(runtimeAction) && runtimeAction[sig].handler > 1 {
		act := runtimeAction[sig]
		syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&act)), 0, sigsetSize, 0, 0)
	}
}

// SetIgnored sets sig's disposition to SIG_IGN in the kernel, which also
// discards it if it is pending. Like SetDefault it keeps the runtime's
// handler: boot sets a signal the parent ignored to SIG_IGN before SIG_DFL,
// and the runtime installs its handler for an inherited-ignored SIGTERM
// (unlike SIGHUP and SIGINT), so this is the only call that sees it
// (round 7 #6).
func SetIgnored(sig syscall.Signal) { replaceAction(sig, sigaction{handler: 1}) }

// setIgnore sets sig's disposition to SIG_IGN in the kernel.
func setIgnore(sig syscall.Signal) {
	act := sigaction{handler: 1} // SIG_IGN
	syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&act)), 0, sigsetSize, 0, 0)
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
	_, _, e := syscall.RawSyscall6(syscall.SYS_RT_SIGACTION, uintptr(sig), 0, uintptr(unsafe.Pointer(&old)), sigsetSize, 0, 0)
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
	for other := syscall.Signal(1); other <= MaxSignal; other++ {
		if other != sig && other != syscall.SIGKILL && other != syscall.SIGSTOP && !runtimeSignal(other) {
			setIgnore(other)
		}
	}
	SetDefault(sig)
	unblock(sig)
	syscall.Tgkill(syscall.Getpid(), syscall.Gettid(), sig)
	os.Exit(128 + int(sig))
}

// RecordIgnored is only needed where Die resets dispositions through
// os/signal.
func RecordIgnored() {}
