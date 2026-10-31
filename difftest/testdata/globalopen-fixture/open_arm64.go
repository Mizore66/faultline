//go:build linux && arm64

package main

import "syscall"

func rawHasOpen() bool { return false }

const sysRtSigsuspend = 133 // arm64 rt_sigsuspend

// rawOpenOrOpenat: arm64 has no open(2); openat is the member.
func rawOpenOrOpenat(path string) (int, error) {
	return rawOpenat(atFDCWD, path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
}
