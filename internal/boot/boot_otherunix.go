//go:build unix && !linux && !darwin

package boot

import (
	"os"
	"syscall"
)

var terminating = []os.Signal{
	syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGABRT, syscall.SIGALRM,
	syscall.SIGTERM, syscall.SIGUSR2, syscall.SIGVTALRM, syscall.SIGXCPU,
}

func resetInherited() {}

func dieOfPending() {}

func raiseNofile() {}

func setCloexec(fd int) bool {
	_, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, syscall.FD_CLOEXEC)
	return e == 0
}
