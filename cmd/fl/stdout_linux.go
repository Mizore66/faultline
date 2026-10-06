package main

import (
	"syscall"
	"unsafe"
)

const ioctlGetTermios = syscall.TCGETS

func waitWritable(fd int) {
	pfd := struct {
		fd             int32
		events, revent int16
	}{int32(fd), 0x4 /* POLLOUT */, 0}
	syscall.Syscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&pfd)), 1, 0, 0, 0, 0)
}
