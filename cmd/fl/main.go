package main

import (
	"io"
	"os"

	"github.com/Mizore66/faultline/internal/cli"
)

func main() {
	// Node's signal dispositions: SIGPIPE ignored, so a closed stdout pipe is
	// a write error (EPIPE) and the unhandled 'error' event exits 1
	// (KNOWN_DIFFERENCES.md).
	installSignals()
	args := nodeArgv()
	stdout := &stdoutWriter{w: os.Stdout}
	code := cli.Run(args, stdout, os.Stderr)
	if stdout.err != nil {
		io.WriteString(os.Stderr, writeErrorLine(os.Stdout, stdout.err)+"\n")
		os.Exit(1)
	}
	os.Exit(code)
}
