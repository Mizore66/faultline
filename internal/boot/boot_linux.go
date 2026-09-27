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

// resetInherited sets kernelDefault to SIG_DFL, clears a signal mask fl
// inherited, by executing itself
// again with the mask empty (Go keeps the inherited mask for its threads and
// for the processes it starts, so it must be empty from exec on), and sets
// inherited SIG_IGN on the stop signals back to SIG_DFL, which Go leaves
// alone and git would otherwise inherit.
func resetInherited() {
	var mask uint64
	syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 0, 0, uintptr(unsafe.Pointer(&mask)), 8, 0, 0)
	if mask != 0 {
		if exe, err := os.Executable(); err == nil {
			var empty uint64
			syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 2 /* SIG_SETMASK */, uintptr(unsafe.Pointer(&empty)), 0, 8, 0, 0)
			syscall.Exec(exe, os.Args, os.Environ())
			syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 2, uintptr(unsafe.Pointer(&mask)), 0, 8, 0, 0)
		}
	}
	for _, sig := range kernelDefault {
		sigexit.SetDefault(sig)
	}
	for _, sig := range []syscall.Signal{syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU, syscall.SIGCONT} {
		if sigexit.IsIgnored(sig) {
			sigexit.SetDefault(sig)
		}
	}
}

// setCloexec is uv__cloexec: FD_CLOEXEC on fd, false if fd is not open.
func setCloexec(fd int) bool {
	_, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, syscall.FD_CLOEXEC)
	return e == 0
}
