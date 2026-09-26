package main

import (
	"errors"
	"io"
	"os"
	"syscall"

	"github.com/Mizore66/faultline/internal/nodefs"
)

// stdoutWriter remembers the first write error, like the 'error' event
// Node's process.stdout emits.
type stdoutWriter struct {
	w   io.Writer
	err error
}

func (s *stdoutWriter) Write(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	n, err := s.w.Write(p)
	if err != nil {
		s.err = err
	}
	return n, err
}

// writeErrorLine is the error line of Node's unhandled stdout 'error'
// event. A file (or non-terminal character device) stdout is a
// SyncWriteStream, whose fs.writeSync throws "Error: CODE: desc, write";
// a pipe, socket or terminal is a libuv stream, whose error is
// "Error: write CODE". Windows pipe errors 109 and 232 are EPIPE for writes
// (uv_translate_write_sys_error).
func writeErrorLine(stdout *os.File, err error) string {
	code := nodefs.ErrnoCode(err)
	var errno syscall.Errno
	if errors.As(err, &errno) && isWindowsPipeClosed(errno) {
		code = "EPIPE"
	}
	if info, statErr := stdout.Stat(); statErr == nil && (info.Mode().IsRegular() || info.Mode()&os.ModeCharDevice != 0 && !isTerminal(stdout)) {
		return "Error: " + code + ": " + nodefs.Describe(code) + ", write"
	}
	return "Error: write " + code
}
