package main

import (
	"os"
	"syscall"
)

func isTerminal(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}

// isWindowsPipeClosed is ERROR_BROKEN_PIPE or ERROR_NO_DATA, which
// uv_translate_write_sys_error reports as EPIPE.
func isWindowsPipeClosed(errno syscall.Errno) bool { return errno == 109 || errno == 232 }
