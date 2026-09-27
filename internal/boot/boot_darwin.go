package boot

import (
	"os"
	"syscall"
	_ "unsafe" // go:linkname
)

//go:linkname sysFcntl syscall.fcntl
func sysFcntl(fd int, cmd int, arg int) (int, error)

// setCloexec is uv__cloexec: FD_CLOEXEC on fd, false if fd is not open.
func setCloexec(fd int) bool {
	_, err := sysFcntl(fd, syscall.F_SETFD, syscall.FD_CLOEXEC)
	return err == nil
}

// terminating are the macOS signals whose default action ends the process
// and that Go would otherwise catch (see boot_linux.go); IO, URG, WINCH,
// INFO, CHLD and CONT are ignored by default there.
// The ones the runtime would drop or dump come first: os/signal registers
// them one at a time.
var terminating = []os.Signal{
	syscall.SIGALRM, syscall.SIGUSR2, syscall.SIGQUIT, syscall.SIGABRT, syscall.SIGXCPU,
	syscall.SIGVTALRM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGTRAP,
	syscall.SIGILL, syscall.SIGEMT, syscall.SIGFPE, syscall.SIGBUS, syscall.SIGSEGV, syscall.SIGSYS,
}

// resetInherited cannot change the kernel's dispositions or the signal
// mask without libc calls the syscall package does not export: an inherited
// mask and inherited SIG_IGN on the stop signals stay as they are on macOS
// (KNOWN_DIFFERENCES.md).
func resetInherited() {}
