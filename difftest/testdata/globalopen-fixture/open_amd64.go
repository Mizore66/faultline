//go:build linux && amd64

package main

import (
	"syscall"
	"unsafe"
)

func rawHasOpen() bool { return true }

const sysRtSigsuspend = 130 // x86_64 rt_sigsuspend

// rawOpenOrOpenat issues open(2) directly (amd64 only).
func rawOpenOrOpenat(path string) (int, error) {
	fd, _, e := syscall.Syscall(syscall.SYS_OPEN, uintptr(unsafe.Pointer(cstr(path))), uintptr(syscall.O_RDONLY|syscall.O_CLOEXEC), 0)
	if e != 0 {
		return -1, e
	}
	return int(fd), nil
}
