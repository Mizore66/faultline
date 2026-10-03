package main

import (
	"errors"
	"os"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/sigexit"
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
// Node's pipes on Windows write synchronously too and report the file form;
// a Windows console is written as UTF-16 with WriteConsoleW (writeTTY).
// The first error is kept, like the 'error' event.
//
// Node creates process.stdout on first use, which for verify is the first
// write, after the checks have run, so the descriptor is classified then
// and not at startup (a terminal can hang up in between). libuv opens a
// terminal or pipe that fd 1 has read-only as a stream that is not
// writable, and a write to it fails with EPIPE without reaching the
// descriptor.
type stdoutWriter struct {
	f          *os.File
	kind       handleType
	classified bool
	err        error
}

func newStdoutWriter(f *os.File) *stdoutWriter {
	return &stdoutWriter{f: f}
}

func (s *stdoutWriter) Write(p []byte) (int, error) {
	sigexit.Hold()
	if !s.classified {
		s.kind, s.classified = guessHandle(s.f), true
		if (s.kind == handleTTY || s.kind == handlePipe) && readOnly(s.f) {
			s.err = syscall.EPIPE
		}
	}
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
	case handleTTY:
		if err := writeTTY(s.f, p); err != nil {
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

// stderrWriter is process.stderr for the CLI's own error output. Unlike
// process.stdout, Node creates it while its modules load (util/colors reads
// it), so fd 2 is classified at startup. A UDP socket or a descriptor libuv
// cannot classify (an AF_UNIX datagram or SOCK_SEQPACKET socket) gets the
// dummy Writable, which discards everything; otherwise the bytes go to fd 2
// as before. V8's fatal reports and the stdout error line are printed with C
// stdio in Node and reach fd 2 whatever it is, so they do not come here.
type stderrWriter struct {
	f       *os.File
	discard bool
}

func newStderrWriter(f *os.File) *stderrWriter {
	kind := guessHandle(f)
	return &stderrWriter{f: f, discard: kind == handleUDP || kind == handleUnknown}
}

func (s *stderrWriter) Write(p []byte) (int, error) {
	if s.discard {
		return len(p), nil
	}
	return s.f.Write(p)
}

// consoleUTF16 is the text conversion in libuv's uv__tty_write_bufs
// (src/win/tty.c) for a Windows console: UTF-8 to UTF-16, with its EOL
// conversion. An LF not preceded by CR is written as CR LF, a CR right after
// an LF is dropped, and any other CR or LF is written as it is. eol is
// tty.wr.previous_eol, kept across writes.
func consoleUTF16(p []byte, eol *rune) []uint16 {
	var units []uint16
	for len(p) > 0 {
		r, size := utf8.DecodeRune(p)
		p = p[size:]
		if r == '\n' || r == '\r' {
			switch {
			case r == '\n' && *eol != '\r':
				units = append(units, '\r', '\n')
			case r == '\r' && *eol == '\n':
			default:
				units = append(units, uint16(r))
			}
			*eol = r
			continue
		}
		*eol = 0
		units = utf16.AppendRune(units, r)
	}
	return units
}
