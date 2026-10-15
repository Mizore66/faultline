//go:build faultline_test && unix

package difftest

import "syscall"

func mkfifo(path string) error { return syscall.Mkfifo(path, 0o644) }
