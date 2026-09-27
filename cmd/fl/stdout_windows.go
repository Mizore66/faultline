package main

import (
	"os"
	"syscall"
)

const isWindows = true

// isWindowsPipeClosed is ERROR_BROKEN_PIPE or ERROR_NO_DATA, which
// uv_translate_write_sys_error reports as EPIPE.
func isWindowsPipeClosed(errno syscall.Errno) bool { return errno == 109 || errno == 232 }

// guessHandle is uv_guess_handle (src/win/util.c).
func guessHandle(f *os.File) handleType {
	h := syscall.Handle(f.Fd())
	t, err := syscall.GetFileType(h)
	if err != nil {
		return handleUnknown
	}
	switch t {
	case syscall.FILE_TYPE_CHAR:
		var mode uint32
		if syscall.GetConsoleMode(h, &mode) == nil {
			return handleTTY
		}
		return handleFile
	case syscall.FILE_TYPE_PIPE:
		return handlePipe
	case syscall.FILE_TYPE_DISK:
		return handleFile
	}
	return handleUnknown
}

// writeOnce is fs.writeSync: one WriteFile, whose count is not checked.
func writeOnce(f *os.File, p []byte) error {
	var n uint32
	return syscall.WriteFile(syscall.Handle(f.Fd()), p, &n, nil)
}

// writeAll writes everything (Node's pipes and consoles write synchronously
// on Windows).
func writeAll(f *os.File, p []byte) error {
	_, err := f.Write(p)
	return err
}
