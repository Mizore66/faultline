//go:build unix && !linux

package main

import (
	"syscall"
	"time"
	"unsafe"
)

const ioctlGetTermios = syscall.TIOCGETA

// waitWritable waits with select(2) until fd is writable.
func waitWritable(fd int) {
	var set syscall.FdSet
	bits := (*[unsafe.Sizeof(set)]byte)(unsafe.Pointer(&set)) // little-endian words
	if fd/8 >= len(bits) {
		return
	}
	bits[fd/8] |= 1 << (uint(fd) % 8)
	tv := syscall.NsecToTimeval(int64(time.Second))
	syscall.Select(fd+1, nil, &set, nil, &tv)
}
