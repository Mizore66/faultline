//go:build !windows

package nodefs

import "syscall"

// libuv retries both on EINTR (uv__fs_work).
func unlinkRaw(raw string) error { return retryEINTR(func() error { return syscall.Unlink(raw) }) }

func rmdirRaw(raw string) error { return retryEINTR(func() error { return syscall.Rmdir(raw) }) }
