package main

import (
	"io"
	"os"

	_ "github.com/Mizore66/faultline/internal/boot" // Node's signal state, first
	"github.com/Mizore66/faultline/internal/cli"
)

func main() {
	args := nodeArgv()
	stdout := &stdoutWriter{w: os.Stdout}
	code := cli.Run(args, stdout, os.Stderr)
	if stdout.err != nil {
		io.WriteString(os.Stderr, writeErrorLine(os.Stdout, stdout.err)+"\n")
		os.Exit(1)
	}
	os.Exit(code)
}
