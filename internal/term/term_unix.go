//go:build unix

// Package term holds the shared terminal check used by cmd/fl and the CLI.
package term

import (
	"os"
	"syscall"
	"unsafe"
)

// IsTerminal reports whether file is a terminal: the isatty(3) ioctl.
func IsTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), ioctlGetTermios, uintptr(unsafe.Pointer(&t)))
	return e == 0
}
