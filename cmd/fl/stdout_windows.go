package main

import (
	"os"
	"sync"
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

// readOnly is false: libuv on Windows does not check a handle's access.
func readOnly(*os.File) bool { return false }

// writeOnce is fs.writeSync: one WriteFile, whose count is not checked.
func writeOnce(f *os.File, p []byte) error {
	var n uint32
	return fsWriteErrno(syscall.WriteFile(syscall.Handle(f.Fd()), p, &n, nil))
}

// writeAll writes everything (Node's pipes and consoles write synchronously
// on Windows, through fs__write like files).
func writeAll(f *os.File, p []byte) error {
	for len(p) > 0 {
		var n uint32
		if err := syscall.WriteFile(syscall.Handle(f.Fd()), p, &n, nil); err != nil {
			return fsWriteErrno(err)
		}
		if n == 0 {
			return nil
		}
		p = p[n:]
	}
	return nil
}

// fsWriteErrno is fs__write's rewrite of ERROR_ACCESS_DENIED (a handle not
// opened for writing) to ERROR_INVALID_FLAGS, which libuv reports as EBADF.
func fsWriteErrno(err error) error {
	if err == syscall.ERROR_ACCESS_DENIED {
		return syscall.Errno(1004) // ERROR_INVALID_FLAGS
	}
	return err
}

// eolState is libuv's tty.wr.previous_eol, kept across writes.
var eolState rune

// writeTTY is libuv's uv__tty_write_bufs (src/win/tty.c): consoleUTF16,
// then WriteConsoleW. Errors are libuv's stream errors
// (uv_translate_sys_error), not fs__write's.
func writeTTY(f *os.File, p []byte) error {
	vtermOnce.Do(func() { enableVTerm(syscall.Handle(f.Fd())) })
	units := consoleUTF16(p, &eolState)
	for len(units) > 0 {
		var n uint32
		if err := syscall.WriteConsole(syscall.Handle(f.Fd()), &units[0], uint32(len(units)), &n, nil); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		units = units[n:]
	}
	return nil
}

var vtermOnce sync.Once

// enableVTerm is libuv's uv__determine_vterm_state, which uv_tty_init runs
// once for the first console handle Node opens for writing (process.stdout
// on its first write): it turns on ENABLE_VIRTUAL_TERMINAL_PROCESSING, so
// the console itself interprets ANSI escape sequences in the output, and
// leaves it on after Node exits (round 6). Consoles that refuse the mode get
// libuv's own ANSI parser under Node, which is not ported (KNOWN_DIFFERENCES).
func enableVTerm(h syscall.Handle) {
	const enableVirtualTerminalProcessing = 0x0004
	var mode uint32
	if syscall.GetConsoleMode(h, &mode) != nil {
		return
	}
	procSetConsoleMode.Call(uintptr(h), uintptr(mode|enableVirtualTerminalProcessing))
}

var procSetConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")
