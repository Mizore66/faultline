package boot

import (
	"os"
	"syscall"
	"unsafe" // go:linkname

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

// resetInherited gives fl the dispositions and mask Node starts with.
//
// Node's PlatformInit clears the mask first and resets the dispositions
// after (src/node.cc), so a signal pending under the inherited mask meets
// the disposition fl inherited: SIG_DFL ends the process, SIG_IGN discards
// the signal. fl does the same where that disposition is still known:
//   - inherited SIG_IGN on the stop signals (TSTP, TTIN, TTOU, CONT), which
//     Go leaves alone, is reset only after the mask is cleared, so a pending
//     one is discarded rather than stopping fl;
//   - a pending PIPE or XFSZ, which fl catches (boot_unix.go), ends fl as
//     it ends Node (dieOfPending).
//
// The other signals had their disposition replaced by the Go runtime before
// any package initialized, so fl cannot tell an inherited SIG_IGN from
// SIG_DFL: they are set to SIG_DFL first, and a pending one ends fl even
// where Node, which inherited SIG_IGN, would discard it
// (KNOWN_DIFFERENCES.md).
//
// The mask is cleared by executing fl again with the mask empty: Go keeps
// the mask it started with for its threads and for the processes it
// starts, so it must be empty from exec on. Pending signals are delivered
// when this thread unblocks them, before the exec. The new image is
// /proc/self/exe, which works when the binary has been deleted or
// replaced; should exec still fail (no /proc), fl goes on with the mask it
// inherited (KNOWN_DIFFERENCES.md).
//
// Node resets only signals 1 to 31, so a real-time signal inherited as
// SIG_IGN stays ignored in Node and in git. Go has replaced that
// disposition with its own handler, so real-time signals are set to
// SIG_DFL whatever they were inherited as (KNOWN_DIFFERENCES.md).
func resetInherited() {
	for _, sig := range kernelDefault {
		sigexit.SetDefault(sig)
	}
	var mask uint64
	syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 0, 0, uintptr(unsafe.Pointer(&mask)), 8, 0, 0)
	if mask != 0 {
		var empty uint64
		syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 2 /* SIG_SETMASK */, uintptr(unsafe.Pointer(&empty)), 0, 8, 0, 0)
		syscall.Exec("/proc/self/exe", os.Args, os.Environ())
		syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 2, uintptr(unsafe.Pointer(&mask)), 0, 8, 0, 0)
	}
	for _, sig := range []syscall.Signal{syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU, syscall.SIGCONT} {
		if sigexit.IsIgnored(sig) {
			sigexit.SetDefault(sig)
		}
	}
}

// dieOfPending runs before os/signal sees PIPE and XFSZ: registering them
// unblocks them on the runtime's signal thread, where a pending one would be
// caught and dropped. Node meets such a signal with its inherited
// disposition, SIG_DFL unless the parent ignored it, and dies of it. So when
// PIPE or XFSZ is pending under the inherited mask, the dispositions are set
// as Node has them at that moment (SIG_DFL, inherited SIG_IGN kept on the
// stop signals) and the mask is cleared on this thread: the kernel then
// delivers the pending signals in its own order, as it does to Node.
func dieOfPending() {
	var mask, pending uint64
	syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 0, 0, uintptr(unsafe.Pointer(&mask)), 8, 0, 0)
	syscall.RawSyscall(syscall.SYS_RT_SIGPENDING, uintptr(unsafe.Pointer(&pending)), 8, 0)
	fatal := pending & mask & (1<<(syscall.SIGPIPE-1) | 1<<(syscall.SIGXFSZ-1))
	if fatal == 0 {
		return
	}
	for _, sig := range append(kernelDefault, syscall.SIGPIPE, syscall.SIGXFSZ) {
		sigexit.SetDefault(sig)
	}
	var empty uint64
	syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, 2 /* SIG_SETMASK */, uintptr(unsafe.Pointer(&empty)), 0, 8, 0, 0)
	sig := syscall.SIGPIPE
	if fatal&(1<<(syscall.SIGPIPE-1)) == 0 {
		sig = syscall.SIGXFSZ
	}
	sigexit.Die(sig) // not reached: the pending signal has ended fl
}

// setCloexec is uv__cloexec: FD_CLOEXEC on fd, false if fd is not open.
func setCloexec(fd int) bool {
	_, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, syscall.FD_CLOEXEC)
	return e == 0
}

// fdFlags is fcntl(fd, F_GETFD); false if fd is not open.
func fdFlags(fd int) (int, bool) {
	r, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	return int(r), e == 0
}

//go:linkname getAuxv runtime.getAuxv
func getAuxv() []uintptr

// unsecureEnvvars is glibc's UNSECURE_ENVVARS (sysdeps/generic/unsecvars.h)
// as glibc 2.36's loader applies it, measured with a setcap'd Node 22.22.2.
var unsecureEnvvars = []string{
	"GCONV_PATH", "GETCONF_DIR", "HOSTALIASES", "LD_AUDIT", "LD_DEBUG", "LD_DEBUG_OUTPUT", "LD_DYNAMIC_WEAK",
	"LD_HWCAP_MASK", "LD_LIBRARY_PATH", "LD_ORIGIN_PATH", "LD_PRELOAD", "LD_PROFILE", "LOCALDOMAIN", "LOCPATH",
	"MALLOC_TRACE", "MALLOC_CHECK_", "NIS_PATH", "NLSPATH", "RESOLV_HOST_CONF", "RES_OPTIONS", "TMPDIR", "TZDIR",
}

// stripSecureEnv is what glibc's dynamic loader does before Node starts
// when getauxval(AT_SECURE) is set (setuid, setgid or file capabilities):
// it deletes these variables, so neither Node's os.tmpdir() nor git sees
// them. A static Go binary has no loader to do it (round 6).
func stripSecureEnv() {
	for a := getAuxv(); len(a) >= 2; a = a[2:] {
		if a[0] == 23 && a[1] != 0 { // AT_SECURE
			for _, name := range unsecureEnvvars {
				os.Unsetenv(name)
			}
			return
		}
	}
}
