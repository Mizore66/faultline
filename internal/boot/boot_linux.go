package boot

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/Mizore66/faultline/internal/sigexit"
)

// terminating are the signals caught to end fl by their default action:
// the ones a fault in Go code raises synchronously, which must keep Go's
// handler so that such faults still panic (sent from outside, they are
// delivered to os/signal).
var terminating = []os.Signal{syscall.SIGSEGV, syscall.SIGBUS, syscall.SIGFPE, syscall.SIGILL, syscall.SIGTRAP}

// kernelDefault are the other Linux signals whose default action ends the
// process: all of 1 to 31 except KILL, STOP, the ignored-by-default CHLD,
// URG and WINCH, the stop signals, PIPE and XFSZ (ignored by Node), USR1
// and PROF; plus the real-time signals from 35 (32 to 34 are left to the C
// library). Their kernel disposition is set straight to SIG_DFL, as Node's
// is: the runtime has already installed its handler and does not install it
// again for os/signal, and this takes microseconds where each os/signal
// registration takes a round trip to the runtime's signal thread.
var kernelDefault = func() []syscall.Signal {
	s := []syscall.Signal{
		syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGABRT, syscall.SIGUSR2,
		syscall.SIGALRM, syscall.SIGTERM, syscall.SIGSTKFLT, syscall.SIGXCPU, syscall.SIGVTALRM,
		syscall.SIGIO, syscall.SIGPWR, syscall.SIGSYS,
	}
	for sig := 35; sig <= 64; sig++ {
		s = append(s, syscall.Signal(sig))
	}
	return s
}()

// resetInherited gives fl the dispositions and mask Node starts with. The
// kernelDefault signals are set to SIG_DFL, and inherited SIG_IGN on the
// stop signals, which Go leaves alone and git would otherwise inherit, is
// reset. Then an inherited signal mask is cleared by executing fl again with
// the mask empty: Go keeps the mask it started with for its threads and for
// the processes it starts, so it must be empty from exec on. The
// dispositions come first, so a signal that was pending under the mask
// takes its default action when the mask is cleared, as it does in Node.
// The new image is /proc/self/exe, which works when the binary has been
// deleted or replaced; should exec still fail (a noexec mount), fl goes on
// with the mask it inherited (KNOWN_DIFFERENCES.md).
//
// Node resets only signals 1 to 31, so a real-time signal inherited as
// SIG_IGN stays ignored in Node and in git. Go has replaced that
// disposition with its own handler before any package initializes and
// keeps the original where it cannot be read, so real-time signals are set
// to SIG_DFL whatever they were inherited as (KNOWN_DIFFERENCES.md).
func resetInherited() {
	for _, sig := range kernelDefault {
		sigexit.SetDefault(sig)
	}
	for _, sig := range []syscall.Signal{syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU, syscall.SIGCONT} {
		if sigexit.IsIgnored(sig) {
			sigexit.SetDefault(sig)
		}
	}
	var mask uint64
	syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 0, 0, uintptr(unsafe.Pointer(&mask)), 8, 0, 0)
	if mask != 0 {
		var empty uint64
		syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 2 /* SIG_SETMASK */, uintptr(unsafe.Pointer(&empty)), 0, 8, 0, 0)
		syscall.Exec("/proc/self/exe", os.Args, os.Environ())
		syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 2, uintptr(unsafe.Pointer(&mask)), 0, 8, 0, 0)
	}
}

// setCloexec is uv__cloexec: FD_CLOEXEC on fd, false if fd is not open.
func setCloexec(fd int) bool {
	_, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, syscall.FD_CLOEXEC)
	return e == 0
}
