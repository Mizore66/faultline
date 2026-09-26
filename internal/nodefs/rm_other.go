//go:build !windows

package nodefs

import "syscall"

func unlinkRaw(raw string) error { return syscall.Unlink(raw) }

func rmdirRaw(raw string) error { return syscall.Rmdir(raw) }
