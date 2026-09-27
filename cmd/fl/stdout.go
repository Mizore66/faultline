package main

import (
	"errors"
	"os"
	"syscall"

	"github.com/Mizore66/faultline/internal/nodefs"
)

// handleType is libuv's uv_guess_handle, which Node's
// createWritableStdioStream uses to build process.stdout.
type handleType int

const (
	handleUnknown handleType = iota
	handleTTY
	handleFile
	handlePipe
	handleTCP
	handleUDP
)

// stdoutWriter is process.stdout for fd 1:
//   - a TTY, pipe or TCP socket is a libuv stream, which writes everything,
//     waiting while the descriptor would block (another process may have
//     made it non-blocking), and reports "Error: write CODE";
//   - a file or other character device is a SyncWriteStream: one write(2)
//     per chunk, whose short count is ignored, and "Error: CODE: desc,
//     write" when the call fails;
//   - UDP sockets and anything libuv cannot classify get Node's dummy
//     Writable, which discards everything.
//
// Node's pipes on Windows write synchronously too and report the file form.
// The first error is kept, like the 'error' event.
type stdoutWriter struct {
	f    *os.File
	kind handleType
	err  error
}

func newStdoutWriter(f *os.File) *stdoutWriter {
	return &stdoutWriter{f: f, kind: guessHandle(f)}
}

func (s *stdoutWriter) Write(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	switch s.kind {
	case handleUDP, handleUnknown:
		return len(p), nil
	case handleFile:
		if err := writeOnce(s.f, p); err != nil {
			s.err = err
			return 0, err
		}
		return len(p), nil
	}
	if err := writeAll(s.f, p); err != nil {
		s.err = err
		return 0, err
	}
	return len(p), nil
}

// errorLine is the error line of Node's unhandled stdout 'error' event.
// Windows pipe errors 109 and 232 are EPIPE for writes
// (uv_translate_write_sys_error).
func (s *stdoutWriter) errorLine() string {
	var errno syscall.Errno
	if errors.As(s.err, &errno) && isWindowsPipeClosed(errno) {
		return fileFormLine("EPIPE")
	}
	if s.kind == handleFile || s.kind == handlePipe && isWindows {
		return fileFormLine(nodefs.ErrnoCode(s.err))
	}
	return "Error: write " + nodefs.SystemErrorName(s.err)
}

// fileFormLine is uvException's message for a failed fs.writeSync.
func fileFormLine(code string) string {
	return "Error: " + code + ": " + nodefs.Describe(code) + ", write"
}
