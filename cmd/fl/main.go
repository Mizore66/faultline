package main

import (
	"errors"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/Mizore66/faultline/internal/cli"
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

func main() {
	// Node ignores SIGPIPE: a closed stdout pipe is a write error (EPIPE),
	// and the unhandled 'error' event exits 1 (KNOWN_DIFFERENCES.md).
	signal.Ignore(syscall.SIGPIPE)
	args := os.Args[1:]
	if runtime.GOOS != "windows" {
		// process.argv decodes the raw bytes as UTF-8 with replacement.
		for i, a := range args {
			args[i] = nodefs.DecodeUTF8([]byte(a))
		}
	}
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
