package main

import (
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Mizore66/faultline/internal/cli"
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

func main() {
	// Node ignores SIGPIPE: a closed stdout pipe is a write error (EPIPE),
	// and the unhandled 'error' event exits 1 (KNOWN_DIFFERENCES.md).
	signal.Ignore(syscall.SIGPIPE)
	args := nodeArgv()
	stdout := &stdoutWriter{w: os.Stdout}
	code := cli.Run(args, stdout, os.Stderr)
	if stdout.err != nil {
		msg := "Error: " + stdout.err.Error()
		if errors.Is(stdout.err, syscall.EPIPE) {
			msg = "Error: write EPIPE"
		}
		io.WriteString(os.Stderr, msg+"\n")
		os.Exit(1)
	}
	os.Exit(code)
}
